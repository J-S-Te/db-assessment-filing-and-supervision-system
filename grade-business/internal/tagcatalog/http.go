package tagcatalog

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
)

const path = "/api/v1/tag-catalog"

type service struct{ pool *pgxpool.Pool }

// Register 挂载知识标签的共享目录接口。
// 参数 mux 为业务路由器，pool 为已迁移的 PostgreSQL 连接池。
// 返回值 无。
func Register(mux *http.ServeMux, pool *pgxpool.Pool) {
	s := service{pool: pool}
	mux.HandleFunc("GET "+path, s.list)
	mux.HandleFunc("POST "+path, s.create)
	mux.HandleFunc("PATCH "+path+"/{id}", s.patch)
}

// list 返回指定筛选范围的标签及真实知识使用数。
// 参数 w 为响应，r 携带名称、启用状态及分页条件。
// 返回值 无。
func (s service) list(w http.ResponseWriter, r *http.Request) {
	input, err := parseList(r.URL.RawQuery)
	if err != nil {
		respond(w, http.StatusOK, nil, err)
		return
	}
	result, err := s.readPage(r.Context(), input)
	respond(w, http.StatusOK, result, err)
}

// create 登记可用于新关联的标签名称及颜色。
// 参数 w 为响应，r 携带待登记标签。
// 返回值 无，成功响应为已登记目录行。
func (s service) create(w http.ResponseWriter, r *http.Request) {
	var input createInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil {
		respond(w, http.StatusCreated, nil, errInvalid)
		return
	}
	name, valid := NormalizeName(input.Name)
	if !valid || !colorPattern.MatchString(input.Color) {
		respond(w, http.StatusCreated, nil, errInvalid)
		return
	}
	var result catalogEntry
	err := s.pool.QueryRow(r.Context(), `INSERT INTO knowledge_tag_catalog (name,color)
	  VALUES($1,$2) RETURNING id::text,name,color,active,version`, name, strings.ToLower(input.Color)).Scan(
		&result.ID, &result.Name, &result.Color, &result.Active, &result.Version)
	respond(w, http.StatusCreated, result, err)
}

// patch 按目录版本更新颜色或启停状态，引用继续绑定原名称。
// 参数 w 为响应，r 携带标签标识、版本和新的目录属性。
// 返回值 无，成功响应为更新的目录行。
func (s service) patch(w http.ResponseWriter, r *http.Request) {
	var id pgtype.UUID
	raw := r.PathValue("id")
	if len(raw) != 36 || raw[8] != '-' || raw[13] != '-' || raw[18] != '-' || raw[23] != '-' || id.Scan(raw) != nil {
		respond(w, http.StatusOK, nil, pgx.ErrNoRows)
		return
	}
	var input patchInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || input.Active == nil ||
		input.ExpectedVersion < 1 || !colorPattern.MatchString(input.Color) {
		respond(w, http.StatusOK, nil, errInvalid)
		return
	}
	input.Color = strings.ToLower(input.Color)
	result, err := s.edit(r.Context(), id, input)
	respond(w, http.StatusOK, result, err)
}

// respond 将目录处理结果映射为最小业务结构和可公开错误。
// 参数 w 为响应，status 为成功状态，result 为业务数据，err 为写入或校验结果。
// 返回值 无。
func respond(w http.ResponseWriter, status int, result any, err error) {
	switch {
	case err == nil:
		httpx.JSON(w, status, result)
	case errors.Is(err, errInvalid):
		httpx.Error(w, http.StatusBadRequest, "validation_error", "请核对标签名称、颜色、状态或查询条件。")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "not_found", "未找到该标签。")
	case errors.Is(err, errVersion):
		httpx.Error(w, http.StatusConflict, "version_conflict", "标签已更新，请刷新后重试。")
	case constraint(err, "23505", "knowledge_tag_catalog_name_unique"):
		httpx.Error(w, http.StatusConflict, "duplicate_tag", "目录中已有同名标签，请在原记录上维护。")
	default:
		slog.Error("标签目录处理失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "标签目录暂时无法处理，请稍后重试。")
	}
}
