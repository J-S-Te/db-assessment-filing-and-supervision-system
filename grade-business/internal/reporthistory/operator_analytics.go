package reporthistory

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type operatorPair struct {
	organization     string
	filing, from, to pgtype.UUID
}

type annualAssessment struct {
	Year         int      `json:"year"`
	ReportNumber string   `json:"reportNumber,omitempty"`
	Score        *float64 `json:"score,omitempty"`
}

type assessmentTrend struct {
	Items []annualAssessment `json:"items"`
}

type issueEntry struct {
	Description string `json:"description"`
	Risk        string `json:"risk"`
}

type reportIssueSource struct {
	ID    string
	Items []issueEntry
}

type issueChange struct {
	Description string `json:"description"`
	Change      string `json:"change"`
	FromRisk    string `json:"fromRisk,omitempty"`
	ToRisk      string `json:"toRisk,omitempty"`
}

type issueComparison struct {
	FromAvailable bool          `json:"fromAvailable"`
	ToAvailable   bool          `json:"toAvailable"`
	Items         []issueChange `json:"items"`
}

// readOperatorPair 解析现有对比页面所选的两份报告及备案范围。
// 参数 r 携带备案标识和运营单位、前后期报告查询条件。
// 返回值 可用于额度为两份报告的读取条件，或输入错误。
func readOperatorPair(r *http.Request) (operatorPair, error) {
	filing, ok := parseHistoryUUID(r.PathValue("id"))
	if !ok {
		return operatorPair{}, pgx.ErrNoRows
	}
	values, organization, err := operatorAssessmentQuery(r.URL.RawQuery, "from", "to")
	if err != nil {
		return operatorPair{}, err
	}
	values.Del("organization")
	from, to, err := pairIDs(values.Encode())
	return operatorPair{organization: organization, filing: filing, from: from, to: to}, err
}

// operatorTrendHTTP 查询同一备案当前可交付的年度得分。
// 参数 w 为响应端，r 仅接受备案和运营单位范围。
// 返回值 无。
func (s *Service) operatorTrendHTTP(w http.ResponseWriter, r *http.Request) {
	filing, ok := parseHistoryUUID(r.PathValue("id"))
	if !ok {
		respondHistory(w, nil, pgx.ErrNoRows)
		return
	}
	_, organization, err := operatorAssessmentQuery(r.URL.RawQuery)
	if err != nil {
		respondHistory(w, nil, err)
		return
	}
	result, err := s.operatorTrend(r.Context(), organization, filing)
	respondHistory(w, result, err)
}

// operatorIssuesHTTP 比较所选报告在现行范围内已交付的整改问题。
// 参数 w 为响应端，r 携带备案、运营单位及前后期报告。
// 返回值 无。
func (s *Service) operatorIssuesHTTP(w http.ResponseWriter, r *http.Request) {
	pair, err := readOperatorPair(r)
	if err != nil {
		respondHistory(w, nil, err)
		return
	}
	result, err := s.operatorIssues(r.Context(), pair)
	respondHistory(w, result, err)
}

const operatorYearlyResultSQL = `WITH eligible AS (
	SELECT r.id, r.number, ` + reportChronology + ` AS assessed_at,
		EXTRACT(YEAR FROM (` + reportChronology + ` AT TIME ZONE 'Asia/Shanghai'))::int AS year
	FROM business_cases f JOIN business_cases r ON r.filing_id=f.id
		AND r.kind='report' AND r.status='approved' AND r.discarded_at IS NULL
	WHERE ` + operatorReleasedReportScope + `
), recent AS (
	SELECT DISTINCT ON (year) id, number, year FROM eligible
	ORDER BY year DESC, assessed_at DESC, id DESC LIMIT 10
)
SELECT r.year,r.number,a.score_hundredths::float8/100
FROM recent r LEFT JOIN LATERAL (
	SELECT score_hundredths FROM assessment_result_revisions
	WHERE report_id=r.id ORDER BY version DESC LIMIT 1
) a ON true ORDER BY r.year DESC`

// operatorTrend 以只读快照查看归属和可交付终审资料，选择每年最新的一份。
// 参数 ctx 为请求上下文，organization 和 filing 指定单位及同一备案。
// 返回值 最近至多十个上海自然年的年度结果，或归属及存储错误。
func (s *Service) operatorTrend(ctx context.Context, organization string, filing pgtype.UUID) (assessmentTrend, error) {
	var result assessmentTrend
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			var system assessmentSystem
			if err := tx.QueryRow(ctx, operatorArchiveSystemSQL+" AND f.id=$2", organization, filing).
				Scan(&system.ID, &system.Number, &system.SystemName); err != nil {
				return err
			}
			rows, err := tx.Query(ctx, operatorYearlyResultSQL, organization, filing)
			if err != nil {
				return err
			}
			recent, err := pgx.CollectRows(rows, pgx.RowToStructByPos[annualAssessment])
			if err == nil {
				result.Items = fillAnnualGaps(recent)
			}
			return err
		})
	return result, err
}

