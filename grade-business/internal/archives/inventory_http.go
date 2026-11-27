package archives

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"grade-business/internal/httpx"
)

const assetPath = "/api/v1/archives/systems/{id}/assets"

// listAssets 返回单个备案系统名下的有界资产登记页。
// 参数 w 为响应，r 携带备案标识及分页查询。
// 返回值 无。
func (s archiveService) listAssets(w http.ResponseWriter, r *http.Request) {
	id, err := archivePathID(r, "id")
	if err != nil {
		writeRegistration(w, nil, false, err)
		return
	}
	options, kind, err := assetReadParameters(r.URL.RawQuery)
	if err != nil {
		writeRegistration(w, nil, false, err)
		return
	}
	result, err := s.readSystemAssets(r.Context(), id, options, kind)
	writeRegistration(w, result, false, err)
}

// assetReadParameters 校验资产类别筛选和登记页的游标范围。
// 参数 raw 为原始查询字符串。
// 返回值 分页条件、可选类别及输入错误。
func assetReadParameters(raw string) (listOptions, string, error) {
	values, err := strictQuery(raw, "kind", "cursor", "limit")
	if err != nil {
		return listOptions{}, "", err
	}
	kind := values.Get("kind")
	switch kind {
	case "", "ip", "domain", "topology", "supplier", "security_event", "cloud", "big_data":
	default:
		return listOptions{}, "", errInvalid
	}
	values.Del("kind")
	options, err := registrationPage(values.Encode())
	return options, kind, err
}

// createAsset 登记备案系统的单项资产或证据记录。
// 参数 w 为响应，r 携带系统标识及登记内容。
// 返回值 无。
func (s archiveService) createAsset(w http.ResponseWriter, r *http.Request) {
	filing, err := archivePathID(r, "id")
	if err != nil {
		writeRegistration(w, nil, false, err)
		return
	}
	var input assetCreate
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || validateAssetCreate(&input) != nil {
		writeRegistration(w, nil, false, errInvalid)
		return
	}
	result, created, err := s.createSystemAsset(r.Context(), filing, input)
	writeRegistration(w, result, created, err)
}

// patchAsset 修改登记资料和启停状态。
// 参数 w 为响应，r 携带登记归属、目标标识及 If-Match 版本。
// 返回值 无。
func (s archiveService) patchAsset(w http.ResponseWriter, r *http.Request) {
	filing, err := archivePathID(r, "id")
	if err != nil {
		writeRegistration(w, nil, false, err)
		return
	}
	id, err := archivePathID(r, "assetId")
	if err != nil {
		writeRegistration(w, nil, false, err)
		return
	}
	var input assetUpdate
	version, err := registrationVersion(r.Header.Values("If-Match"))
	if r.URL.RawQuery != "" || err != nil || httpx.Decode(w, r, &input) != nil || input.Active == nil {
		writeRegistration(w, nil, false, errInvalid)
		return
	}
	result, err := s.updateSystemAsset(r.Context(), filing, id, version, input)
	writeRegistration(w, result, false, err)
}

// archivePathID 校验档案路由中的公开标识。
// 参数 r 为当前请求，key 为备案或登记路径字段。
// 返回值 有效 UUID，格式异常时返回档案不存在。
func archivePathID(r *http.Request, key string) (pgtype.UUID, error) {
	id, err := parseArchiveID(r.PathValue(key))
	if err != nil {
		return id, pgx.ErrNoRows
	}
	return id, nil
}

// registrationPage 读取登记列表的有界游标参数。
// 参数 raw 为原始查询字符串。
// 返回值 列表选项或非法参数错误。
func registrationPage(raw string) (listOptions, error) {
	if _, err := strictQuery(raw, "cursor", "limit"); err != nil {
		return listOptions{}, err
	}
	return listParameters(raw, true)
}

// registrationVersion 验证唯一强格式 If-Match 版本。
// 参数 values 为客户端提供的版本请求头。
// 返回值 正整数版本或格式错误。
func registrationVersion(values []string) (int64, error) {
	if len(values) != 1 {
		return 0, errInvalid
	}
	raw := values[0]
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return 0, errInvalid
	}
	version, err := strconv.ParseInt(raw[1:len(raw)-1], 10, 64)
	if err != nil || version < 1 || strconv.FormatInt(version, 10) != raw[1:len(raw)-1] {
		return 0, errInvalid
	}
	return version, nil
}

// writeRegistration 返回档案登记结果或安全的领域错误。
// 参数 w 为响应，result 为业务结果，created 表示确实新建，err 为执行结果。
// 返回值 无。
func writeRegistration(w http.ResponseWriter, result any, created bool, err error) {
	switch {
	case err == nil && created:
		httpx.JSON(w, http.StatusCreated, result)
	case err == nil:
		httpx.JSON(w, http.StatusOK, result)
	case errors.Is(err, errInvalid):
		httpx.Error(w, http.StatusBadRequest, "invalid_input", "请核对登记范围、来源、内容和版本。")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "not_found", "未找到该档案或登记记录。")
	case errors.Is(err, errRegistrationConflict):
		httpx.Error(w, http.StatusConflict, "conflict", "登记标识已占用、记录已变更或当前归属不允许登记，请刷新后核对。")
	default:
		slog.Error("档案登记处理失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "档案登记暂时无法处理，请稍后重试。")
	}
}
