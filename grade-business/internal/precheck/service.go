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
type Finding struct {
	RuleID       string `json:"ruleId"`
	Message      string `json:"message"`
	AttachmentID string `json:"attachmentId,omitempty"`
}
type Job struct {
	ID       string    `json:"id"`
	Status   string    `json:"status"`
	Stale    bool      `json:"stale"`
	Findings []Finding `json:"findings,omitempty"`
	Failure  string    `json:"failure,omitempty"`
}
type Service struct {
	pool   *pgxpool.Pool
	source Source
}

// New 创建负责持久化备案预审任务的服务。
// 参数 pool 为业务库，source 为获取真实材料版本的业务函数。
// 返回值 可注册业务接口和运行异步任务的预审服务。
func New(pool *pgxpool.Pool, source Source) *Service { return &Service{pool, source} }

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
	err = s.pool.QueryRow(ctx, `INSERT INTO filing_prechecks
 (filing_id,revision,materials,policy_version,policy_rules) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT (filing_id,revision,policy_version) DO UPDATE SET status=CASE WHEN filing_prechecks.status='failed' THEN 'queued' ELSE filing_prechecks.status END,
 attempt=CASE WHEN filing_prechecks.status='failed' THEN 0 ELSE filing_prechecks.attempt END,
 failure=CASE WHEN filing_prechecks.status='failed' THEN '' ELSE filing_prechecks.failure END
 RETURNING id::text,status,findings,failure`, id, snapshot.Revision, snapshot.Materials,
		policy.Version, policy.For(filingpolicy.Initial)).Scan(&job.ID, &job.Status, &job.Findings, &job.Failure)
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
 revision<>$2 OR policy_version<>(SELECT version FROM filing_material_policy WHERE singleton),findings,failure
 FROM filing_prechecks
 WHERE filing_id=$1 AND ($3::uuid IS NULL OR id=$3::uuid) ORDER BY created_at DESC,id DESC LIMIT 1`,
		id, snapshot.Revision, wanted).Scan(&job.ID, &job.Status, &job.Stale, &job.Findings, &job.Failure)
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
	if _, err := s.pool.Exec(ctx, `UPDATE filing_prechecks SET status='failed',failure='处理超时，可以重新发起预审',lease_until=NULL
 WHERE status='running' AND attempt>=3 AND lease_until<now()`); err != nil {
		return err
	}
	var id string
	var attempt int
	var materials []Material
	var rules []filingpolicy.Rule
	err := s.pool.QueryRow(ctx, `WITH target AS (SELECT id FROM filing_prechecks WHERE status='queued' OR
 (status='running' AND attempt<3 AND lease_until<now()) ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE filing_prechecks p SET status='running',attempt=p.attempt+1,lease_until=now()+interval '30 seconds'
 FROM target WHERE p.id=target.id RETURNING p.id::text,p.attempt,p.materials,p.policy_rules`).
		Scan(&id, &attempt, &materials, &rules)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE filing_prechecks SET status='succeeded',findings=$3,lease_until=NULL
 WHERE id=$1 AND attempt=$2 AND status='running'`, id, attempt, evaluateWithPolicy(materials, rules))
	return err
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
