package compliance

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/httpx"
)

const candidateLimit = 20

const maxReferenceTitle = 200

type candidateInput struct {
	attachmentID      string
	extractionVersion int64
	fieldKey          string
}

type knowledgeCandidate struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Source      string `json:"source"`
	Version     int64  `json:"version"`
	InOriginal  bool   `json:"inOriginal"`
	InCorrected bool   `json:"inCorrected"`
}

type candidatePage struct {
	Items   []knowledgeCandidate `json:"items"`
	HasMore bool                 `json:"hasMore"`
}

// handleCandidates 按所选校对证据返回有明确文档名引用的知识库候选。
// 参数 w 为响应对象，r 携带当前报告、正文版本和栏目。
// 返回值 无。
func (s *Service) handleCandidates(w http.ResponseWriter, r *http.Request) {
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
	page, err := s.candidates(r.Context(), id, input)
	if err != nil {
		respondError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, page)
}

// parseCandidateInput 校验只读候选查询的附件、校对版本和证据栏目。
// 参数 raw 为未经解码的查询字符串。
// 返回值 规范化查询参数，或缺失、多余和无效参数错误。
func parseCandidateInput(raw string) (candidateInput, error) {
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) != 3 || len(values["attachmentId"]) != 1 ||
		len(values["extractionVersion"]) != 1 || len(values["fieldKey"]) != 1 {
		return candidateInput{}, errInvalid
	}
	attachment, ok := parseUUID(values.Get("attachmentId"))
	versionText := values.Get("extractionVersion")
	version, err := strconv.ParseInt(versionText, 10, 64)
	key := values.Get("fieldKey")
	if !ok || err != nil || version < 1 || strconv.FormatInt(version, 10) != versionText || !validFieldKey(key) {
		return candidateInput{}, errInvalid
	}
	return candidateInput{attachmentID: attachment, extractionVersion: version, fieldKey: key}, nil
}

// candidateField 读取待人工审核报告中由经办人确认的当前正文和栏目快照。
// 参数 ctx 为请求上下文，id 为报告标识，input 为用户选定的校对证据。
// 返回值 证据快照，或报告、状态、证据不可用及数据库错误。
func (s *Service) candidateField(ctx context.Context, id string, input candidateInput) (evidenceField, error) {
	var status string
	var field *evidenceField
	err := s.db.QueryRow(ctx, `SELECT c.status,source.field FROM business_cases c
		LEFT JOIN LATERAL (
			SELECT f.field FROM case_attachments a
			JOIN report_extractions e ON e.attachment_id=a.id
			JOIN report_extraction_reviews r ON r.attachment_id=e.attachment_id AND r.version=e.version
			CROSS JOIN LATERAL jsonb_array_elements(e.fields) AS f(field)
			WHERE a.case_id=c.id AND a.kind='report_file' AND a.id=$2 AND e.version=$3
			AND e.confirmed_at IS NOT NULL AND f.field->>'key'=$4
		) source ON true
		WHERE c.id=$1 AND c.kind='report' AND c.discarded_at IS NULL`,
		id, input.attachmentID, input.extractionVersion, input.fieldKey).Scan(&status, &field)
	if errors.Is(err, pgx.ErrNoRows) {
		return evidenceField{}, errMissing
	}
	if err != nil {
		return evidenceField{}, err
	}
	if !canRecord(status) {
		return evidenceField{}, errState
	}
	if field == nil || field.Start < 1 || field.End < field.Start {
		return evidenceField{}, errSource
	}
	return *field, nil
}

// candidates 查找有效法规和标准中与所选证据明确引用名称完全相同的当前版本。
// 参数 ctx 为请求上下文，id 为报告标识，input 为要核对的附件和栏目。
// 返回值 至多二十条按标题及来源排序的候选，或业务和数据库错误。
func (s *Service) candidates(ctx context.Context, id string, input candidateInput) (candidatePage, error) {
	field, err := s.candidateField(ctx, id, input)
	if err != nil {
		return candidatePage{}, err
	}
	original := referencedTitles(field.Original)
	var corrected []string
	if field.Value != nil {
		corrected = referencedTitles(*field.Value)
	}
	page := candidatePage{Items: make([]knowledgeCandidate, 0)}
	if len(original)+len(corrected) == 0 {
		return page, nil
	}
	page.Items, err = s.findKnowledgeCandidates(ctx, original, corrected)
	if err != nil {
		return candidatePage{}, err
	}
	if len(page.Items) > candidateLimit {
		page.Items = page.Items[:candidateLimit]
		page.HasMore = true
	}
	return page, nil
}

// referencedTitles 保留同一证据中使用书名号明确标出的原样文档名称。
// 参数 text 为当前校对快照中的原文或校对值。
// 返回值 原样且去重的标题列表。
func referencedTitles(text string) []string {
	titles := make([]string, 0)
	seen := make(map[string]bool)
	depth, start := 0, -1
	for index, char := range text {
		switch char {
		case '\r', '\n':
			depth, start = 0, -1
		case '《':
			depth++
			start = -1
			if depth == 1 {
				start = index + utf8.RuneLen(char)
			}
		case '》':
			if depth == 0 {
				continue
			}
			depth--
			if depth != 0 || start < 0 || start == index {
				continue
			}
			title := text[start:index]
			if !seen[title] && utf8.RuneCountInString(title) <= maxReferenceTitle {
				titles = append(titles, title)
				seen[title] = true
			}
		}
	}
	return titles
}

// findKnowledgeCandidates 定位启用条目对应的同版本知识正文，并标记标题出现的证据类型。
// 参数 ctx 为请求上下文，original 和 corrected 分别为证据原文及校对值中的精确标题。
// 返回值 用于限制结果和标记更多条目的候选列表，或数据库错误。
func (s *Service) findKnowledgeCandidates(ctx context.Context, original, corrected []string) ([]knowledgeCandidate, error) {
	rows, err := s.db.Query(ctx, `WITH document_titles AS (
		SELECT unnest($1::text[]) COLLATE "C" AS title UNION SELECT unnest($2::text[]) COLLATE "C"
	) SELECT k.id::text,r.title,r.source,r.version,
		coalesce(r.title COLLATE "C" = ANY($1::text[]),false),
		coalesce(r.title COLLATE "C" = ANY($2::text[]),false)
		FROM document_titles d JOIN knowledge_entries k
			ON md5(k.title)=md5(d.title) AND k.title COLLATE "C"=d.title COLLATE "C"
		JOIN knowledge_revisions r ON r.entry_id=k.id AND r.version=k.version
		WHERE k.category IN ('regulation','standard') AND k.inactive_at IS NULL
		AND r.category=k.category AND r.title COLLATE "C"=d.title COLLATE "C" AND r.inactive_at IS NULL
		ORDER BY r.title COLLATE "C",r.source COLLATE "C",k.id LIMIT $3`,
		original, corrected, candidateLimit+1)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[knowledgeCandidate])
}
