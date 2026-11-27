package cases

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/httpx"
)

// registerMaterialIssues 接通三端查询和机构删除未送审报告的入口。
// 参数 mux 为业务路由。
// 返回值 无。
func (s *Service) registerMaterialIssues(mux *http.ServeMux) {
	for _, scope := range []struct{ audience, path, key string }{
		{"regulator", "/api/v1/material-issues", ""},
		{"institution", "/api/v1/institution/material-issues", "institution"},
		{"operator", "/api/v1/operator/material-issues", "organization"},
	} {
		mux.HandleFunc("GET "+scope.path, s.materialIssuesHTTP(scope.audience, scope.key))
	}
	mux.HandleFunc("POST /api/v1/institution/material-issues/{id}/discard", withInstitution(s.discardIssueHTTP))
	mux.HandleFunc("DELETE /api/v1/institution/reports/{id}", withInstitution(s.discardReportHTTP))
}

// materialIssuesHTTP 查询对应端可见的材料异常，禁止传入额外的范围条件。
// 参数 audience 为业务端，scopeKey 为该端允许的业务范围字段。
// 返回值 当前端的列表处理器。
func (s *Service) materialIssuesHTTP(audience, scopeKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter, err := parseMaterialIssueFilter(r.URL.RawQuery, audience, scopeKey)
		if err != nil {
			respondError(w, err)
			return
		}
		result, err := s.listMaterialIssues(r.Context(), filter)
		respond(w, http.StatusOK, result, err)
	}
}

// parseMaterialIssueFilter 解码办理范围、异常状态、来源和分页参数。
// 参数 raw 为 URL 查询，audience 为办理端，scopeKey 为范围字段。
// 返回值 验证后的查询或参数错误。
func parseMaterialIssueFilter(raw, audience, scopeKey string) (materialIssueFilter, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return materialIssueFilter{}, ErrInvalid
	}
	for key, entries := range values {
		if len(entries) != 1 || (key != scopeKey && key != "status" && key != "caseKind" &&
			key != "id" && key != "before") {
			return materialIssueFilter{}, ErrInvalid
		}
	}
	filter := materialIssueFilter{audience: audience, name: strings.TrimSpace(values.Get(scopeKey)),
		status: values.Get("status"), caseKind: values.Get("caseKind")}
	if scopeKey != "" && !validText(filter.name, 200) {
		return materialIssueFilter{}, ErrInvalid
	}
	if filter.status == "" {
		filter.status = "open"
	}
	if filter.status != "open" && filter.status != "closed" && filter.status != "all" ||
		filter.caseKind != "" && filter.caseKind != filing && filter.caseKind != report {
		return materialIssueFilter{}, ErrInvalid
	}
	if value := values.Get("id"); value != "" {
		if !validID(value) {
			return materialIssueFilter{}, ErrInvalid
		}
		filter.id = &value
	}
	if value := values.Get("before"); value != "" {
		filter.before, err = strconv.ParseInt(value, 10, 64)
		if err != nil || filter.before <= 0 {
			return materialIssueFilter{}, ErrInvalid
		}
	}
	return filter, nil
}

// discardIssueHTTP 从未成功条目删除所关联的本机构未送审报告。
// 参数 w、r 为删除请求和响应，institution 为请求的机构范围。
// 返回值 无，成功返回空响应。
func (s *Service) discardIssueHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	s.discardMaterialReportHTTP(w, r, institution, true)
}

// discardReportHTTP 从报告详情删除本机构未送审报告。
// 参数 w、r 为删除请求和响应，institution 为请求的机构范围。
// 返回值 无，成功返回空响应。
func (s *Service) discardReportHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	s.discardMaterialReportHTTP(w, r, institution, false)
}

// discardMaterialReportHTTP 验证删除命令，选择异常条目或报告作为删除入口。
// 参数 w、r 为请求和响应，institution 为机构，byIssue 表示路径使用材料异常标识。
// 返回值 无，失败沿用统一状态冲突与归属提示。
func (s *Service) discardMaterialReportHTTP(w http.ResponseWriter, r *http.Request, institution string, byIssue bool) {
	var input versionInput
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		respondError(w, ErrInvalid)
		return
	}
	id := r.PathValue("id")
	if !validID(id) {
		respondError(w, ErrNotFound)
		return
	}
	if err := s.discardMaterialReport(r.Context(), institution, id, input.ExpectedVersion, byIssue); err != nil {
		respondError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// discardMaterialReport 隐藏未送审草稿，保留材料及异常留痕，并同步关闭活动异常。
// 参数 ctx 为命令上下文，institution、id、version 为机构、目标及旧版本，byIssue 标识来源。
// 返回值 删除结果或权限、状态、版本及数据库错误。
func (s *Service) discardMaterialReport(ctx context.Context, institution, id string, version int64, byIssue bool) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		caseID, err := reportToDiscard(ctx, tx, institution, id, byIssue)
		if err != nil {
			return err
		}
		current, err := lockRecord(ctx, tx, report, caseID)
		if err != nil {
			return err
		}
		if err := current.checkInstitutionEdit(institution); err != nil {
			return err
		}
		if current.version != version || current.status != draft {
			return ErrConflict
		}
		return markReportDiscarded(ctx, tx, caseID, id, byIssue)
	})
}

// reportToDiscard 按机构范围查出失败条目原关联报告。
// 参数 ctx、tx 控制查询，institution 和 id 指定入口，byIssue 为是否需读取失败记录。
// 返回值 报告标识，或业务不存在、数据库错误。
func reportToDiscard(ctx context.Context, tx pgx.Tx, institution, id string, byIssue bool) (string, error) {
	if !byIssue {
		return id, nil
	}
	var caseID string
	err := tx.QueryRow(ctx, `SELECT case_id::text FROM material_issues
		WHERE id=$1 AND case_kind='report' AND institution=$2`, id, institution).Scan(&caseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return caseID, err
}

// markReportDiscarded 在持有报告行锁时复查失败记录和审核历史并执行逻辑删除。
// 参数 ctx、tx 为状态事务，caseID 为报告，id 为命令入口，byIssue 为是否必须仍有失败条目。
// 返回值 状态与回执写入结果。
func markReportDiscarded(ctx context.Context, tx pgx.Tx, caseID, id string, byIssue bool) error {
	command, err := tx.Exec(ctx, `UPDATE business_cases c
		SET discarded_at=clock_timestamp(),version=version+1
		WHERE id=$1 AND submitted_at IS NULL
		  AND (NOT $2 OR EXISTS(SELECT 1 FROM material_issues i
		      WHERE i.case_id=c.id AND i.id=$3 AND i.closed_at IS NULL))`, caseID, byIssue, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	return closeMaterialIssues(ctx, tx, caseID, nil, nil)
}
