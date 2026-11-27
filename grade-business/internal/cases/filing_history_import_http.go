package cases

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"
)

const historyImportBase = "/api/v1/filing-history-import"

// registerFilingHistoryImport 挂载监管历史备案导入，不向机构和运营者增加写入入口。
// 参数 mux 为后台业务 HTTP 服务。
// 返回值 无。
func (s *Service) registerFilingHistoryImport(mux *http.ServeMux) {
	mux.HandleFunc("GET "+historyImportBase+"/template", historyImportTemplateHTTP)
	mux.HandleFunc("POST "+historyImportBase+"/preview", s.historyImportHTTP(false))
	mux.HandleFunc("POST "+historyImportBase+"/commit", s.historyImportHTTP(true))
	mux.HandleFunc("GET "+historyImportBase+"/batches/{id}", s.historyImportBatchHTTP)
}

// historyImportTemplateHTTP 提供历史备案 ZIP 中所需的 CSV 清单表头。
// 参数 w、r 为附件响应和监管模板请求。
// 返回值 无。
func historyImportTemplateHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respondError(w, ErrInvalid)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": "manifest.csv"}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, "\ufeff"+strings.Join(historyImportColumns, ",")+"\r\n")
}

// historyImportHTTP 创建经过同样上传校验的预检或确认导入处理器。
// 参数 commit 表示是否保存已通过校验的档案。
// 返回值 HTTP 处理函数，成功时输出逐行结果。
func (s *Service) historyImportHTTP(commit bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		file, err := historyImportUploadPart(w, r)
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		if err != nil {
			respondError(w, err)
			return
		}
		source, err := file.Open()
		if err != nil {
			respondError(w, err)
			return
		}
		defer source.Close()
		items, err := parseHistoryImport(source, file.Size)
		if err != nil {
			respondError(w, err)
			return
		}
		result, err := s.runHistoryImport(r, source, items, commit)
		respond(w, http.StatusOK, result, err)
	}
}

// historyImportUploadPart 检查唯一的文件表单及 ZIP 大小和后缀。
// 参数 w、r 为接收限长响应及原始上传请求。
// 返回值 受控 ZIP 文件头，或请求错误。
func historyImportUploadPart(w http.ResponseWriter, r *http.Request) (*multipart.FileHeader, error) {
	if r.URL.RawQuery != "" {
		return nil, ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPackageSize+(64<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return nil, ErrFileSize
		}
		return nil, ErrInvalid
	}
	files := r.MultipartForm.File["file"]
	if len(r.MultipartForm.Value) != 0 || len(r.MultipartForm.File) != 1 || len(files) != 1 {
		return nil, ErrInvalid
	}
	file := files[0]
	if file.Size <= 0 || file.Size > maxPackageSize {
		return nil, ErrFileSize
	}
	if !strings.EqualFold(path.Ext(file.Filename), ".zip") {
		return nil, ErrFilingPackage
	}
	return file, nil
}

// runHistoryImport 根据请求执行只读预检或保留幂等留痕的确认导入。
// 参数 r 为当前调用，source 为尚未顺序读取的 ZIP，items 为已验证记录，commit 为操作类型。
// 返回值 实际批次结果或读写失败。
func (s *Service) runHistoryImport(r *http.Request, source io.Reader, items []historyImportCandidate,
	commit bool,
) (historyImportResult, error) {
	if !commit {
		return s.previewHistoryImport(r.Context(), items)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, source); err != nil {
		return historyImportResult{}, err
	}
	return s.commitHistoryImport(r.Context(), hex.EncodeToString(digest.Sum(nil)), items)
}

// historyImportBatchHTTP 查询管理端指定批次已保存的真实结果及来源。
// 参数 w、r 为响应和包含批次标识的请求。
// 返回值 无，批次不存在时返回统一业务错误。
func (s *Service) historyImportBatchHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.URL.RawQuery != "" || !validID(id) {
		respondError(w, ErrInvalid)
		return
	}
	var result historyImportResult
	err := s.db.QueryRow(r.Context(), "SELECT result FROM filing_history_import_batches WHERE id=$1", id).Scan(&result)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	respond(w, http.StatusOK, result, err)
}
