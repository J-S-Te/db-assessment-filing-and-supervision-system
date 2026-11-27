package compliance

import (
	"context"
	"net/http"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/httpx"
)

type clauseReference struct {
	Title  string `json:"title"`
	Number string `json:"number"`
}

type clauseCandidate struct {
	knowledgeCandidate
	Clause clauseSnapshot `json:"clause"`
}

type clauseCandidatesPage struct {
	Items   []clauseCandidate `json:"items"`
	HasMore bool              `json:"hasMore"`
}

// handleClauseCandidates 返回当前人工校对栏目中明确引用的条款。
// 参数 w 为响应对象，r 携带报告及已校对的证据位置。
// 返回值 无。
func (s *Service) handleClauseCandidates(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(r.PathValue("id"))
	if !ok {
		respondError(w, errMissing)
		return
	}
	input, err := parseCandidateInput(r.URL.RawQuery)
	if err != nil {
		respondError(w, err)
		return
	}
	field, err := s.candidateField(r.Context(), id, input)
	if err != nil {
		respondError(w, err)
		return
	}
	result, err := s.findClauseCandidates(r.Context(), field)
	if err != nil {
		respondError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

// referencedClauses 解析书名号与紧邻的“第…条、款或项”，隔开同段其他引用。
// 参数 value 为原文或人工校对后的完整栏目快照。
// 返回值 去重的标题和条款号；忽略嵌套或跨行的标题。
func referencedClauses(value string) []clauseReference {
	items := []clauseReference{}
	seen := make(map[clauseReference]bool)
	depth, start := 0, -1
	for index, char := range value {
		switch char {
		case '\r', '\n':
			depth, start = 0, -1
		case '《':
			depth++
			start = -1
			if depth == 1 {
				start = index + len("《")
			}
		case '》':
			if depth == 0 {
				continue
			}
			depth--
			if depth != 0 || start < 0 || index == start ||
				utf8.RuneCountInString(value[start:index]) > maxReferenceTitle {
				continue
			}
			match := clauseCitation.FindStringSubmatch(value[index+len("》"):])
			if len(match) != 2 || !validClauseNumber(match[1]) {
				continue
			}
			ref := clauseReference{Title: value[start:index], Number: match[1]}
			if !seen[ref] {
				items = append(items, ref)
				seen[ref] = true
			}
		}
	}
	return items
}

// findClauseCandidates 查找同一标题和编号的启用知识条款，记录出现在原文还是校对值。
// 参数 ctx 为请求上下文，field 为已确认的报告栏目快照。
// 返回值 至多二十条候选及其分页提示，或数据库错误。
func (s *Service) findClauseCandidates(ctx context.Context, field evidenceField) (clauseCandidatesPage, error) {
	original, corrected := referencedClauses(field.Original), []clauseReference{}
	if field.Value != nil {
		corrected = referencedClauses(*field.Value)
	}
	result := clauseCandidatesPage{Items: []clauseCandidate{}}
	if len(original)+len(corrected) == 0 {
		return result, nil
	}
	rows, err := s.db.Query(ctx, clauseCandidatesSQL, original, corrected, candidateLimit+1)
	if err != nil {
		return result, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToFunc[clauseCandidate](readClauseCandidate))
	result.Items = items
	if len(items) > candidateLimit {
		result.Items = items[:candidateLimit]
		result.HasMore = true
	}
	return result, err
}

const clauseCandidatesSQL = `WITH mentioned AS (
	SELECT title,number,true AS original FROM jsonb_to_recordset($1::jsonb) AS m(title text,number text)
	UNION ALL
	SELECT title,number,false FROM jsonb_to_recordset($2::jsonb) AS m(title text,number text)
), refs AS (
	SELECT title COLLATE "C" AS title,number COLLATE "C" AS number,
		bool_or(original) AS in_original,bool_or(NOT original) AS in_corrected
	FROM mentioned GROUP BY 1,2
)
SELECT k.id::text,r.title,r.source,r.version,ref.in_original,ref.in_corrected,c.number,c.version,c.text
FROM refs ref JOIN knowledge_entries k ON md5(k.title)=md5(ref.title) AND k.title COLLATE "C"=ref.title
JOIN knowledge_revisions r ON r.entry_id=k.id AND r.version=k.version AND r.title COLLATE "C"=ref.title
JOIN LATERAL (
	SELECT number,version,text,active FROM knowledge_clause_revisions
	WHERE knowledge_id=k.id AND knowledge_version=k.version AND number=ref.number
	ORDER BY version DESC LIMIT 1
) c ON c.active
WHERE k.category IN ('regulation','standard') AND k.inactive_at IS NULL
AND r.category=k.category AND r.inactive_at IS NULL
ORDER BY r.title COLLATE "C",r.source COLLATE "C",k.id,c.number COLLATE "C" LIMIT $3`

// readClauseCandidate 读取命中的版本和出处，用于管理员对照。
// 参数 row 为明确引用的候选结果行。
// 返回值 候选内容及扫描错误。
func readClauseCandidate(row pgx.CollectableRow) (clauseCandidate, error) {
	var item clauseCandidate
	err := row.Scan(&item.ID, &item.Title, &item.Source, &item.Version, &item.InOriginal,
		&item.InCorrected, &item.Clause.Number, &item.Clause.Version, &item.Clause.Text)
	return item, err
}