// fillAnnualGaps 为报告间缺少数据的年份保留空位，避免遗漏被误读成零分。
// 参数 recent 为按报告自然年倒序排列的至多十条已核对记录。
// 返回值 连续且按时间升序排列的有界年度趋势。
func fillAnnualGaps(recent []annualAssessment) []annualAssessment {
	items := []annualAssessment{}
	if len(recent) == 0 {
		return items
	}
	lastYear := recent[0].Year
	firstYear := max(lastYear-9, recent[len(recent)-1].Year)
	for year := firstYear; year <= lastYear; year++ {
		items = append(items, annualAssessment{Year: year})
	}
	for _, item := range recent {
		if item.Year >= firstYear {
			items[item.Year-firstYear] = item
		}
	}
	return items
}

const operatorIssueSourceSQL = `WITH available AS (
	SELECT r.id,f.organization_name AS current_organization,` + reportChronology + ` AS assessed_at
	FROM business_cases f JOIN business_cases r ON r.filing_id=f.id AND ` + operatorApprovedReport + `
	WHERE ` + operatorReleasedReportScope + ` AND r.id IN ($3,$4)
)
SELECT r.id::text,(SELECT p.risks ` + operatorApprovedPlan + `)
FROM available r ORDER BY r.assessed_at,r.id`

// operatorIssues 仅在两份资料均按原归属可交付时比较其已批准问题。
// 参数 ctx 为请求上下文，pair 为经校验的同备案前后期范围。
// 返回值 可用性与问题变化，或归属、顺序及数据库错误。
func (s *Service) operatorIssues(ctx context.Context, pair operatorPair) (issueComparison, error) {
	rows, err := s.db.Query(ctx, operatorIssueSourceSQL, pair.organization, pair.filing, pair.from, pair.to)
	if err != nil {
		return issueComparison{}, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[reportIssueSource])
	if err != nil {
		return issueComparison{}, err
	}
	if len(items) != 2 {
		return issueComparison{}, pgx.ErrNoRows
	}
	if items[0].ID != pair.from.String() {
		return issueComparison{}, errOrder
	}
	return compareIssueLists(items[0].Items, items[1].Items), nil
}

// compareIssueLists 对两期有据清单按原描述匹配，分别标明重复、新增或本期未列出。
// 参数 from、to 为前后两期已批问题；无已批清单时为 nil。
// 返回值 可用性和实际列出的差异，任一期无清单时不推断。
func compareIssueLists(from, to []issueEntry) issueComparison {
	result := issueComparison{FromAvailable: from != nil, ToAvailable: to != nil, Items: []issueChange{}}
	if from == nil || to == nil {
		return result
	}
	before, after := indexAssessmentIssues(from), indexAssessmentIssues(to)
	for key, item := range after {
		change := issueChange{Description: item.Description, ToRisk: item.Risk, Change: "added"}
		if prior, ok := before[key]; ok {
			change.Change, change.FromRisk = "recurring", prior.Risk
		}
		result.Items = append(result.Items, change)
	}
	for key, item := range before {
		if _, ok := after[key]; !ok {
			result.Items = append(result.Items, issueChange{
				Description: item.Description, FromRisk: item.Risk, Change: "not_listed",
			})
		}
	}
	order := map[string]int{"recurring": 0, "added": 1, "not_listed": 2}
	slices.SortFunc(result.Items, func(a, b issueChange) int {
		if a.Change != b.Change {
			return order[a.Change] - order[b.Change]
		}
		return strings.Compare(a.Description, b.Description)
	})
	return result
}

// indexAssessmentIssues 合并一份清单中的重复填写，展示最高的已登记风险等级。
// 参数 items 为已批整改清单，匹配时统一连续空白及英文字母大小写。
// 返回值 描述对应的实际最高风险问题。
func indexAssessmentIssues(items []issueEntry) map[string]issueEntry {
	ranks := map[string]int{"low": 1, "medium": 2, "high": 3}
	index := make(map[string]issueEntry, len(items))
	for _, item := range items {
		item.Description = strings.Join(strings.Fields(item.Description), " ")
		key := strings.ToLower(item.Description)
		if key != "" && ranks[item.Risk] > ranks[index[key].Risk] {
			index[key] = item
		}
	}
	return index
}
