package knowledge

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/httpx"
	"grade-business/internal/tagcatalog"
)

const maxImportBytes = 2 << 20
const maxImportRows = 100
const importPath = collectionPath + "/import"

var importHeader = []string{"title", "category", "source", "body", "tags"}
var errImportFile = errors.New("请上传一个不超过 2 MiB 的 UTF-8 CSV 文件。")
var errImportIssues = errors.New("批次包含无效条目。")

type importRow struct {
	line int
	item createInput
	tags []string
}

type importIssue struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

type importBatch struct {
	rows   []importRow
	issues []importIssue
	total  int
}

type importPreviewItem struct {
	Row      int      `json:"row"`
	Title    string   `json:"title"`
	Category string   `json:"category"`
	Source   string   `json:"source"`
	Tags     []string `json:"tags"`
}

type importPreviewResult struct {
	Total  int                 `json:"total"`
	Items  []importPreviewItem `json:"items"`
	Issues []importIssue       `json:"issues"`
}

type importResult struct {
	Count int      `json:"count"`
	IDs   []string `json:"ids"`
}

// registerImport 挂载 CSV 导入模板、只读预检与批量保存接口。
// 参数 mux 为监管知识库所在的路由表。
// 返回值 无。
func (s *Service) registerImport(mux *http.ServeMux) {
	mux.HandleFunc("GET "+importPath+"/template", importTemplate)
	mux.HandleFunc("POST "+importPath+"/preview", s.previewImport)
	mux.HandleFunc("POST "+importPath, s.commitImport)
}

