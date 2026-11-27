package alerts

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

const enqueueSupervisionLeads = `
WITH candidates AS (
	SELECT a.id, f.number, f.system_name, f.organization_name, h.deadline
	FROM local_alerts a` + alertSourceJoins + `
	WHERE a.kind='overdue' AND a.status<>'closed' AND h.status<>'closed'
	  AND h.deadline<` + alertBusinessDateSQL + `
	  AND NOT EXISTS (SELECT 1 FROM alert_supervision_leads l WHERE l.alert_id=a.id)
	ORDER BY a.id FOR SHARE OF h,a,f SKIP LOCKED
)
INSERT INTO alert_supervision_leads(alert_id,filing_number,system_name,organization_name,deadline)
SELECT id,number,system_name,organization_name,deadline FROM candidates
ON CONFLICT (alert_id) DO NOTHING
`

type supervisionLead struct {
	AlertID          string    `json:"alertId"`
	FilingNumber     string    `json:"filingNumber"`
	SystemName       string    `json:"systemName"`
	OrganizationName string    `json:"organizationName"`
	Deadline         string    `json:"deadline"`
	AdmittedAt       time.Time `json:"admittedAt"`
	Status           string    `json:"status"`
	Active           bool      `json:"active"`
	Sequence         int64     `json:"-"`
}

type supervisionLeadPage struct {
	Items      []supervisionLead `json:"items"`
	NextBefore string            `json:"nextBefore,omitempty"`
}

// handleSupervisionLeads 查询监管重点线索及当前是否仍需跟踪。
// 参数 w、r 为监管响应和请求。
// 返回值 无。
func (s *Service) handleSupervisionLeads(w http.ResponseWriter, r *http.Request) {
	page, err := s.readSupervisionLeads(r)
	respond(w, page, err)
}

// readSupervisionLeads 查询有界线索队列，保留原入册期限和归属。
// 参数 r 为包含筛选、游标及上下文的监管请求。
// 返回值 已登记的线索分页及参数或数据库错误。
func (s *Service) readSupervisionLeads(r *http.Request) (supervisionLeadPage, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return supervisionLeadPage{}, errInvalid
	}
	before, active, err := parseLeadQuery(values)
	if err != nil {
		return supervisionLeadPage{}, err
	}
	rows, err := s.db.Query(r.Context(), `
	WITH records AS (
		SELECT a.id::text,l.filing_number,l.system_name,l.organization_name,
		       to_char(l.deadline,'YYYY-MM-DD'),l.admitted_at,a.status,
		       (a.status<>'closed' AND h.status<>'closed' AND h.deadline<`+alertBusinessDateSQL+`) AS active,
		       l.sequence
		FROM alert_supervision_leads l JOIN local_alerts a ON a.id=l.alert_id
		JOIN hazard_items h ON h.id=a.hazard_id
		WHERE ($1::bigint=0 OR l.sequence<$1)
	)
	SELECT * FROM records WHERE (NOT $2 OR active) ORDER BY sequence DESC LIMIT 21`, before, active)
	if err != nil {
		return supervisionLeadPage{}, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[supervisionLead])
	page := supervisionLeadPage{Items: items}
	if len(items) > 20 {
		page.Items = items[:20]
		page.NextBefore = strconv.FormatInt(items[19].Sequence, 10)
	}
	return page, err
}

// parseLeadQuery 校验仅供监管线索列表使用的状态及有序位置。
// 参数 values 为已解码的查询参数。
// 返回值 游标、是否只看跟踪中线索及参数错误。
func parseLeadQuery(values url.Values) (int64, bool, error) {
	for key, fields := range values {
		if len(fields) != 1 || (key != "before" && key != "status") {
			return 0, false, errInvalid
		}
	}
	status := values.Get("status")
	if status != "" && status != "active" && status != "all" {
		return 0, false, errInvalid
	}
	var before int64
	var err error
	if values.Has("before") {
		before, err = strconv.ParseInt(values.Get("before"), 10, 64)
		if err != nil || before < 1 {
			return 0, false, errInvalid
		}
	}
	return before, status == "active", nil
}
