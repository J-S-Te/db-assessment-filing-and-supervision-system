package knowledge

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"grade-business/internal/httpx"
)

const snapshotFields = "id, version, title, category, source, body, tags, updated_at, inactive_at"
const captureSnapshot = "INSERT INTO knowledge_revisions " +
	"(entry_id, version, title, category, source, body, tags, updated_at, inactive_at, action) " +
	"SELECT " + snapshotFields

type statusInput struct {
	Active          *bool `json:"active"`
	ExpectedVersion int64 `json:"expectedVersion"`
}

type revisionSummary struct {
	Version    int64      `json:"version"`
	Action     string     `json:"action"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	Title      string     `json:"title"`
	Category   string     `json:"category"`
	Source     string     `json:"source"`
	Tags       []string   `json:"tags"`
	InactiveAt *time.Time `json:"inactiveAt,omitempty"`
}

type revisionDetail struct {
	revisionSummary
	Body string `json:"body"`
}

type revisionPage struct {
	Items      []revisionSummary `json:"items"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

type insertTarget interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// insertKnowledge 保存条目及其第一个版本，可用于单条维护或批次事务。
// 参数 ctx 控制写入，target 为写入连接或事务，item、tags 和 action 为条目字段及版本来源。
// 返回值 创建出的条目标识和数据库错误。
func insertKnowledge(ctx context.Context, target insertTarget, item createInput, tags []string, action string) (string, error) {
	var id string
	err := target.QueryRow(ctx, `WITH created AS (
		INSERT INTO knowledge_entries (title, category, source, body, tags)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+snapshotFields+`
	), snapshot AS (
		`+captureSnapshot+`, $6 FROM created
	) SELECT id::text FROM created`, item.Title, item.Category, item.Source, item.Body, tags, action).Scan(&id)
	return id, err
}

// revise 以一次数据库语句更新知识当前字段并持久化同版本快照。
// 参数 ctx、id、version 指定条目和已读版本，action 为来源，assignment 和 args 为内部变更及其参数。
// 返回值 用于报告成功、版本冲突或记录缺失的查询行。
func (s *Service) revise(ctx context.Context, id pgtype.UUID, version int64, action, assignment string, args ...any) pgx.Row {
	return s.pool.QueryRow(ctx, `WITH updated AS (
		UPDATE knowledge_entries SET `+assignment+`, version = version + 1, updated_at = clock_timestamp()
		WHERE id = $1 AND version = $2 RETURNING `+snapshotFields+`
	), snapshot AS (
		`+captureSnapshot+`, $3 FROM updated
	)`+entryExists, append([]any{id, version, action}, args...)...)
}

// setStatus 停用或重新启用条目，并将维护动作记入版本历史。
// 参数 w 为响应写入端，r 为状态与当前版本请求。
// 返回值 无，成功时返回新版本。
func (s *Service) setStatus(w http.ResponseWriter, r *http.Request) {
	id, valid := parseID(r.PathValue("id"))
	if !valid {
		httpx.Error(w, http.StatusNotFound, "not_found", "知识条目不存在。")
		return
	}
	var input statusInput
	if httpx.Decode(w, r, &input) != nil || input.Active == nil || input.ExpectedVersion < 1 {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "请提交有效的条目状态和版本。")
		return
	}
	action, assignment := "disable", "inactive_at = COALESCE(inactive_at, clock_timestamp())"
	if *input.Active {
		action, assignment = "restore", "inactive_at = NULL"
	}
	respondUpdate(w, s.revise(r.Context(), id, input.ExpectedVersion, action, assignment))
}

// versions 读取当前条目最近的版本元数据。
// 参数 w 为响应写入端，r 提供条目标识、版本游标与页大小。
// 返回值 无，成功响应携带倒序版本分页。
func (s *Service) versions(w http.ResponseWriter, r *http.Request) {
	id, valid := parseID(r.PathValue("id"))
	if !valid {
		httpx.Error(w, http.StatusNotFound, "not_found", "知识条目不存在。")
		return
	}
	before, limit, err := revisionFilters(r.URL.RawQuery)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	result, err := s.readVersions(r.Context(), id, before, limit)
	respondRevision(w, result, err)
}

// revisionFilters 约束版本分页字段并解析可继续浏览的位置。
// 参数 raw 为由客户端提交的查询字符串。
// 返回值 上一页末尾版本、页大小和可能发生的校验错误。
func revisionFilters(raw string) (int64, int, error) {
	invalid := errors.New("版本分页条件无效。")
	values, err := url.ParseQuery(raw)
	if err != nil {
		return 0, 0, invalid
	}
	for key, entries := range values {
		if len(entries) != 1 || (key != "before" && key != "limit") {
			return 0, 0, invalid
		}
	}
	limit, err := parseLimit(values.Get("limit"))
	if err != nil {
		return 0, 0, err
	}
	if supplied, ok := values["before"]; ok {
		before, valid := revisionNumber(supplied[0])
		if !valid {
			return 0, 0, invalid
		}
		return before, limit, nil
	}
	return 0, limit, nil
}

// revisionNumber 将版本字段解析为规范的正整数。
// 参数 raw 为地址或游标中的原始版本号。
// 返回值 正整数版本以及是否合法。
func revisionNumber(raw string) (int64, bool) {
	number, err := strconv.ParseInt(raw, 10, 64)
	return number, err == nil && number > 0 && strconv.FormatInt(number, 10) == raw
}

// readVersions 在条目内按稳定版本号倒序读取快照摘要。
// 参数 ctx 控制读取，id 为条目标识，before 为上页末尾版本，limit 为页大小。
// 返回值 版本分页与可能发生的数据库错误。
func (s *Service) readVersions(ctx context.Context, id pgtype.UUID, before int64, limit int) (revisionPage, error) {
	rows, err := s.pool.Query(ctx, `SELECT version, action, updated_at, title, category, source, tags, inactive_at
		FROM knowledge_revisions WHERE entry_id = $1 AND ($2::bigint = 0 OR version < $2)
		ORDER BY version DESC LIMIT $3`, id, before, limit+1)
	if err != nil {
		return revisionPage{}, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[revisionSummary])
	if err != nil {
		return revisionPage{}, err
	}
	if len(items) == 0 {
		var exists int
		err = s.pool.QueryRow(ctx, "SELECT 1 FROM knowledge_entries WHERE id = $1", id).Scan(&exists)
	}
	result := revisionPage{Items: items}
	if len(items) > limit {
		result.Items = items[:limit]
		result.NextCursor = strconv.FormatInt(items[limit-1].Version, 10)
	}
	return result, err
}

// versionDetail 读取用户选中的历史版本正文及当时的状态。
// 参数 w 为响应写入端，r 携带条目标识和版本号。
// 返回值 无，选中的历史快照通过响应返回。
func (s *Service) versionDetail(w http.ResponseWriter, r *http.Request) {
	id, validID := parseID(r.PathValue("id"))
	version, validVersion := revisionNumber(r.PathValue("version"))
	if !validID || !validVersion {
		respondRevision(w, nil, pgx.ErrNoRows)
		return
	}
	var result revisionDetail
	err := s.pool.QueryRow(r.Context(), `SELECT version, action, updated_at, title, category, source, tags, inactive_at, body
		FROM knowledge_revisions WHERE entry_id = $1 AND version = $2`, id, version).Scan(
		&result.Version, &result.Action, &result.UpdatedAt, &result.Title, &result.Category,
		&result.Source, &result.Tags, &result.InactiveAt, &result.Body,
	)
	respondRevision(w, result, err)
}

// respondRevision 返回只读历史查询结果并映射存储错误。
// 参数 w 为响应写入端，result 为历史快照或分页，err 为查询错误。
// 返回值 无。
func respondRevision(w http.ResponseWriter, result any, err error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "not_found", "知识条目或版本不存在。")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "知识版本暂时无法读取。")
	default:
		httpx.JSON(w, http.StatusOK, result)
	}
}
