package directory

import (
	"net/http"
	"strings"

	"grade-business/internal/httpx"
)

// registerMerge 注册仅监管端可使用的合并预检、确认及留痕。
// 参数 mux 为服务路由。
// 返回值 无。
func (s service) registerMerge(mux *http.ServeMux) {
	path := publicDirectory + "/unit-merges"
	mux.HandleFunc("POST "+path+"/preview", s.mergeHTTP(false))
	mux.HandleFunc("POST "+path, s.mergeHTTP(true))
	mux.HandleFunc("GET "+path+"/{id}", s.mergeHistoryHTTP)
}

// mergeHTTP 校验合并输入后执行预检或确认。
// 参数 confirm 表示是否提交正式迁移。
// 返回值 监管合并请求处理器。
func (s service) mergeHTTP(confirm bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input mergeInput
		if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil {
			respond(w, http.StatusOK, nil, errInvalid)
			return
		}
		input, err := normalizeMerge(input, confirm)
		if err != nil {
			respond(w, http.StatusOK, nil, err)
			return
		}
		if confirm {
			result, err := s.confirmMerge(r.Context(), input)
			respond(w, http.StatusOK, result, err)
			return
		}
		plan, err := s.previewMerge(r.Context(), input)
		respond(w, http.StatusOK, plan, err)
	}
}

// mergeHistoryHTTP 查询确认时保存的完整归属迁移和原始预览。
// 参数 w、r 为响应和指定合并标识的请求。
// 返回值 无。
func (s service) mergeHistoryHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.URL.RawQuery != "" || !validID(id) {
		respond(w, http.StatusOK, nil, errInvalid)
		return
	}
	item, err := findMergedUnitRecords(r.Context(), s.pool, "id::text", strings.ToLower(id))
	respond(w, http.StatusOK, item, err)
}
