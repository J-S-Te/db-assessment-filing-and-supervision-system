package dashboard

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
)

const classificationPageSize = 25
const classificationPageLimit = 10000

type classificationOption struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

type classificationValue struct {
	Code    string `json:"code"`
	Version int64  `json:"version"`
}

type classificationRow struct {
	ID      string `json:"id"`
	Number  string `json:"number"`
	Name    string `json:"name"`
	Context string `json:"context"`
	classificationValue
}

type classificationPage struct {
	Items   []classificationRow    `json:"items"`
	Options []classificationOption `json:"options"`
	HasMore bool                   `json:"hasMore"`
}

type classificationInput struct {
	Code            *string `json:"code"`
	ExpectedVersion *int64  `json:"expectedVersion"`
}

var (
	errClassificationMissing  = errors.New("classification target missing")
	errClassificationCode     = errors.New("classification code invalid")
	errClassificationConflict = errors.New("classification version changed")
)

// handleClassificationList 查询指定业务种类的记录、当前归类和可选项目。
// 参数 pool 为业务数据库。
// 返回值 分页分类数据请求处理器。
func handleClassificationList(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.PathValue("kind")
		queries, ok := classificationQueries[kind]
		if !ok {
			classificationError(w, errClassificationMissing)
			return
		}
		query, page, valid := parseClassificationQuery(r.URL.RawQuery)
		if !valid {
			httpx.Error(w, http.StatusBadRequest, "invalid_input", "请检查分类检索条件和页码。")
			return
		}
		result, err := readClassificationPage(r.Context(), pool, queries, kind, query, page)
		if err != nil {
			classificationError(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, result)
	}
}

// handleClassificationPatch 保存或清除单条业务记录的分类。
// 参数 pool 为业务数据库。
// 返回值 使用版本校验的分类更新请求处理器。
func handleClassificationPatch(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.PathValue("kind")
		queries, ok := classificationQueries[kind]
		if !ok {
			classificationError(w, errClassificationMissing)
			return
		}
		var id pgtype.UUID
		idText := r.PathValue("id")
		var input classificationInput
		if r.URL.RawQuery != "" || len(idText) != 36 || id.Scan(idText) != nil ||
			httpx.Decode(w, r, &input) != nil || input.Code == nil || input.ExpectedVersion == nil ||
			*input.ExpectedVersion < 0 || !validClassificationText(*input.Code, 48) {
			httpx.Error(w, http.StatusBadRequest, "invalid_input", "请提供有效的分类编码和记录版本。")
			return
		}
		saved, err := saveClassification(r.Context(), pool, queries, kind, id, input)
		if err != nil {
			classificationError(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, saved)
	}
}

// parseClassificationQuery 验证维护列表的可选检索词及页码。
// 参数 raw 为请求 URL 中的原始查询字符串。
// 返回值 已规范化的检索词、页码和参数是否合法。
func parseClassificationQuery(raw string) (string, int, bool) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", 0, false
	}
	for key, items := range values {
		if (key != "q" && key != "page") || len(items) != 1 {
			return "", 0, false
		}
	}
	query, page := strings.TrimSpace(values.Get("q")), 1
	if values.Has("page") {
		page, err = strconv.Atoi(values.Get("page"))
	}
	return query, page, err == nil && page >= 1 && page <= classificationPageLimit &&
		validClassificationText(query, 120)
}

// validClassificationText 拦截非法编码、控制字符和过长分类字段。
// 参数 text 为输入字段，max 为允许的最大字符数。
// 返回值 文本是否在允许范围。
func validClassificationText(text string, max int) bool {
	return utf8.ValidString(text) && utf8.RuneCountInString(text) <= max &&
		!strings.ContainsFunc(text, unicode.IsControl)
}

// classificationError 将分类维护错误转换为可安全显示的业务反馈。
// 参数 w 为响应对象，err 为分类维护错误。
// 返回值 无。
func classificationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errClassificationMissing):
		httpx.Error(w, http.StatusNotFound, "classification_target_not_found", "当前记录不在分类维护范围内。")
	case errors.Is(err, errClassificationCode):
		httpx.Error(w, http.StatusBadRequest, "invalid_classification_code", "请选择有效的分类。")
	case errors.Is(err, errClassificationConflict):
		httpx.Error(w, http.StatusConflict, "classification_version_conflict", "分类已更新，请重新查询后再保存。")
	default:
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "暂时无法处理驾驶舱归类。")
	}
}
