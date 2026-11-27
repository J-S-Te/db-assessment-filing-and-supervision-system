package directory

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
)

const publicDirectory = "/api/v1/directory"
const institutionDirectory = "/api/v1/institution/directory"

type endpoint struct {
	service service
	scoped  bool
}

// Register 挂载监管和机构的共享名录、历史与业务选择接口。
// 参数 mux 为业务路由，pool 为已应用名录迁移的数据库。
// 返回值 无。
func Register(mux *http.ServeMux, pool *pgxpool.Pool) {
	s := service{pool: pool}
	s.registerMerge(mux)
	registerDirectory(mux, publicDirectory, endpoint{service: s})
	registerDirectory(mux, institutionDirectory, endpoint{service: s, scoped: true})
	mux.HandleFunc("GET "+publicDirectory+"/options", s.optionsHTTP)
	mux.HandleFunc("POST "+institutionDirectory+"/units/{id}/link", endpoint{service: s, scoped: true}.linkHTTP)
}

// registerDirectory 注册两个办理角色统一的目录入口。
// 参数 mux 为业务路由，path 为角色端 URL，handler 为包含范围配置的处理器。
// 返回值 无。
func registerDirectory(mux *http.ServeMux, path string, handler endpoint) {
	mux.HandleFunc("GET "+path+"/{collection}", handler.listHTTP)
	mux.HandleFunc("POST "+path+"/{collection}", handler.createHTTP)
	mux.HandleFunc("GET "+path+"/{collection}/{id}", handler.detailHTTP)
	mux.HandleFunc("PATCH "+path+"/{collection}/{id}", handler.patchHTTP)
	mux.HandleFunc("GET "+path+"/{collection}/{id}/history", handler.historyHTTP)
}

// listHTTP 校验筛选条件并返回有界的目录页。
// 参数 w 为响应，r 为带目标名录和范围条件的请求。
// 返回值 无。
func (e endpoint) listHTTP(w http.ResponseWriter, r *http.Request) {
	kind, scope, values, err := e.request(r, "q", "status", "institutionId", "before")
	if err != nil {
		respond(w, http.StatusOK, nil, err)
		return
	}
	input, err := prepareList(kind, scope, values)
	if err != nil {
		respond(w, http.StatusOK, nil, err)
		return
	}
	page, err := e.service.list(r.Context(), input)
	respond(w, http.StatusOK, page, err)
}

// optionsHTTP 查询业务允许新引用的最小选项，不返回证书及归属详情。
// 参数 w 为响应，r 携带条目类型、可选机构和关键词。
// 返回值 无。
func (s service) optionsHTTP(w http.ResponseWriter, r *http.Request) {
	input, err := optionsQuery(r.URL.RawQuery)
	if err != nil {
		respond(w, http.StatusOK, nil, err)
		return
	}
	result, err := s.list(r.Context(), input)
	if err != nil {
		respond(w, http.StatusOK, nil, err)
		return
	}
	out := page[option]{Items: make([]option, 0, len(result.Items)), NextCursor: result.NextCursor}
	for _, item := range result.Items {
		pick := option{
			ID: item.ID, Name: item.Name, ScopeName: item.ScopeName, Usable: item.Usable, Status: item.Status,
		}
		if input.kind == person {
			pick.Code = item.Code
		}
		out.Items = append(out.Items, pick)
	}
	respond(w, http.StatusOK, out, nil)
}

// optionsQuery 核对目录选择器使用的最小搜索参数。
// 参数 raw 为选项查询字符串。
// 返回值 仅选择当前可用项的目录条件。
func optionsQuery(raw string) (listFilter, error) {
	values, err := parseQuery(raw, "kind", "institution", "q", "before")
	if err != nil {
		return listFilter{}, err
	}
	kind := values.Get("kind")
	if kind != unit && kind != institution && kind != person {
		return listFilter{}, errInvalid
	}
	scope, err := readScope(values, kind == person)
	if err != nil {
		return listFilter{}, err
	}
	input, err := prepareList(kind, scope, values)
	input.status, input.options = active, true
	return input, err
}

// createHTTP 按角色权限登记名录条目和历史。
// 参数 w 为响应，r 携带登记字段及必要的机构范围。
// 返回值 无。
func (e endpoint) createHTTP(w http.ResponseWriter, r *http.Request) {
	kind, scope, _, err := e.request(r)
	var input change
	if err == nil {
		err = e.decodeWrite(w, r, kind, &input)
	}
	if err != nil {
		respond(w, http.StatusCreated, nil, err)
		return
	}
	item, err := e.service.create(r.Context(), kind, scope, input)
	respond(w, http.StatusCreated, item, err)
}

// detailHTTP 读取有归属限制的名录详情。
// 参数 w 为响应，r 携带目标位置和当前机构。
// 返回值 无。
func (e endpoint) detailHTTP(w http.ResponseWriter, r *http.Request) {
	kind, scope, _, err := e.request(r)
	if err != nil {
		respond(w, http.StatusOK, nil, err)
		return
	}
	item, err := readEntry(r.Context(), e.service.pool, kind, scope, r.PathValue("id"))
	respond(w, http.StatusOK, item, err)
}

