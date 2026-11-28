package precheck

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/filingaudit"
	"grade-business/internal/filingpolicy"
	"grade-business/internal/httpx"
)

var ErrNotFound = errors.New("precheck filing or job not found")
var errPrecheckInProgress = errors.New("existing precheck is already running")

type Material = filingpolicy.Material
type Snapshot struct {
	Revision  int64
	Materials []Material
}
type Source func(context.Context, string) (Snapshot, error)

// ContentSource 读取预审快照中已经登记的原件和所关联的单位。
type ContentSource func(context.Context, string, []Material) ([]filingaudit.Document, filingaudit.Unit, error)

// Assessor 为备案原件生成有出处的模型结论并描述当前配置。
type Assessor interface {
	Assess(context.Context, []filingaudit.Document, filingaudit.Unit) ([]filingaudit.Result, error)
	Profile() string
	Timeout() time.Duration
}

// batchAssessor 支持逐件结果和终审阶段的持久化回调。
type batchAssessor interface {
	AssessBatch(context.Context, []filingaudit.Document, filingaudit.Unit, filingaudit.BatchProgress) (filingaudit.BatchResult, error)
}

type Finding struct {
	RuleID       string `json:"ruleId"`
	Message      string `json:"message"`
	AttachmentID string `json:"attachmentId,omitempty"`
}
type Job struct {
	ID                  string                       `json:"id"`
	Status              string                       `json:"status"`
	Stale               bool                         `json:"stale"`
	Findings            []Finding                    `json:"findings,omitempty"`
	Checks              []filingaudit.Result         `json:"checks,omitempty"`
	Source              string                       `json:"checkSource,omitempty"`
	Fallback            string                       `json:"fallbackReason,omitempty"`
	Failure             string                       `json:"failure,omitempty"`
	Stage               string                       `json:"progressStage,omitempty"`
	Advice              *Recommendation              `json:"recommendation,omitempty"`
	MaterialReviews     []filingaudit.MaterialReview `json:"materialReviews,omitempty"`
	MaterialReviewTotal int                          `json:"materialReviewTotal,omitempty"`
}
type Service struct {
	pool     *pgxpool.Pool
	source   Source
	content  ContentSource
	unit     func(context.Context, string) (filingaudit.Unit, error)
	assessor Assessor
}

// New 创建负责持久化备案预审任务的服务。
// 参数 pool、source 为业务库和材料快照，content 可提供实际材料正文。
// 返回值 可注册业务接口和运行异步任务的预审服务。
func New(pool *pgxpool.Pool, source Source, content ...ContentSource) *Service {
	service := &Service{pool: pool, source: source}
	if len(content) > 0 {
		service.content = content[0]
	}
	return service
}

// WithAssessor 在启动后台任务前选择实际模型接口。
// 参数 assessor 为配置好的模型实现。
// 返回值 无。
func (s *Service) WithAssessor(assessor Assessor) { s.assessor = assessor }

// WithUnitSource 用途 在运行任务和读取结果时检查当前备案的单位名录。
// 参数 source 为不读取原件、只查询当前名录事实的函数。
// 返回值 无。
func (s *Service) WithUnitSource(source func(context.Context, string) (filingaudit.Unit, error)) {
	s.unit = source
}

// profile 返回结果应使用的模型或本地规则版本。
// 参数 无。
// 返回值 可判断历史结果是否仍可复用的版本标识。
func (s *Service) profile() string {
	if s.assessor != nil {
		return s.assessor.Profile()
	}
	if s.content != nil {
		return "local-rules-v1"
	}
	return ""
}

// Register 注册预审创建、轮询与近期结果查询接口。
// 参数 mux 为业务路由表。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/filings/{id}/prechecks", s.startHTTP)
	mux.HandleFunc("GET /api/v1/filings/{id}/prechecks/{jobId}", s.getHTTP)
}

// Start 将指定备案的当前材料版本提交到预审队列。
// 参数 ctx 控制读写，id 为备案标识。
// 返回值 对应版本的任务或备案、数据库错误。
func (s *Service) Start(ctx context.Context, id string) (Job, error) {
	return s.start(ctx, id, false)
}

