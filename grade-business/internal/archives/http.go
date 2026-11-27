package archives

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
)

// Register 注册从现有备案及其关联记录读取的档案接口。
// 参数 mux 为监管业务路由，pool 为业务数据库。
// 返回值 无。
func Register(mux *http.ServeMux, pool *pgxpool.Pool) {
	service := archiveService{pool: pool}
	mux.HandleFunc("GET /api/v1/archives/units", service.units)
	mux.HandleFunc("GET /api/v1/archives/units/{organization}", service.unit)
	mux.HandleFunc("GET /api/v1/archives/systems", service.systems)
	mux.HandleFunc("GET /api/v1/archives/systems/{id}", service.system)
	mux.HandleFunc("GET "+supplierPath, service.listSuppliers)
	mux.HandleFunc("POST "+supplierPath, service.createSupplier)
	mux.HandleFunc("GET "+supplierPath+"/{id}", service.getSupplier)
	mux.HandleFunc("PATCH "+supplierPath+"/{id}", service.patchSupplier)
	mux.HandleFunc("GET "+assetPath, service.listAssets)
	mux.HandleFunc("POST "+assetPath, service.createAsset)
	mux.HandleFunc("PATCH "+assetPath+"/{assetId}", service.patchAsset)
	mux.HandleFunc("GET "+coveragePath, service.listCoverage)
	mux.HandleFunc("POST "+coveragePath, service.createCoverage)
	mux.HandleFunc("PATCH "+coveragePath+"/{id}", service.patchCoverage)
}

type archiveService struct{ pool *pgxpool.Pool }

// units 返回批准和注销备案中的精确单位分组。
// 参数 w 为响应，r 携带列表查询条件。
// 返回值 无。
func (s archiveService) units(w http.ResponseWriter, r *http.Request) {
	input, err := listParameters(r.URL.RawQuery, false)
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	result, err := readUnits(r.Context(), s.pool, "", input)
	writeResult(w, result, err)
}

// systems 返回进入档案范围的备案及其汇总信息。
// 参数 w 为响应，r 携带关键词、备案级别、状态及分页条件。
// 返回值 无。
func (s archiveService) systems(w http.ResponseWriter, r *http.Request) {
	input, err := listParameters(r.URL.RawQuery, true)
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	result, err := readSystems(r.Context(), s.pool, archiveScope{}, input)
	writeResult(w, result, err)
}

// unit 读取备案同名单位分组和关联业务历史。
// 参数 w 为响应，r 携带单位名称键及分区查询位置。
// 返回值 无。
func (s archiveService) unit(w http.ResponseWriter, r *http.Request) {
	name, err := decodeGroupKey(r.PathValue("organization"))
	if err != nil {
		writeResult(w, nil, pgx.ErrNoRows)
		return
	}
	input, err := detailParameters(r.URL.RawQuery, true)
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	result, err := s.readUnit(r.Context(), name, input)
	writeResult(w, result, err)
}

// system 读取单一备案系统的档案和关联业务历史。
// 参数 w 为响应，r 携带备案标识及分区查询位置。
// 返回值 无。
func (s archiveService) system(w http.ResponseWriter, r *http.Request) {
	id, err := parseArchiveID(r.PathValue("id"))
	if err != nil {
		writeResult(w, nil, pgx.ErrNoRows)
		return
	}
	input, err := detailParameters(r.URL.RawQuery, false)
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	result, err := s.readSystem(r.Context(), archiveScope{filingID: id}, input)
	writeResult(w, result, err)
}

// writeResult 输出最小档案结构或稳定领域错误。
// 参数 w 为 HTTP 响应，result 为可读数据，err 为处理结果。
// 返回值 无。
func writeResult(w http.ResponseWriter, result any, err error) {
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, result)
	case errors.Is(err, errInvalid):
		httpx.Error(w, http.StatusBadRequest, "invalid_input", "请核对档案查询和分页参数。")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "not_found", "未找到该档案。")
	default:
		slog.Error("全息档案读取失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "档案暂时无法读取，请稍后重试。")
	}
}
