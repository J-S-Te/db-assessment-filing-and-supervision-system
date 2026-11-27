package cases

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/access"
	"grade-business/internal/filingaudit"
	"grade-business/internal/httpx"
)

const materialReviewPath = "/api/v1/filings/{id}/prechecks/review"

type materialReview struct {
	RuleID     string    `json:"ruleId"`
	Status     string    `json:"status"`
	Remark     string    `json:"remark"`
	Version    int64     `json:"version"`
	Reviewer   string    `json:"reviewer"`
	ReviewedAt time.Time `json:"reviewedAt"`
}

type materialReviewItem struct {
	filingaudit.Rule
	Review *materialReview `json:"review,omitempty"`
}

type materialReviewInput struct {
	Status           string `json:"status"`
	Remark           string `json:"remark"`
	MaterialRevision *int64 `json:"materialRevision"`
	ExpectedVersion  *int64 `json:"expectedVersion"`
}

// registerMaterialReviews 挂载备案材料逐项审核的读取和保存接口。
// 参数 mux 为管理端业务路由。
// 返回值 无。
func (s *Service) registerMaterialReviews(mux *http.ServeMux) {
	mux.HandleFunc("GET "+materialReviewPath, s.listMaterialReviews)
	mux.HandleFunc("PUT "+materialReviewPath+"/{ruleId}", s.putMaterialReview)
}

// listMaterialReviews 返回当前有权备案的规则目录及对应材料版本的最新意见。
// 参数 w、r 为响应和携带备案标识的请求。
// 返回值 无。
func (s *Service) listMaterialReviews(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	revision, err := materialReviewRevision(ctx, s.db, id, false)
	if err != nil {
		respondError(w, err)
		return
	}
	reviews, err := s.materialReviews(ctx, id, revision)
	if err != nil {
		respondError(w, err)
		return
	}
	items := make([]materialReviewItem, 0, 11)
	for _, rule := range filingaudit.Rules() {
		items = append(items, materialReviewItem{Rule: rule, Review: reviews[rule.ID]})
	}
	httpx.JSON(w, http.StatusOK, struct {
		MaterialRevision int64                `json:"materialRevision"`
		Items            []materialReviewItem `json:"items"`
	}{revision, items})
}

// materialReviews 读取已鉴权且指定版本的各项最后一次人工审核。
// 参数 ctx、id、revision 为请求上下文、备案标识和材料版本。
// 返回值 各规则的最近一次审核，或数据库错误。
func (s *Service) materialReviews(ctx context.Context, id string, revision int64) (map[string]*materialReview, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT ON (rule_id)
		rule_id,status,remark,version,reviewer_name,reviewed_at FROM filing_material_reviews
		WHERE filing_id=$1 AND material_revision=$2 ORDER BY rule_id,version DESC`, id, revision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reviews := make(map[string]*materialReview)
	for rows.Next() {
		var review materialReview
		if err := rows.Scan(&review.RuleID, &review.Status, &review.Remark,
			&review.Version, &review.Reviewer, &review.ReviewedAt); err != nil {
			return nil, err
		}
		reviews[review.RuleID] = &review
	}
	return reviews, rows.Err()
}

// putMaterialReview 接收备案当前材料版本中的一项人工审核。
// 参数 w、r 为响应和包含备案、规则与人工意见的请求。
// 返回值 无。
func (s *Service) putMaterialReview(w http.ResponseWriter, r *http.Request) {
	actor, ok := access.Current(r.Context())
	if !ok || (actor.Role != access.Regulator && actor.Role != access.SystemAdmin) {
		httpx.Error(w, http.StatusForbidden, "permission_denied", "当前账号无权审核备案材料。")
		return
	}
	rule := r.PathValue("ruleId")
	var input materialReviewInput
	if httpx.Decode(w, r, &input) != nil || !filingaudit.HasRule(rule) || !input.valid() {
		respondError(w, ErrInvalid)
		return
	}
	input.Remark = strings.TrimSpace(input.Remark)
	result, err := s.saveMaterialReview(r.Context(), r.PathValue("id"), rule, input, actor)
	respond(w, http.StatusOK, result, err)
}

// valid 校验人工结论、备注和用于防止覆盖的两个版本号。
// 参数 无。
// 返回值 所有字段符合规则时为真。
func (input materialReviewInput) valid() bool {
	return (input.Status == "pending" || input.Status == "pass" || input.Status == "fail") &&
		input.MaterialRevision != nil && *input.MaterialRevision >= 0 &&
		input.ExpectedVersion != nil && *input.ExpectedVersion >= 0 &&
		utf8.ValidString(input.Remark) && utf8.RuneCountInString(input.Remark) <= 2000 &&
		!strings.ContainsRune(input.Remark, 0)
}

// saveMaterialReview 锁定辖区内备案并为规则追加一条带审核人的新版本。
// 参数 ctx、id、rule、input、actor 分别为会话、备案、规则、审核内容与操作人。
// 返回值 本次已保存的审核，或辖区、版本冲突及数据库错误。
func (s *Service) saveMaterialReview(ctx context.Context, id, rule string,
	input materialReviewInput, actor access.Principal,
) (materialReview, error) {
	var result materialReview
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		revision, err := materialReviewRevision(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if revision != *input.MaterialRevision {
			return ErrConflict
		}
		var head int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0) FROM filing_material_reviews
			WHERE filing_id=$1 AND material_revision=$2 AND rule_id=$3`, id, revision, rule).
			Scan(&head); err != nil {
			return err
		}
		if head != *input.ExpectedVersion {
			return ErrConflict
		}
		result = materialReview{RuleID: rule, Status: input.Status, Remark: input.Remark,
			Version: head + 1, Reviewer: actor.Name}
		return tx.QueryRow(ctx, `INSERT INTO filing_material_reviews
			(filing_id,material_revision,rule_id,version,status,remark,reviewer_id,reviewer_name)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING reviewed_at`,
			id, revision, rule, result.Version, result.Status, result.Remark, actor.ID, actor.Name).
			Scan(&result.ReviewedAt)
	})
	return result, err
}

type materialReviewQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// materialReviewRevision 使用现有备案辖区条件读取或锁定材料版本。
// 参数 ctx、db、id、lock 分别为会话、查询环境、备案和是否锁定。
// 返回值 当前材料版本，或不存在和数据库错误。
func materialReviewRevision(ctx context.Context, db materialReviewQuery, id string, lock bool) (int64, error) {
	if !validID(id) {
		return 0, ErrNotFound
	}
	query := `SELECT c.material_revision FROM business_cases c
		LEFT JOIN business_cases f ON f.id=c.filing_id
		WHERE c.id=$1 AND c.kind='filing' AND c.discarded_at IS NULL` + caseDistrictScope("$2")
	if lock {
		query += " FOR UPDATE OF c"
	}
	var revision int64
	err := db.QueryRow(ctx, query, id, access.Districts(ctx)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return revision, err
}