// start 用途 按当前材料与政策快照排队，按需开启显式重审。
// 参数 ctx、id 为操作上下文和备案标识，force 表示跳过旧批次的 AI 结果。
// 返回值 对应版本的任务，或读取与保存错误。
func (s *Service) start(ctx context.Context, id string, force bool) (Job, error) {
	snapshot, err := s.source(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if snapshot.Materials == nil {
		snapshot.Materials = []Material{}
	}
	policy, err := filingpolicy.Load(ctx, s.pool, false)
	if err != nil {
		return Job{}, err
	}
	return s.startBatch(ctx, id, snapshot, policy, force)
}

// Get 读取指定备案中的一条预审任务并比较当前材料版本。
// 参数 ctx 控制读写，id 为备案标识，jobID 为空时查最新任务。
// 返回值 任务公开状态，或不存在和查询错误。
func (s *Service) Get(ctx context.Context, id, jobID string) (Job, error) {
	snapshot, err := s.source(ctx, id)
	if err != nil {
		return Job{}, err
	}
	var wanted any
	if jobID != "" {
		var parsed pgtype.UUID
		if len(jobID) != 36 || parsed.Scan(jobID) != nil || !parsed.Valid {
			return Job{}, ErrNotFound
		}
		wanted = jobID
	}
	var job Job
	var unitKey string
	err = s.pool.QueryRow(ctx, `SELECT id::text,status,
 revision<>$2 OR policy_version<>(SELECT version FROM filing_material_policy WHERE singleton) OR check_profile<>$4,
 findings,check_results,check_source,fallback_reason,failure,progress_stage,material_reviews,material_review_total,
 unit_fingerprint
 FROM (
   SELECT `+readBatchFields+` FROM filing_prechecks WHERE filing_id=$1
   UNION ALL
   SELECT `+readBatchFields+` FROM filing_precheck_history WHERE filing_id=$1
 ) AS batches
 WHERE filing_id=$1 AND ($3::uuid IS NULL OR id=$3::uuid) ORDER BY created_at DESC,id DESC LIMIT 1`,
		id, snapshot.Revision, wanted, s.profile()).
		Scan(&job.ID, &job.Status, &job.Stale, &job.Findings, &job.Checks,
			&job.Source, &job.Fallback, &job.Failure, &job.Stage, &job.MaterialReviews, &job.MaterialReviewTotal, &unitKey)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err == nil && !job.Stale && s.unit != nil && job.Status == "succeeded" {
		job.Stale, err = s.unitChanged(ctx, id, unitKey, job.Source)
	}
	return job, err
}

// unitChanged 用途 判断保存结果对应的审核名录是否仍有效。
// 参数 ctx、id、key、source 为查询上下文、备案、已保存名录指纹和结论来源。
// 返回值 审核名录是否过时及查询错误。
func (s *Service) unitChanged(ctx context.Context, id, key, source string) (bool, error) {
	if key == "" {
		return source == "deepseek", nil
	}
	unit, err := s.unit(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	return unitFingerprint(unit) != key, err
}

// startHTTP 用途 受理空请求体的业务预审动作及可选的显式重审。
// 参数 w 和 r 是响应和携带备案标识的请求。
// 返回值 无。
func (s *Service) startHTTP(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 {
		httpx.Error(w, 400, "invalid_input", "发起预审无需额外字段")
		return
	}
	var force bool
	if values, ok := r.URL.Query()["force"]; ok {
		if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			httpx.Error(w, 400, "invalid_input", "force 必须是 true 或 false")
			return
		}
		force = values[0] == "true"
	}
	job, err := s.start(r.Context(), r.PathValue("id"), force)
	reply(w, 202, job, err)
}

// getHTTP 返回可用于页面轮询的精简预审数据。
// 参数 w 和 r 是响应和携带备案及任务标识的请求。
// 返回值 无。
func (s *Service) getHTTP(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("jobId")
	if jobID == "latest" {
		jobID = ""
	}
	job, err := s.Get(r.Context(), r.PathValue("id"), jobID)
	reply(w, 200, job, err)
}

// reply 输出任务公开状态或短错误提示。
// 参数 w 为响应对象，status 为成功状态，job 和 err 是查询结果。
// 返回值 无。
func reply(w http.ResponseWriter, status int, job Job, err error) {
	if errors.Is(err, errPrecheckInProgress) {
		httpx.Error(w, http.StatusConflict, "precheck_in_progress", "普通预审正在执行，完成后可发起强制重审")
		return
	}
	if errors.Is(err, ErrNotFound) {
		httpx.Error(w, 404, "not_found", "未找到该备案预审任务")
		return
	}
	if err != nil {
		httpx.Error(w, 500, "internal_error", "暂时无法读取或提交预审任务")
		return
	}
	job.Advice = recommendReview(job)
	httpx.JSON(w, status, job)
}

// Run 在应用生命周期内轮询并计算待办预审。
// 参数 ctx 用于停止任务循环或取消当前数据库操作。
// 返回值 无。
func (s *Service) Run(ctx context.Context) {
	var workers sync.WaitGroup
	workers.Go(func() { s.runWorker(ctx) })
	s.runWorker(ctx)
	workers.Wait()
}

// runWorker 循环领取独立批次，其他工作循环可以同时处理不同备案。
// 参数 ctx 为应用工作循环的生命周期。
// 返回值 无。
func (s *Service) runWorker(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := s.runOne(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("预审任务暂时无法处理，将在下轮继续")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// runOne 领取一个任务并以租约轮次保证只有当前执行者能保存结果。
// 参数 ctx 控制数据库操作。
// 返回值 数据库错误，成功或无待办任务返回空值。
func (s *Service) runOne(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `UPDATE filing_prechecks SET status='failed',failure='处理超时，可以重新发起预审',
 lease_until=NULL,claim_token=NULL,progress_stage=''
 WHERE status='running' AND attempt>=3 AND lease_until<now()`); err != nil {
		return err
	}
	run := aiExecution{service: s}
	var materials []Material
	var rules []filingpolicy.Rule
	lease := 60 * time.Second
	if s.assessor != nil {
		lease += s.assessor.Timeout()
	}
	err := s.pool.QueryRow(ctx, `WITH target AS (SELECT id FROM filing_prechecks WHERE status='queued' OR
 (status='running' AND attempt<3 AND lease_until<now()) ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE filing_prechecks p SET status='running',attempt=p.attempt+1,progress_stage='materials',
 claim_token=gen_random_uuid(),material_reviews='[]'::jsonb,material_review_total=0,
 lease_until=now()+$1::integer*interval '1 second'
 FROM target WHERE p.id=target.id
 RETURNING p.id::text,p.filing_id::text,p.revision,p.claim_token::text,p.materials,p.policy_rules,
 p.policy_version,p.force_ai`, int(lease.Seconds())).
		Scan(&run.job, &run.filing, &run.revision, &run.claim, &materials, &rules, &run.policy, &run.force)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	recordProgress := func(stage string, reviews []filingaudit.MaterialReview, total int) error {
		return s.advanceProgress(ctx, run.job, run.claim, stage, reviews, total, lease)
	}
	result, err := s.checkContents(ctx, run.filing, run.revision, materials, &run, recordProgress)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	status := "succeeded"
	if result.failure != "" {
		status = "failed"
	}
	_, err = s.pool.Exec(ctx, `UPDATE filing_prechecks SET status=$3,findings=$4,
		check_results=$5,check_source=$6,check_profile=$7,failure=$8,fallback_reason=$9,unit_fingerprint=$10,
		lease_until=NULL,claim_token=NULL,progress_stage=''
		WHERE id=$1 AND claim_token=$2 AND status='running' AND lease_until>now()`,
		run.job, run.claim, status, evaluateWithPolicy(materials, rules), result.checks, result.source,
		s.profile(), result.failure, result.fallback, run.unitKey)
	return err
}

// advanceProgress 写入当前领取者的阶段和逐件结果并续租。
// 参数 ctx、id、claim、stage、reviews、total、lease 为执行上下文、批次、领取凭据、阶段、逐件记录、总数和租期。
// 返回值 阶段写入错误，或租约失效错误。
func (s *Service) advanceProgress(ctx context.Context, id, claim, stage string,
	reviews []filingaudit.MaterialReview, total int, lease time.Duration,
) error {
	if reviews == nil {
		reviews = []filingaudit.MaterialReview{}
	}
	tag, err := s.pool.Exec(ctx, `UPDATE filing_prechecks SET progress_stage=$3,
		material_reviews=CASE WHEN $5>0 THEN $4::jsonb ELSE material_reviews END,
		material_review_total=CASE WHEN $5>0 THEN $5 ELSE material_review_total END,
		lease_until=now()+$6::integer*interval '1 second'
		WHERE id=$1 AND claim_token=$2 AND status='running' AND lease_until>now()`,
		id, claim, stage, reviews, total, int(lease.Seconds()))
	if err == nil && tag.RowsAffected() == 0 {
		return errors.New("备案预审任务租约已失效")
	}
	return err
}

type contentResult struct {
	checks                    []filingaudit.Result
	source, fallback, failure string
}

// checkContents 优先使用 DeepSeek，模型不可用时自动完成本地规则预审。
// 参数 ctx、id、revision、materials 为执行上下文、备案、版本和快照，
// run 记录本次领取，progress 记录阶段。
// 返回值 内容结论、回退或失败原因，以及无法更新任务状态时的错误。
func (s *Service) checkContents(ctx context.Context, id string, revision int64,
	materials []Material, run *aiExecution, progress filingaudit.BatchProgress,
) (result contentResult, resultErr error) {
	defer func() {
		if recover() != nil {
			slog.Error("备案材料预审发生内部异常", "filingId", id, "revision", revision)
			result = contentResult{checks: []filingaudit.Result{}, failure: "材料正文暂时无法检查，请重新发起预审。"}
			resultErr = nil
		}
	}()
	result = contentResult{checks: []filingaudit.Result{}}
	if s.content == nil {
		return result, nil
	}
	deadline, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	current, err := s.source(deadline, id)
	if errors.Is(err, ErrNotFound) || (err == nil && current.Revision != revision) {
		return result, nil
	}
	if err != nil {
		result.failure = "材料版本暂时无法读取，请重新发起预审。"
		return result, nil
	}
	docs, unit, err := s.content(deadline, id, materials)
	if err != nil {
		result.failure = "材料正文暂时无法检查，请重新发起预审。"
		return result, nil
	}
	if run != nil {
		run.unit, run.unitKey = unit, unitFingerprint(unit)
	}
	stage := "local_rules"
	if s.assessor != nil {
		checks, modelErr, progressErr := s.assessContents(ctx, docs, materials, unit, run, progress)
		if errors.Is(progressErr, errAIInputSuperseded) {
			return result, nil
		}
		if progressErr != nil {
			return result, progressErr
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if modelErr == nil {
			return contentResult{checks: checks, source: "deepseek"}, nil
		}
		stage = "fallback"
		if errors.Is(modelErr, filingaudit.ErrNoEvidence) {
			result.fallback = "有材料缺少可核对的原件依据，已执行本地规则。"
		} else {
			slog.Warn("DeepSeek 备案预审调用未完成")
			result.fallback = "DeepSeek 未完成逐件审核或终审，已执行本地规则。"
		}
	}
	if err := progress(stage, nil, 0); err != nil {
		return result, err
	}
	result.checks, result.source = localChecks(docs, unit), "local_rules"
	return result, nil
}

// localChecks 执行本地规则供人工复核或模型失败时参考。
// 参数 docs、unit 为已读取的材料和所属单位。
// 返回值 北京时间下的逐项内容核对结果。
func localChecks(docs []filingaudit.Document, unit filingaudit.Unit) []filingaudit.Result {
	return filingaudit.Evaluate(docs, unit, time.Now().In(time.FixedZone("UTC+8", 8*60*60)))
}

// evaluateWithPolicy 合并基础备案材料规则和已锁定版本的附加规则。
// 参数 materials 为任务附件快照，rules 为发起预审时的附加规则。
// 返回值 携带真实附件标识的核对结果。
func evaluateWithPolicy(materials []Material, rules []filingpolicy.Rule) []Finding {
	findings := evaluate(materials)
	for i, rule := range rules {
		id := "configured." + strconv.Itoa(i+1)
		attachment := filingpolicy.Match(rule, materials)
		if attachment == "" {
			findings = append(findings, Finding{RuleID: id + ".missing", Message: "缺少" + rule.Label})
		} else {
			findings = append(findings, Finding{RuleID: id + ".present", Message: "已上传" + rule.Label, AttachmentID: attachment})
		}
	}
	return findings
}

// evaluate 核对当前材料快照的必要单件或已验证备案包。
// 参数 materials 是备案发起预审时已经保存的材料证据。
// 返回值 规则命中与真实附件引用。
func evaluate(materials []Material) []Finding {
	results := make([]Finding, 0, 2)
	for _, rule := range []struct{ kind, label string }{{"filing_form", "备案表"}, {"classification_report", "定级报告"}} {
		result := Finding{RuleID: rule.kind + ".missing", Message: "缺少" + rule.label}
		for _, material := range materials {
			if material.Kind != rule.kind && material.Kind != "filing_package" {
				continue
			}
			result = Finding{RuleID: rule.kind + ".present", Message: "已上传" + rule.label, AttachmentID: material.ID}
			break
		}
		results = append(results, result)
	}
	return results
}
