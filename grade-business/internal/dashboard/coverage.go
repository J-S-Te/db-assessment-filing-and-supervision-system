package dashboard

type filingCoverage struct {
	Covered  int64 `json:"covered"`
	Eligible int64 `json:"eligible"`
}

type districtCoverage struct {
	District string `json:"district"`
	filingCoverage
}

type registeredAssets struct {
	IP            int64 `json:"ip"`
	Domain        int64 `json:"domain"`
	Topology      int64 `json:"topology"`
	Supplier      int64 `json:"supplier"`
	SecurityEvent int64 `json:"securityEvent"`
	Cloud         int64 `json:"cloud"`
	BigData       int64 `json:"bigData"`
}

const coverageStatisticsSQL = `
coverage_counts AS (
    SELECT count(*) FILTER (WHERE covered) AS covered,count(*) AS eligible
    FROM archive_coverage_status WHERE active AND
      ($1::text IS NULL OR COALESCE(NULLIF(btrim(district),''),'') COLLATE "C"=$1)
), district_coverage AS (
    SELECT COALESCE(NULLIF(btrim(district),''),'未登记区县') AS district,
           count(*) FILTER (WHERE covered) AS covered,count(*) AS eligible
    FROM archive_coverage_status WHERE active AND
      ($1::text IS NULL OR COALESCE(NULLIF(btrim(district),''),'') COLLATE "C"=$1)
    GROUP BY 1 ORDER BY eligible DESC,district LIMIT 20
), asset_counts AS (
    SELECT count(*) FILTER (WHERE a.kind='ip') AS ip,count(*) FILTER (WHERE a.kind='domain') AS domain,
           count(*) FILTER (WHERE a.kind='topology') AS topology,
           count(*) FILTER (WHERE a.kind='supplier') AS supplier,
           count(*) FILTER (WHERE a.kind='security_event') AS security_event,
           count(*) FILTER (WHERE a.kind='cloud') AS cloud,
           count(*) FILTER (WHERE a.kind='big_data') AS big_data
    FROM archive_system_assets a JOIN scope_filings f ON f.id=a.filing_id
    WHERE a.active AND f.kind='filing' AND f.status='approved'
      AND f.cancelled_at IS NULL AND f.discarded_at IS NULL
)
`