// importTemplate 生成可直接下载的 UTF-8 CSV 示例模板。
// 参数 w 为文件下载响应，r 为模板请求。
// 返回值 无，生成的 CSV 通过 HTTP 下载。
func importTemplate(w http.ResponseWriter, _ *http.Request) {
	var data bytes.Buffer
	data.WriteString("\ufeff")
	writer := csv.NewWriter(&data)
	writer.UseCRLF = true
	_ = writer.WriteAll([][]string{
		importHeader,
		{"示例知识标题", "standard", "发布单位或出处", "在此填写正文", `["示例标签"]`},
	})
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="knowledge-import.csv"`)
	_, _ = w.Write(data.Bytes())
}

// readImport 从提交中读取唯一的 CSV 文件并校验格式和条目。
// 参数 w 为响应写入端，r 为批量导入的 multipart 请求。
// 返回值 已解析的候选批次或文件无效错误。
func readImport(w http.ResponseWriter, r *http.Request) (importBatch, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		return importBatch{}, errImportFile
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || !strings.EqualFold(path.Ext(part.FileName()), ".csv") {
		return importBatch{}, errImportFile
	}
	data, err := io.ReadAll(io.LimitReader(part, maxImportBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxImportBytes || !utf8.Valid(data) {
		return importBatch{}, errImportFile
	}
	if _, err := reader.NextPart(); !errors.Is(err, io.EOF) {
		return importBatch{}, errImportFile
	}
	return parseImport(data), nil
}

// parseImport 读取 CSV 的字段与物理行号，收集整批可恢复的逐行校验问题。
// 参数 data 为已完成大小和 UTF-8 校验的 CSV 原文。
// 返回值 含可预览行和校验问题的候选批次。
func parseImport(data []byte) importBatch {
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\ufeff"))))
	header, err := reader.Read()
	batch := importBatch{issues: []importIssue{}}
	if err != nil || !slices.Equal(header, importHeader) {
		batch.issues = append(batch.issues, importIssue{1, "列名须为 title,category,source,body,tags。"})
		return batch
	}
	seen := make(map[[3]string]int)
	for {
		cells, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		batch.total++
		line := csvRecordLine(reader, cells, err)
		if batch.total > maxImportRows {
			batch.issues = append(batch.issues, importIssue{line, "每批最多导入 100 条知识。"})
			break
		}
		if err != nil {
			batch.issues = append(batch.issues, importIssue{line, "CSV 列数或引号无效。"})
			if !errors.Is(err, csv.ErrFieldCount) {
				break
			}
			continue
		}
		batch.acceptRow(line, cells, seen)
	}
	if batch.total == 0 {
		batch.issues = append(batch.issues, importIssue{1, "请至少填写一条知识。"})
	}
	return batch
}

// csvRecordLine 获取当前记录开始的物理行号，包括解析不完整的记录。
// 参数 reader 为已读取当前行的 CSV 读取器，cells 为字段，err 为读取异常。
// 返回值 从 1 开始的文件行号。
func csvRecordLine(reader *csv.Reader, cells []string, err error) int {
	var malformed *csv.ParseError
	if errors.As(err, &malformed) {
		return malformed.StartLine
	}
	if len(cells) != 0 {
		line, _ := reader.FieldPos(0)
		return line
	}
	return 1
}

// acceptRow 用单条知识规则检查一行及其在文件内的唯一性。
// 参数 line 为物理行号，cells 为已核对列数的字段，seen 保存本批先前的有效键。
// 返回值 无，向批次登记合法行或校验问题。
func (batch *importBatch) acceptRow(line int, cells []string, seen map[[3]string]int) {
	item := createInput{Title: cells[0], Category: strings.TrimSpace(cells[1]), Source: cells[2], Body: cells[3]}
	clean, err := validateCreate(item)
	if err != nil {
		batch.issues = append(batch.issues, importIssue{line, err.Error()})
		return
	}
	tags, err := parseImportTags(cells[4])
	if err != nil {
		batch.issues = append(batch.issues, importIssue{line, err.Error()})
		return
	}
	key := [3]string{clean.Category, clean.Title, clean.Source}
	if previous, duplicate := seen[key]; duplicate {
		message := fmt.Sprintf("与第 %d 行的分类、标题和来源重复。", previous)
		batch.issues = append(batch.issues, importIssue{line, message})
		return
	}
	seen[key] = line
	batch.rows = append(batch.rows, importRow{line: line, item: clean, tags: tags})
}

// parseImportTags 从空值或 JSON 数组读取标签，沿用条目维护的标签规则。
// 参数 raw 为当前 CSV 行的标签单元格。
// 返回值 规范化标签列表或可展示的输入错误。
func parseImportTags(raw string) ([]string, error) {
	tags := []string{}
	if strings.TrimSpace(raw) != "" && json.Unmarshal([]byte(raw), &tags) != nil {
		return nil, errors.New("标签须填写为 JSON 字符串数组，或留空。")
	}
	return normalizeTags(tags)
}

// preview 返回有效行的概要与逐行错误，可安全在前端展示。
// 参数 无，使用当前已校验批次。
// 返回值 不含正文的候选概要和按行排序的校验问题。
func (batch importBatch) preview() importPreviewResult {
	items := make([]importPreviewItem, 0, len(batch.rows))
	for _, row := range batch.rows {
		items = append(items, importPreviewItem{
			Row: row.line, Title: row.item.Title, Category: row.item.Category, Source: row.item.Source, Tags: row.tags,
		})
	}
	sort.SliceStable(batch.issues, func(i, j int) bool { return batch.issues[i].Row < batch.issues[j].Row })
	return importPreviewResult{Total: batch.total, Items: items, Issues: batch.issues}
}

// previewImport 校验上传数据和库内重复项，不持久化任何知识。
// 参数 w 为响应写入端，r 为待预检的 CSV 上传请求。
// 返回值 无，预检响应包含合法行及校验问题。
func (s *Service) previewImport(w http.ResponseWriter, r *http.Request) {
	batch, err := readImport(w, r)
	if err != nil {
		httpx.Error(w, 400, "validation_error", err.Error())
		return
	}
	if err := batch.checkStorage(r.Context(), s.pool); err != nil {
		httpx.Error(w, 500, "internal_error", "批量导入暂时无法预检。")
		return
	}
	httpx.JSON(w, 200, batch.preview())
}

// commitImport 重新校验上传并将完整批次原子写入知识库。
// 参数 w 为响应写入端，r 为正式批量导入请求。
// 返回值 无，成功响应列出导入数量及新建条目标识。
func (s *Service) commitImport(w http.ResponseWriter, r *http.Request) {
	batch, err := readImport(w, r)
	if err != nil {
		httpx.Error(w, 400, "validation_error", err.Error())
		return
	}
	if len(batch.issues) != 0 {
		respondImportIssues(w, batch)
		return
	}
	result, err := s.saveImport(r.Context(), &batch)
	switch {
	case errors.Is(err, errImportIssues):
		respondImportIssues(w, batch)
	case err != nil:
		httpx.Error(w, 500, "internal_error", "批量导入未能保存，请稍后重试。")
	default:
		httpx.JSON(w, 201, result)
	}
}

// respondImportIssues 提示应纠正的文件行，并引导查看全批预检结果。
// 参数 w 为响应写入端，batch 为包含校验问题的批次。
// 返回值 无。
func respondImportIssues(w http.ResponseWriter, batch importBatch) {
	first := batch.preview().Issues[0]
	message := fmt.Sprintf("第 %d 行有误。%s 请预检查看全部问题。", first.Row, first.Message)
	httpx.Error(w, 400, "validation_error", message)
}

type importQuery interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// checkExisting 从数据库找出已存在的导入键，按文件行添加反馈。
// 参数 ctx 控制查询，target 为读取连接或当前导入事务。
// 返回值 读取重复条目的数据库错误。
func (batch *importBatch) checkExisting(ctx context.Context, target importQuery) error {
	if len(batch.rows) == 0 {
		return nil
	}
	titles := make([]string, 0, len(batch.rows))
	categories := make([]string, 0, len(batch.rows))
	sources := make([]string, 0, len(batch.rows))
	for _, row := range batch.rows {
		titles = append(titles, row.item.Title)
		categories = append(categories, row.item.Category)
		sources = append(sources, row.item.Source)
	}
	rows, err := target.Query(ctx, `SELECT input.ordinality FROM
		unnest($1::text[], $2::text[], $3::text[]) WITH ORDINALITY AS input(title, category, source, ordinality)
		WHERE EXISTS (SELECT 1 FROM knowledge_entries e
		    WHERE e.category = input.category
		      AND md5(e.title) = md5(input.title) AND md5(e.source) = md5(input.source)
		      AND e.title = input.title AND e.source = input.source)
		ORDER BY input.ordinality`, titles, categories, sources)
	if err != nil {
		return err
	}
	indexes, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	batch.excludeRows(indexes, "库中已存在相同分类、标题和来源。")
	return nil
}

// excludeRows 将库内校验不通过的条目移出候选，并保留其他条目的文件顺序。
// 参数 indexes 为从 1 开始的有序候选下标，message 为可展示的业务原因。
// 返回值 无。
func (batch *importBatch) excludeRows(indexes []int64, message string) {
	accepted := batch.rows[:0]
	for index, row := range batch.rows {
		if len(indexes) > 0 && int64(index+1) == indexes[0] {
			batch.issues = append(batch.issues, importIssue{row.line, message})
			indexes = indexes[1:]
			continue
		}
		accepted = append(accepted, row)
	}
	batch.rows = accepted
}

// saveImport 在一个事务中校验导入键及标签，并保存整批条目和各自的版本。
// 参数 ctx 控制写入，batch 为已通过本地校验的上传内容。
// 返回值 导入摘要或阻止整批持久化的错误。
func (s *Service) saveImport(ctx context.Context, batch *importBatch) (importResult, error) {
	result := importResult{IDs: make([]string, 0, len(batch.rows))}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// 锁住会改变导入键的并发写入，提交时重新核对预检之后的变化。
		if _, err := tx.Exec(ctx, "LOCK TABLE knowledge_entries IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return err
		}
		if err := batch.checkStorage(ctx, tx); err != nil {
			return err
		}
		if len(batch.issues) != 0 {
			return errImportIssues
		}
		for _, row := range batch.rows {
			id, err := insertKnowledge(ctx, tx, row.item, row.tags, "import")
			if tagcatalog.IsDisabledAssignment(err) {
				batch.issues = append(batch.issues, importIssue{row.line, disabledImportMessage})
				return errImportIssues
			}
			if err != nil {
				return err
			}
			result.IDs = append(result.IDs, id)
		}
		return nil
	})
	result.Count = len(result.IDs)
	return result, err
}