// patchHTTP 为可编辑的条目应用经版本保护的变更。
// 参数 w 为响应，r 携带目标、范围和部分更新字段。
// 返回值 无。
func (e endpoint) patchHTTP(w http.ResponseWriter, r *http.Request) {
	kind, scope, _, err := e.request(r)
	var input change
	if err == nil {
		err = e.decodeWrite(w, r, kind, &input)
	}
	if err != nil {
		respond(w, http.StatusOK, nil, err)
		return
	}
	item, err := e.service.patch(r.Context(), kind, scope, r.PathValue("id"), input)
	respond(w, http.StatusOK, item, err)
}

// historyHTTP 输出受当前范围保护的历史变更及分页游标。
// 参数 w 为响应，r 携带名录类型、标识和可选游标。
// 返回值 无。
func (e endpoint) historyHTTP(w http.ResponseWriter, r *http.Request) {
	kind, scope, values, err := e.request(r, "before")
	if err != nil {
		respond(w, http.StatusOK, nil, err)
		return
	}
	cursor, err := historyBefore(values)
	if err != nil {
		respond(w, http.StatusOK, nil, err)
		return
	}
	page, err := e.service.history(r.Context(), kind, scope, r.PathValue("id"), cursor)
	respond(w, http.StatusOK, page, err)
}

// linkHTTP 为当前机构关联已有被测单位，关联操作不需要额外正文。
// 参数 w 为响应，r 携带当前所属机构和目录单位标识。
// 返回值 无。
func (e endpoint) linkHTTP(w http.ResponseWriter, r *http.Request) {
	values, err := parseQuery(r.URL.RawQuery, "institution")
	if err != nil || r.ContentLength != 0 {
		respond(w, http.StatusOK, nil, errInvalid)
		return
	}
	scope, err := readScope(values, true)
	id := r.PathValue("id")
	if err != nil || !validID(id) {
		respond(w, http.StatusOK, nil, errInvalid)
		return
	}
	result, err := e.service.link(r.Context(), scope, id)
	respond(w, http.StatusOK, result, err)
}

// request 解析路由名录类型、标识以及是否需要机构归属。
// 参数 r 为业务请求，fields 为操作允许提交的附加筛选参数。
// 返回值 目录类型、机构范围、保留查询项或业务错误。
func (e endpoint) request(r *http.Request, fields ...string) (string, string, url.Values, error) {
	kind := kindOfCollection(r.PathValue("collection"))
	if id := r.PathValue("id"); kind == "" || (id != "" && !validID(id)) {
		return "", "", nil, pgx.ErrNoRows
	}
	if e.scoped {
		fields = append(fields, "institution")
	}
	values, err := parseQuery(r.URL.RawQuery, fields...)
	if err != nil {
		return "", "", nil, err
	}
	scope, err := readScope(values, e.scoped)
	return kind, scope, values, err
}

// decodeWrite 防止机构替监管审批资质，并解码名录修改字段。
// 参数 w、r 为 HTTP 请求与响应，kind 为目录类型，input 为解码目标。
// 返回值 请求结构或越权错误。
func (e endpoint) decodeWrite(w http.ResponseWriter, r *http.Request, kind string, input *change) error {
	if e.scoped && kind == institution {
		return errForbidden
	}
	if httpx.Decode(w, r, input) != nil {
		return errInvalid
	}
	return nil
}

// respond 将业务结果映射为精简状态和可公开说明。
// 参数 w 为响应，status 为成功状态码，result 为业务数据，err 为可能的错误。
// 返回值 无。
func respond(w http.ResponseWriter, status int, result any, err error) {
	switch {
	case err == nil:
		httpx.JSON(w, status, result)
	case errors.Is(err, errInvalid):
		httpx.Error(w, http.StatusBadRequest, "validation_error", "请核对名录字段、筛选条件和所选归属。")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "not_found", "未找到当前范围内的目录记录。")
	case errors.Is(err, errForbidden):
		httpx.Error(w, http.StatusForbidden, "directory_read_only", "此记录由监管端或已共享单位统一维护。")
	case errors.Is(err, errVersion):
		httpx.Error(w, http.StatusConflict, "version_conflict", "记录已更新，请刷新后重试。")
	case errors.Is(err, errDuplicate):
		httpx.Error(w, http.StatusConflict, "duplicate_directory_entry", "已有相同名称、代码或证书编号的记录。")
	case errors.Is(err, errUnavailable):
		httpx.Error(w, http.StatusConflict, "directory_unavailable", "关联机构或单位尚未登记、已停用或资质到期。")
	case errors.Is(err, errInFlight):
		httpx.Error(w, http.StatusConflict, "directory_in_review", "仍有在审业务引用，请办结后再停用或调整人员归属。")
	case errors.Is(err, errMergeBlocked):
		httpx.Error(w, http.StatusConflict, "unit_merge_blocked", "单位或关联业务已变化，请重新预检。")
	case errors.Is(err, errMergeBusy):
		httpx.Error(w, http.StatusConflict, "unit_merge_busy", "关联业务正在更新，请稍后重新预检。")
	default:
		slog.Error("办理名录失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "名录暂时无法处理，请稍后重试。")
	}
}
