package knowledge

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/httpx"
)

// Service 提供监管工作台的知识条目录入、维护和检索。
type Service struct {
	pool *pgxpool.Pool
}

const collectionPath = "/api/v1/knowledge"

// New 创建知识库接口。
// 参数 pool 为业务 PostgreSQL 连接池。
// 返回值 使用该连接池的知识库服务。
func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Register 将知识库接口注册到业务路由。
// 参数 mux 为应用私有路由表。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+collectionPath, s.list)
	mux.HandleFunc("POST "+collectionPath, s.create)
	mux.HandleFunc("GET "+collectionPath+"/{id}", s.detail)
	mux.HandleFunc("PATCH "+collectionPath+"/{id}", s.update)
	mux.HandleFunc("PATCH "+collectionPath+"/{id}/tags", s.updateTags)
	mux.HandleFunc("PATCH "+collectionPath+"/{id}/status", s.setStatus)
	mux.HandleFunc("GET "+collectionPath+"/{id}/versions", s.versions)
	mux.HandleFunc("GET "+collectionPath+"/{id}/versions/{version}", s.versionDetail)
	s.registerImport(mux)
}

// list 按分类与字面关键词读取知识库摘要。
// 参数 w 为响应写入端，r 为带筛选条件的请求。
// 返回值 无，列表通过 HTTP 响应返回。
func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "列表筛选参数无效。")
		return
	}
	query, err := parseListQuery(values)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	results, err := s.readPage(r.Context(), query)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "知识库暂时无法读取。")
		return
	}
	httpx.JSON(w, http.StatusOK, results)
}

// create 验证并持久化知识库正文。
// 参数 w 为响应写入端，r 为提交条目的请求。
// 返回值 无，新条目标识通过 HTTP 响应返回。
func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var input createInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "请提交有效的知识条目信息。")
		return
	}
	input, err := validateCreate(input)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	id, err := insertKnowledge(r.Context(), s.pool, input, []string{}, "create")
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "知识条目暂时无法保存。")
		return
	}
	httpx.JSON(w, http.StatusCreated, createResult{ID: id})
}

// detail 读取指定条目的正文与来源。
// 参数 w 为响应写入端，r 为带有条目标识的请求。
// 返回值 无，条目详情通过 HTTP 响应返回。
func (s *Service) detail(w http.ResponseWriter, r *http.Request) {
	id, valid := parseID(r.PathValue("id"))
	if !valid {
		httpx.Error(w, http.StatusNotFound, "not_found", "知识条目不存在。")
		return
	}
	var item entry
	err := s.pool.QueryRow(r.Context(), `SELECT id::text, title, category, source, updated_at, tags, inactive_at, body, version
		FROM knowledge_entries WHERE id = $1`, id).Scan(
		&item.ID, &item.Title, &item.Category, &item.Source, &item.UpdatedAt, &item.Tags, &item.InactiveAt, &item.Body, &item.Version,
	)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "not_found", "知识条目不存在。")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "知识条目暂时无法读取。")
	default:
		httpx.JSON(w, http.StatusOK, item)
	}
}

// readPage 读取满足所有关键词的摘要并根据结果生成下一页游标。
// 参数 ctx 控制查询生命周期，query 为已校验的筛选和排序位置。
// 返回值 数据库分页结果和可能发生的查询错误。
func (s *Service) readPage(ctx context.Context, query listQuery) (page, error) {
	anchorTime := pgtype.Timestamptz{Time: query.anchor.at, Valid: query.anchor.id.Valid}
	rows, err := s.pool.Query(ctx, `SELECT id::text, title, category, source, updated_at, tags, inactive_at
		FROM knowledge_entries
		WHERE ($1::text = '' OR category = $1)
		  AND ($6::text = '' OR strpos(lower(source), lower($6)) > 0)
		  AND ($7 = 'all' OR ((inactive_at IS NULL) = ($7 = 'active')))
		  AND NOT EXISTS (
		    SELECT 1 FROM unnest($2::text[]) AS search(term)
		    WHERE strpos(lower(concat_ws(' ', title, source, body, array_to_string(tags, ' '))), lower(search.term)) = 0
		  )
		  AND ($3::timestamptz IS NULL OR (updated_at, id) < ($3, $4::uuid))
		ORDER BY updated_at DESC, id DESC LIMIT $5`,
		query.category, query.terms, anchorTime, query.anchor.id, query.limit+1, query.source, query.status,
	)
	if err != nil {
		return page{}, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[summary])
	if err != nil {
		return page{}, err
	}
	result := page{Items: items}
	if len(items) > query.limit {
		result.Items = items[:query.limit]
		last := result.Items[len(result.Items)-1]
		result.NextCursor = encodeCursor(last.UpdatedAt, last.ID)
	}
	return result, nil
}
