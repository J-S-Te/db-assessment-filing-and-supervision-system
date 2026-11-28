package remediations

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"grade-business/internal/access"
	"grade-business/internal/cases"

	"github.com/jackc/pgx/v5"
)

// materialKind 校验三类必传整改材料及可选高风险证据。
// 参数 kind 为表单提交的材料类别。
// 返回值 类别为受支持的材料时返回真。
func materialKind(kind string) bool {
	return kind == "plan" || kind == "responsibility" || kind == "schedule" || kind == "high_risk_evidence"
}

// attachments 加载指定整改方案已上传的材料摘要。
// 参数 ctx 含企业端可选范围，id 为方案标识。
// 返回值 该计划所拥有的文件元数据，或归属和读取错误。
func (s *Service) attachments(ctx context.Context, id string) (collection[cases.Attachment], error) {
	if err := s.ensurePlan(ctx, id); err != nil {
		return collection[cases.Attachment]{}, err
	}
	rows, err := s.db.Query(ctx, `SELECT a.id::text,a.kind,a.file_name,a.size_bytes,a.uploaded_at `+joins+`
		JOIN remediation_attachments a ON a.plan_id=p.id
		WHERE p.id=$1 AND ($2='' OR COALESCE(p.frozen_organization_name,f.organization_name)=$2)
		AND `+access.FilingDistrictScope("f.id", "$3", false)+` ORDER BY a.uploaded_at DESC,a.id DESC`,
		id, operatorOrganization(ctx), access.Districts(ctx))
	if err != nil {
		return collection[cases.Attachment]{}, err
	}
	defer rows.Close()
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[cases.Attachment])
	return collection[cases.Attachment]{Items: items}, err
}

// canStageMaterial 在写临时文件前检查计划的可见性和材料上传状态。
// 参数 ctx 含企业端可选范围，id 为整改规划标识。
// 返回值 可暂存时为 nil，失败时为归属、状态或数据库错误。
func (s *Service) canStageMaterial(ctx context.Context, id string) error {
	var status string
	err := s.db.QueryRow(ctx, `SELECT p.status `+joins+`
		WHERE p.id=$1 AND ($2='' OR COALESCE(p.frozen_organization_name,f.organization_name)=$2)`, id, operatorOrganization(ctx)).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err == nil && !editable(status) {
		return errConflict
	}
	return err
}

// addMaterial 登记通过原有安全校验的整改材料，并推进计划版本。
// 参数 ctx 含企业端可选范围，id 为方案标识，report 为页面上的报告，item 和 key 为已校验材料。
// 返回值 新登记的材料，或状态冲突、写入错误。
func (s *Service) addMaterial(ctx context.Context, id, report string,
	item cases.Attachment, key string,
) (cases.Attachment, error) {
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		current, err := lockPlan(ctx, tx, id)
		if err != nil {
			return err
		}
		if !editable(current.Status) {
			return errConflict
		}
		if !strings.EqualFold(current.ReportID, report) {
			return errMaterialReportChanged
		}
		if current.Status == draft && operatorOrganization(ctx) != "" {
			if err := lockEligible(ctx, tx, report); err != nil {
				return err
			}
		}
		err = tx.QueryRow(ctx, `INSERT INTO remediation_attachments
			(plan_id,kind,file_name,file_key,size_bytes) VALUES($1,$2,$3,$4,$5)
			RETURNING id::text,uploaded_at`, id, item.Kind, item.FileName, key, item.Size).
			Scan(&item.ID, &item.UploadedAt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE remediation_plans SET version=version+1 WHERE id=$1`, id)
		return err
	})
	return item, err
}

// discardMaterial 仅在能确认数据库未登记时清理本次落盘文件。
// 参数 ctx 为原请求上下文，key 为本次上传生成的内部键。
// 返回值 清理结果；事务状态不确定时保留数据并返回数据库错误。
func (s *Service) discardMaterial(ctx context.Context, key string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	var exists bool
	err := s.db.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM remediation_attachments
		WHERE file_key=$1)`, key).Scan(&exists)
	if err != nil || exists {
		return err
	}
	return s.records.RemoveUnregisteredAttachment(key)
}

// downloadMaterial 只向持有指定附件的整改计划提供下载。
// 参数 w 和 r 为响应和可选企业范围的请求，id 为计划标识，attachmentID 为附件标识。
// 返回值 成功时返回 nil，未找到、归属不匹配和读取失败时返回错误。
func (s *Service) downloadMaterial(w http.ResponseWriter, r *http.Request, id, attachmentID string) error {
	var item cases.Attachment
	var key string
	err := s.db.QueryRow(r.Context(), `SELECT a.id::text,a.kind,a.file_name,a.size_bytes,a.uploaded_at,a.file_key `+joins+`
		JOIN remediation_attachments a ON a.plan_id=p.id
		WHERE a.id=$1 AND p.id=$2 AND ($3='' OR COALESCE(p.frozen_organization_name,f.organization_name)=$3)
		AND `+access.FilingDistrictScope("f.id", "$4", false), attachmentID, id,
		operatorOrganization(r.Context()), access.Districts(r.Context())).
		Scan(&item.ID, &item.Kind, &item.FileName, &item.Size, &item.UploadedAt, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	return s.records.ServeBusinessAttachment(w, r, item, key)
}
