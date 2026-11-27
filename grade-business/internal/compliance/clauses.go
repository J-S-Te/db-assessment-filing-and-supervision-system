package compliance

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/httpx"
)

const clausePageLimit = 30

const clauseNumberPattern = `(?:[0-9A-Za-z]+(?:[.\-][0-9A-Za-z]+)*|[零〇一二三四五六七八九十百千万两]+)`

const clauseEntryQuery = `SELECT version FROM knowledge_entries
	WHERE id=$1 AND category IN ('regulation','standard') AND inactive_at IS NULL`

var (
	clauseNumber       = regexp.MustCompile(`^` + clauseNumberPattern + `$`)
	clauseCitation     = regexp.MustCompile(`^[ \t　]*第[ \t　]*(` + clauseNumberPattern + `)[ \t　]*[条款项]`)
	errRequestConflict = errors.New("different operation for the same request id")
)

type clauseInput struct {
	RequestID        string `json:"requestId"`
	KnowledgeVersion int64  `json:"knowledgeVersion"`
	Number           string `json:"number"`
	ExpectedVersion  *int64 `json:"expectedVersion"`
	Text             string `json:"text"`
	Active           *bool  `json:"active"`
}

type clauseSnapshot struct {
	Number  string `json:"number"`
	Version int64  `json:"version"`
	Text    string `json:"text"`
}

type clauseEntry struct {
	clauseSnapshot
	Active bool `json:"active"`
}

type clausePage struct {
	Items            []clauseEntry `json:"items"`
	KnowledgeVersion int64         `json:"knowledgeVersion"`
	NextAfter        string        `json:"nextAfter,omitempty"`
}

