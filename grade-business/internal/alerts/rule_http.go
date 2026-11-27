package alerts

import (
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/httpx"
)

const rulesPath = "/api/v1/alerts/rules"

type ruleRequest struct {
	rulePolicy
	Enabled         *bool  `json:"enabled"`
	RequestID       string `json:"requestId,omitempty"`
	ExpectedVersion int64  `json:"expectedVersion,omitempty"`
}

type matchRow struct {
	ruleHit
	RuleID      string `json:"ruleId"`
	RuleName    string `json:"ruleName"`
	RuleVersion int64  `json:"ruleVersion"`
	MatchedAt   string `json:"matchedAt"`
	Sequence    int64  `json:"-"`
}

// registerRules 挂载监管预警规则、预览范围及命中留痕。
// 参数 mux 为业务路由。
// 返回值 无。
func (s *Service) registerRules(mux *http.ServeMux) {
	mux.HandleFunc("GET "+rulesPath, s.listRulesHTTP)
	mux.HandleFunc("POST "+rulesPath, s.ruleWriteHTTP("create"))
	mux.HandleFunc("PATCH "+rulesPath+"/{id}", s.ruleWriteHTTP("update"))
	mux.HandleFunc("POST "+rulesPath+"/preview", s.ruleWriteHTTP("preview"))
	mux.HandleFunc("POST "+rulesPath+"/sync", s.syncRulesHTTP)
	mux.HandleFunc("GET "+rulesPath+"/matches", s.listRuleMatchesHTTP)
}

// ruleWriteHTTP 严格区分预览、创建和更新的版本与重放参数。
// 参数 mode 为路由允许的固定操作。
// 返回值 规则写入或预览处理器。
func (s *Service) ruleWriteHTTP(mode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		input, ok := parseRuleRequest(w, r, mode)
		if !ok {
			respond(w, nil, errInvalid)
			return
		}
		if mode == "create" {
			item, err := s.storeRule(r.Context(), input.RequestID, input.rulePolicy)
			if err == nil {
				httpx.JSON(w, http.StatusCreated, item)
			} else {
				respond(w, nil, err)
			}
			return
		}
		if mode == "update" {
			item, err := s.updateRule(r.Context(), r.PathValue("id"), input.ExpectedVersion, input.rulePolicy)
			respond(w, item, err)
			return
		}
		s.previewRuleHTTP(w, r, input.rulePolicy)
	}
}

// parseRuleRequest 校验请求只包含该操作需要的入口字段。
// 参数 w、r 为响应及请求，mode 为当前操作类型。
// 返回值 规范化表单及是否合法。
func parseRuleRequest(w http.ResponseWriter, r *http.Request, mode string) (ruleRequest, bool) {
	var input ruleRequest
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || input.Enabled == nil {
		return input, false
	}
	input.rulePolicy.Enabled = *input.Enabled
	if !normalizeRule(&input.rulePolicy, true) {
		return input, false
	}
	switch mode {
	case "create":
		_, valid := parseAlertID(input.RequestID)
		return input, valid && input.ExpectedVersion == 0
	case "update":
		_, valid := parseAlertID(r.PathValue("id"))
		return input, valid && input.ExpectedVersion > 0 && input.RequestID == ""
	default:
		return input, input.ExpectedVersion == 0 && input.RequestID == ""
	}
}

// previewRuleHTTP 读取有限的真实命中样本，不修改通知和规则。
// 参数 w、r、input 分别为响应、请求与校验过的配置。
// 返回值 无。
func (s *Service) previewRuleHTTP(w http.ResponseWriter, r *http.Request, input rulePolicy) {
	rows, err := s.db.Query(r.Context(), rulePreviewSQL, s.ruleWindows.InitialDays,
		s.ruleWindows.FinalDays, input.Trigger, input.Audience, input.UnitID, input.District, input.Level)
	if err != nil {
		respond(w, nil, err)
		return
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ruleHit])
	result := struct {
		Items   []ruleHit `json:"items"`
		HasMore bool      `json:"hasMore,omitempty"`
	}{Items: items, HasMore: len(items) > rulesPageSize}
	if result.HasMore {
		result.Items = items[:rulesPageSize]
	}
	respond(w, result, err)
}

// syncRulesHTTP 手动更新本地业务事实并按现行规则进行一次定向扫描。
// 参数 w、r 为响应和无正文的监管同步请求。
// 返回值 无。
func (s *Service) syncRulesHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
	if r.URL.RawQuery != "" || err != nil || len(body) != 0 {
		respond(w, nil, errInvalid)
		return
	}
	if _, err = s.sync(r.Context()); err != nil {
		respond(w, nil, err)
		return
	}
	out, err := s.sweepRules(r.Context())
	respond(w, out, err)
}

// listRulesHTTP 读取监管配置的预警规则和版本。
// 参数 w、r 为响应和分页请求。
// 返回值 无。
func (s *Service) listRulesHTTP(w http.ResponseWriter, r *http.Request) {
	readRuleList(w, r, s, "SELECT "+ruleColumns+" FROM alert_config_rules", pgx.RowToStructByPos[ruleRow],
		func(item ruleRow) int64 { return item.Sequence })
}

// listRuleMatchesHTTP 返回近期规则命中及当时的接收分配。
// 参数 w、r 为响应和分页请求。
// 返回值 无。
func (s *Service) listRuleMatchesHTTP(w http.ResponseWriter, r *http.Request) {
	const data = `SELECT resource,resource_id::text,reference,system_name,organization_name,
		audience,recipient,to_char(deadline,'YYYY-MM-DD'),rule_id::text,rule_name,rule_version,
		to_char(matched_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),sequence FROM alert_rule_matches`
	readRuleList(w, r, s, data, pgx.RowToStructByPos[matchRow], func(item matchRow) int64 { return item.Sequence })
}

// readRuleList 统一按递减业务序号读取配置或命中的一页数据。
// 参数 w、r、s 为响应、分页请求和服务，query、decode、sequence 为当前列表的固定查询及解码。
// 返回值 无。
func readRuleList[T any](w http.ResponseWriter, r *http.Request, s *Service, query string,
	decode pgx.RowToFunc[T], sequence func(T) int64,
) {
	before, ok := ruleBefore(r)
	if !ok {
		respond(w, nil, errInvalid)
		return
	}
	rows, err := s.db.Query(r.Context(), query+" WHERE ($1::bigint=0 OR sequence<$1) ORDER BY sequence DESC LIMIT 21", before)
	if err != nil {
		respond(w, nil, err)
		return
	}
	items, err := pgx.CollectRows(rows, decode)
	result := ruleCollection[T]{Items: items}
	if len(items) > rulesPageSize {
		result.Items = items[:rulesPageSize]
		result.NextBefore = strconv.FormatInt(sequence(items[rulesPageSize-1]), 10)
	}
	respond(w, result, err)
}

// ruleBefore 解析不携带额外条件的规则列表游标。
// 参数 r 为分页请求。
// 返回值 分页位置，或输入不合法时的状态。
func ruleBefore(r *http.Request) (int64, bool) {
	if r.URL.RawQuery == "" {
		return 0, true
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 1 || len(query["before"]) != 1 {
		return 0, false
	}
	value, err := strconv.ParseInt(query.Get("before"), 10, 64)
	return value, err == nil && value > 0
}
