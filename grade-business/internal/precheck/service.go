package precheck

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/filingaudit"
	"grade-business/internal/filingpolicy"
	"grade-business/internal/httpx"
)

var ErrNotFound = errors.New("precheck filing or job not found")

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

type Finding struct {
	RuleID       string `json:"ruleId"`
	Message      string `json:"message"`
	AttachmentID string `json:"attachmentId,omitempty"`
}
type Job struct {
	ID       string               `json:"id"`
	Status   string               `json:"status"`
	Stale    bool                 `json:"stale"`
	Findings []Finding            `json:"findings,omitempty"`
	Checks   []filingaudit.Result `json:"checks,omitempty"`
	Source   string               `json:"checkSource,omitempty"`
	Fallback string               `json:"fallbackReason,omitempty"`
	Failure  string               `json:"failure,omitempty"`
	Stage    string               `json:"progressStage,omitempty"`
	Advice   *Recommendation      `json:"recommendation,omitempty"`
}
type Service struct {
	pool     *pgxpool.Pool
	source   Source
	content  ContentSource
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
	var job Job
	const restart = `current.status='failed' OR (current.status='succeeded' AND
		(current.check_profile<>EXCLUDED.check_profile OR
		  ($6 AND current.check_results='[]'::jsonb) OR
		  ($8 AND current.fallback_reason<>'')))`
	err = s.pool.QueryRow(ctx, `INSERT INTO filing_prechecks AS current
		(filing_id,revision,materials,policy_version,policy_rules,check_profile) VALUES($1,$2,$3,$4,$5,$7)
		ON CONFLICT (filing_id,revision,policy_version) DO UPDATE SET
		status=CASE WHEN `+restart+` THEN 'queued' ELSE current.status END,
		attempt=CASE WHEN `+restart+` THEN 0 ELSE current.attempt END,
		failure=CASE WHEN `+restart+` THEN '' ELSE current.failure END,
		fallback_reason=CASE WHEN `+restart+` THEN '' ELSE current.fallback_reason END,
		progress_stage=CASE WHEN `+restart+` THEN '' ELSE current.progress_stage END,
		check_profile=CASE WHEN `+restart+` THEN EXCLUDED.check_profile ELSE current.check_profile END
		RETURNING id::text,status,findings,check_results,check_source,fallback_reason,failure,progress_stage`,
		id, snapshot.Revision, snapshot.Materials, policy.Version, policy.For(filingpolicy.Initial),
		s.content != nil, s.profile(), s.assessor != nil).
		Scan(&job.ID, &job.Status, &job.Findings, &job.Checks, &job.Source, &job.Fallback, &job.Failure, &job.Stage)
	return job, err
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
	err = s.pool.QueryRow(ctx, `SELECT id::text,status,
 revision<>$2 OR policy_version<>(SELECT version FROM filing_material_policy WHERE singleton) OR check_profile<>$4,
 findings,check_results,check_source,fallback_reason,failure,progress_stage
 FROM filing_prechecks
 WHERE filing_id=$1 AND ($3::uuid IS NULL OR id=$3::uuid) ORDER BY created_at DESC,id DESC LIMIT 1`,
		id, snapshot.Revision, wanted, s.profile()).
		Scan(&job.ID, &job.Status, &job.Stale, &job.Findings, &job.Checks,
			&job.Source, &job.Fallback, &job.Failure, &job.Stage)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return job, err
}

// startHTTP 受理空请求体的业务预审动作。
// 参数 w 和 r 是响应和携带备案标识的请求。
// 返回值 无。
func (s *Service) startHTTP(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 {
		httpx.Error(w, 400, "invalid_input", "发起预审无需额外字段")
		return
	}
	job, err := s.Start(r.Context(), r.PathValue("id"))
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
 lease_until=NULL,progress_stage=''
 WHERE status='running' AND attempt>=3 AND lease_until<now()`); err != nil {
		return err
	}
	var id, filingID string
	var revision int64
	var attempt int
	var materials []Material
	var rules []filingpolicy.Rule
	lease := 60 * time.Second
	if s.assessor != nil {
		lease += s.assessor.Timeout()
	}
	err := s.pool.QueryRow(ctx, `WITH target AS (SELECT id FROM filing_prechecks WHERE status='queued' OR
 (status='running' AND attempt<3 AND lease_until<now()) ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE filing_prechecks p SET status='running',attempt=p.attempt+1,progress_stage='materials',
 lease_until=now()+$1::integer*interval '1 second'
 FROM target WHERE p.id=target.id
 RETURNING p.id::text,p.filing_id::text,p.revision,p.attempt,p.materials,p.policy_rules`, int(lease.Seconds())).
		Scan(&id, &filingID, &revision, &attempt, &materials, &rules)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	result, err := s.checkContents(ctx, filingID, revision, materials, func(stage string) error {
		return s.advanceProgress(ctx, id, attempt, stage)
	})
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
		check_results=$5,check_source=$6,check_profile=$7,failure=$8,fallback_reason=$9,
		lease_until=NULL,progress_stage=''
		WHERE id=$1 AND attempt=$2 AND status='running'`,
		id, attempt, status, evaluateWithPolicy(materials, rules), result.checks, result.source,
		s.profile(), result.failure, result.fallback)
	return err
}

// advanceProgress 写入当前执行轮次的实际进度。
// 参数 ctx、id、attempt、stage 分别为执行上下文、任务、租约轮次和即将开始的阶段。
// 返回值 阶段写入错误，或租约失效错误。
func (s *Service) advanceProgress(ctx context.Context, id string, attempt int, stage string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE filing_prechecks SET progress_stage=$3
		WHERE id=$1 AND attempt=$2 AND status='running' AND lease_until>now()`, id, attempt, stage)
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
// 参数 ctx、id、revision、materials 为执行上下文、备案、版本和快照，progress 记录当前阶段。
// 返回值 内容结论、回退或失败原因，以及无法更新任务状态时的错误。
func (s *Service) checkContents(ctx context.Context, id string, revision int64,
	materials []Material, progress func(string) error,
) (contentResult, error) {
	result := contentResult{checks: []filingaudit.Result{}}
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
	stage := "local_rules"
	if s.assessor != nil {
		if err := progress("deepseek"); err != nil {
			return result, err
		}
		modelContext, cancelModel := context.WithTimeout(ctx, s.assessor.Timeout())
		defer cancelModel()
		checks, err := s.assessor.Assess(modelContext, docs, unit)
		if err == nil {
			return contentResult{checks: checks, source: "deepseek"}, nil
		}
		stage = "fallback"
		if errors.Is(err, filingaudit.ErrNoEvidence) {
			result.fallback = "未提取到可送检的材料，已执行本地规则。"
		} else {
			slog.Warn("DeepSeek 备案预审调用失败", "cause", err)
			result.fallback = "DeepSeek 暂不可用，已自动完成本地规则审核。"
		}
	}
	if err := progress(stage); err != nil {
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
