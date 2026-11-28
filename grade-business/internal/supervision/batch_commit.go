package supervision

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/access"
	"grade-business/internal/httpx"
)

// confirmBatch 同一预览只下发一次，收件名单或模板变化时要求重新核对。
// 参数 ctx 控制事务，input 包含原筛选条件和预览摘要。
// 返回值 当前或先前的完整批次结果，或未写入任何发送的业务冲突。
func (s *Service) confirmBatch(ctx context.Context, input batchConfirm) (batchResult, error) {
	var result batchResult
	hash, err := supervisionHash(input.batchCriteria)
	if err != nil {
		return result, err
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		// 等待同一预览的前一事务结束后再读回执，避免并发重放遭遇瞬时范围冲突。
		if _, lockErr := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,109))`, input.Token); lockErr != nil {
			return lockErr
		}
		replay, readErr := loadBatchResult(ctx, tx, input.Token, hash)
		if !errors.Is(readErr, pgx.ErrNoRows) {
			result = replay
			return readErr
		}
		plan, planErr := buildBatchPlan(ctx, tx, input.batchCriteria, true)
		if planErr != nil {
			return planErr
		}
		token, digestErr := plan.digest()
		if digestErr != nil {
			return digestErr
		}
		if token != input.Token {
			return errScope
		}
		result, planErr = s.saveBatch(ctx, tx, plan, input.Token, hash)
		return planErr
	})
	return result, err
}

// loadBatchResult 核对原输入、当前辖区及申请账号后返回原发放结果。
// 参数 ctx、tx 控制事务，token、hash 分别为收件快照和请求信息的摘要。
// 返回值 原回执、请求变化冲突或尚未处理的 ErrNoRows。
func loadBatchResult(ctx context.Context, tx pgx.Tx, token, hash string) (batchResult, error) {
	var result batchResult
	var inputHash string
	actor, _ := access.Current(ctx)
	err := tx.QueryRow(ctx, `SELECT b.input_hash,b.result FROM supervision_dispatch_batches b
		WHERE b.preview_token=$1 AND ($2::text[] IS NULL OR (
			b.actor_id=NULLIF($3,'')::uuid AND EXISTS (
				SELECT 1 FROM supervision_tasks t WHERE t.id=b.template_task_id AND `+
		access.FilingDistrictScope("t.filing_id", "$2", false)+`
			) AND NOT EXISTS (
				SELECT 1 FROM jsonb_array_elements(b.result->'items') AS recipients(item)
				WHERE NOT `+access.FilingDistrictScope("(item->>'filingId')::uuid", "$2", false)+`
			)
		))`, token, access.Districts(ctx), actor.ID).Scan(&inputHash, &result)
	if err == nil && inputHash != hash {
		return batchResult{}, errScope
	}
	return result, err
}

// saveBatch 原子建立批次、全部选定任务、通知及其回执。
// 参数 ctx、tx 为操作事务，plan、token、hash 为核对后的业务计划和输入标记。
// 返回值 全部发布及跳过结果，或可回滚的错误。
func (s *Service) saveBatch(ctx context.Context, tx pgx.Tx, plan batchPlan, token, hash string) (batchResult, error) {
	input := plan.input
	result := batchResult{Items: make([]batchDelivery, 0, len(plan.items))}
	name, actor := supervisionAuditActor(ctx, s.actor, "")
	err := tx.QueryRow(ctx, `INSERT INTO supervision_dispatch_batches(preview_token,input_hash,
		template_task_id,template_version,district,levels,title,due_on,actor,actor_id,actor_role)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,'')::uuid,NULLIF($11,'')) RETURNING id::text`,
		token, hash, input.TemplateTaskID, input.TemplateVersion, input.District, input.Levels, input.Title,
		input.DueOn, name, actor.ID, actor.Role).Scan(&result.ID)
	if err != nil {
		return batchResult{}, err
	}
	for _, item := range plan.items {
		if item.Status == "existing" {
			result.Items = append(result.Items, batchDelivery{item.FilingID, item.ExistingTaskID, "existing"})
			continue
		}
		child, err := s.publishBatchChild(ctx, tx, result.ID, plan, item)
		if err != nil {
			return batchResult{}, err
		}
		result.Items = append(result.Items, batchDelivery{item.FilingID, child, "published"})
		result.PublishedCount++
	}
	if result.PublishedCount == 0 {
		return batchResult{}, errScope
	}
	_, err = tx.Exec(ctx, `UPDATE supervision_dispatch_batches SET result=$2 WHERE id=$1`, result.ID, result)
	return result, err
}

// publishBatchChild 保留实际接收方快照、模板关联和下发留痕，复用单件站内投递。
// 参数 ctx、tx 为事务，batchID、plan、item 为批次、核对计划及目标备案。
// 返回值 已公开的子任务标识或唯一约束和写入错误。
func (s *Service) publishBatchChild(ctx context.Context, tx pgx.Tx, batchID string, plan batchPlan,
	item batchCandidate,
) (string, error) {
	var child string
	err := tx.QueryRow(ctx, `INSERT INTO supervision_tasks(request_id,dispatch_batch_id,filing_id,
		filing_number,system_name,organization_name,title,due_on,cycle_year,status,version,published_at)
		SELECT gen_random_uuid(),$1,$2::uuid,$3,$4,$5,$6,$7::date,EXTRACT(YEAR FROM $7::date)::int,
		       'open',2,clock_timestamp()
		WHERE `+access.FilingDistrictScope("$2::uuid", "$8", true)+`
		ON CONFLICT (filing_id,cycle_year) WHERE lead_alert_id IS NULL AND status<>'cancelled'
		DO NOTHING RETURNING id::text`, batchID, item.FilingID, item.Number, item.SystemName,
		item.OrganizationName, plan.input.Title, plan.input.DueOn, access.Districts(ctx)).Scan(&child)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errScope
	}
	if err != nil {
		return "", err
	}
	if err = s.persistBatchChild(ctx, tx, child, plan.sources); err != nil {
		return "", err
	}
	record := taskRecord{taskSummary: taskSummary{
		FilingNumber: item.Number, OrganizationName: item.OrganizationName, Title: plan.input.Title, DueOn: plan.input.DueOn,
	}}
	err = sendActionNotice(ctx, tx, record, actionInput{Action: "publish"}, mutation{child, "open", 2})
	return child, err
}

// persistBatchChild 保存两份经核验的原模板引用，以及新建、下发操作轨迹。
// 参数 ctx、tx 为批次事务，child、sources 是子任务和不可变源文件。
// 返回值 任一引用或留痕落库错误。
func (s *Service) persistBatchChild(ctx context.Context, tx pgx.Tx, child string, sources [2]string) error {
	batch := &pgx.Batch{}
	name, actor := supervisionAuditActor(ctx, s.actor, "")
	batch.Queue(`INSERT INTO supervision_dispatched_templates(task_id,kind,source_attachment_id)
		VALUES($1,'inspection_template',$2),($1,'summary_template',$3)`, child, sources[0], sources[1])
	batch.Queue(`INSERT INTO supervision_events(task_id,version,action,from_status,to_status,actor,actor_id,actor_role)
		VALUES($1,1,'create',NULL,'draft',$2,NULLIF($3,'')::uuid,NULLIF($4,'')),
		      ($1,2,'publish','draft','open',$2,NULLIF($3,'')::uuid,NULLIF($4,''))`, child, name, actor.ID, actor.Role)
	return tx.SendBatch(ctx, batch).Close()
}

// registerBatchRoutes 提供监管预览、同预览确认和模板来源查询。
// 参数 mux 是同源服务路由。
// 返回值 无。
func (s *Service) registerBatchRoutes(mux *http.ServeMux) {
	path := regulatorPath + "/batches"
	mux.HandleFunc("GET "+path+"/template-options", s.templateOptionsHTTP)
	mux.HandleFunc("POST "+path+"/preview", s.batchPreviewHTTP)
	mux.HandleFunc("POST "+path, s.batchConfirmHTTP)
}

// templateOptionsHTTP 读取现有已核验、可选为下发样板的任务。
// 参数 w、r 为 HTTP 响应与请求。
// 返回值 无。
func (s *Service) templateOptionsHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, 200, nil, errInput)
		return
	}
	result, err := s.batchTemplateOptions(r.Context())
	respond(w, 200, result, err)
}

// batchPreviewHTTP 仅接受结构化范围条件，并返回可审计预览。
// 参数 w、r 为 HTTP 响应与预览条件。
// 返回值 无。
func (s *Service) batchPreviewHTTP(w http.ResponseWriter, r *http.Request) {
	var input batchCriteria
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || !validateBatchCriteria(&input) {
		respond(w, 200, nil, errInput)
		return
	}
	result, err := s.previewBatch(r.Context(), input)
	respond(w, 200, result, err)
}

// batchConfirmHTTP 核对确认摘要和原范围后尝试原子下发任务。
// 参数 w、r 为 HTTP 响应与确认条件。
// 返回值 无。
func (s *Service) batchConfirmHTTP(w http.ResponseWriter, r *http.Request) {
	var input batchConfirm
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil ||
		!validateBatchCriteria(&input.batchCriteria) || !validBatchToken(input.Token) {
		respond(w, 201, nil, errInput)
		return
	}
	result, err := s.confirmBatch(r.Context(), input)
	respond(w, 201, result, err)
}