// handleClauses 读取启用知识条目当前版本的条款目录。
// 参数 w 为响应对象，r 携带条目标识和可选的条款分页游标。
// 返回值 无。
func (s *Service) handleClauses(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(r.PathValue("id"))
	if !ok {
		respondError(w, errSource)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	after := query.Get("after")
	if err != nil || (r.URL.RawQuery != "" &&
		(len(query) != 1 || len(query["after"]) != 1 || !validClauseNumber(after))) {
		respondError(w, errInvalid)
		return
	}
	result, err := s.listClauses(r.Context(), id, after)
	if err != nil {
		respondError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

// handleSaveClause 追加正式知识条款的经办版本。
// 参数 w 为响应对象，r 携带条目标识、条款内容及预期版本。
// 返回值 无。
func (s *Service) handleSaveClause(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(r.PathValue("id"))
	if !ok {
		respondError(w, errSource)
		return
	}
	var input clauseInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || !input.normalize() {
		respondError(w, errInvalid)
		return
	}
	result, err := s.saveClause(r.Context(), id, input)
	if err != nil {
		respondError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, result)
}

// validClauseNumber 检查可引用与分页的条款编号格式。
// 参数 number 为省略“第”和“条”等前后缀的条款号。
// 返回值 编号受支持时为真。
func validClauseNumber(number string) bool {
	return utf8.RuneCountInString(number) <= 48 && clauseNumber.MatchString(number)
}

// normalize 校验并规范人工维护的条款内容和请求标识。
// 参数 无，使用当前请求字段。
// 返回值 提交内容满足版本和文本限制时为真。
func (input *clauseInput) normalize() bool {
	requestID, ok := parseUUID(input.RequestID)
	input.RequestID = requestID
	input.Number = strings.TrimSpace(input.Number)
	input.Text = strings.TrimSpace(input.Text)
	return ok && input.KnowledgeVersion > 0 && input.ExpectedVersion != nil && *input.ExpectedVersion >= 0 &&
		*input.ExpectedVersion < math.MaxInt64 &&
		input.Active != nil && validClauseNumber(input.Number) && input.Text != "" &&
		utf8.ValidString(input.Text) && utf8.RuneCountInString(input.Text) <= 4000 &&
		!strings.ContainsRune(input.Text, 0)
}

// listClauses 加载条款号排序的当前条款版本，并从下一条判断是否可翻页。
// 参数 ctx 为请求上下文，id 为知识条目标识，after 为上一页末尾的条款号。
// 返回值 固定知识版本的目录页，或知识不可用和存储错误。
func (s *Service) listClauses(ctx context.Context, id, after string) (clausePage, error) {
	result := clausePage{Items: []clauseEntry{}}
	if err := s.db.QueryRow(ctx, clauseEntryQuery, id).Scan(&result.KnowledgeVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = errSource
		}
		return result, err
	}
	rows, err := s.db.Query(ctx, `SELECT DISTINCT ON (number COLLATE "C") number,version,text,active
		FROM knowledge_clause_revisions WHERE knowledge_id=$1 AND knowledge_version=$2
		AND number COLLATE "C" > $3 ORDER BY number COLLATE "C",version DESC LIMIT $4`,
		id, result.KnowledgeVersion, after, clausePageLimit+1)
	if err != nil {
		return result, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToFunc[clauseEntry](readClauseRow))
	result.Items = items
	if len(items) > clausePageLimit {
		result.Items = items[:clausePageLimit]
		result.NextAfter = result.Items[clausePageLimit-1].Number
	}
	return result, err
}

// readClauseRow 读取目录一行，避免展示内部请求标识。
// 参数 row 为目录只读查询返回的当前条款。
// 返回值 业务条款及扫描错误。
func readClauseRow(row pgx.CollectableRow) (clauseEntry, error) {
	var item clauseEntry
	err := row.Scan(&item.Number, &item.Version, &item.Text, &item.Active)
	return item, err
}

// lockRequest 串行处理同一业务写入的重试，锁随事务结束释放。
// 参数 ctx 为请求上下文，tx 为写事务，key 为含业务前缀的请求标识。
// 返回值 加锁错误或 nil。
func lockRequest(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext(current_schema()),hashtext($1))`, key)
	return err
}

// saveClause 先恢复已提交的同一请求，再验证条目和条款版本并新增修订。
// 参数 ctx 为请求上下文，id 为知识标识，input 为已校验的编辑表单。
// 返回值 条款修订，或版本、重复提交及存储错误。
func (s *Service) saveClause(ctx context.Context, id string, input clauseInput) (clauseEntry, error) {
	result := clauseEntry{clauseSnapshot: clauseSnapshot{Number: input.Number,
		Version: *input.ExpectedVersion + 1, Text: input.Text}, Active: *input.Active}
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := lockRequest(ctx, tx, "clause:"+input.RequestID); err != nil {
			return err
		}
		replay, err := checkClauseReplay(ctx, tx, id, input, result)
		if replay || err != nil {
			return err
		}
		if err := lockClauseVersion(ctx, tx, id, input); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO knowledge_clause_revisions
			(knowledge_id,knowledge_version,number,version,text,active,request_id)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, id, input.KnowledgeVersion,
			result.Number, result.Version, result.Text, result.Active, input.RequestID)
		return err
	})
	return result, err
}

// checkClauseReplay 核对同一个请求标识是否已保存且业务内容完全相同。
// 参数 ctx 为请求上下文，tx 为事务，id 和 input 标识目标，expected 为期望写入的条款。
// 返回值 是否找到已完成的同一请求，以及冲突或存储错误。
func checkClauseReplay(ctx context.Context, tx pgx.Tx, id string, input clauseInput, expected clauseEntry) (bool, error) {
	var savedID string
	var version int64
	var saved clauseEntry
	err := tx.QueryRow(ctx, `SELECT knowledge_id::text,knowledge_version,number,version,text,active
		FROM knowledge_clause_revisions WHERE request_id=$1`, input.RequestID).
		Scan(&savedID, &version, &saved.Number, &saved.Version, &saved.Text, &saved.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if savedID != id || version != input.KnowledgeVersion || saved != expected {
		return false, errRequestConflict
	}
	return true, nil
}

// lockClauseVersion 与知识维护和留痕登记使用同一条目锁，拒绝使用过时编辑表单。
// 参数 ctx 为请求上下文，tx 为写入事务，id 为知识条目，input 包含所见版本。
// 返回值 成功锁定时为 nil，或版本冲突及存储错误。
func lockClauseVersion(ctx context.Context, tx pgx.Tx, id string, input clauseInput) error {
	var version int64
	err := tx.QueryRow(ctx, clauseEntryQuery+" FOR UPDATE", id).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && version != input.KnowledgeVersion) {
		return errSource
	}
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT coalesce(max(version),0) FROM knowledge_clause_revisions
		WHERE knowledge_id=$1 AND knowledge_version=$2 AND number=$3`,
		id, input.KnowledgeVersion, input.Number).Scan(&version)
	if err == nil && version != *input.ExpectedVersion {
		return errSource
	}
	return err
}
