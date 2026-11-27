package archives

import "time"

type page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type unitRow struct {
	Key               string     `json:"key"`
	OrganizationName  string     `json:"organizationName"`
	DirectoryName     string     `json:"directoryName,omitempty"`
	CreditCode        string     `json:"creditCode,omitempty"`
	Classification    string     `json:"classification,omitempty"`
	District          string     `json:"district,omitempty"`
	DirectoryStatus   string     `json:"directoryStatus,omitempty"`
	SystemCount       int64      `json:"systemCount"`
	ActiveSystemCount int64      `json:"activeSystemCount"`
	LatestReportAt    *time.Time `json:"latestReportAt,omitempty"`
	OpenHazardCount   int64      `json:"openHazardCount"`
	ClosedHazardCount int64      `json:"closedHazardCount"`
}

type rowOrder struct {
	ID     string    `json:"id"`
	SortAt time.Time `json:"-"`
}

type systemRow struct {
	rowOrder
	FilingNumber      string     `json:"filingNumber"`
	SystemName        string     `json:"systemName"`
	OrganizationName  string     `json:"organizationName"`
	AssessmentOrg     string     `json:"assessmentOrg"`
	Level             string     `json:"level"`
	Status            string     `json:"status"`
	CancelledAt       *time.Time `json:"cancelledAt,omitempty"`
	LatestReportAt    *time.Time `json:"latestReportAt,omitempty"`
	OpenHazardCount   int64      `json:"openHazardCount"`
	ClosedHazardCount int64      `json:"closedHazardCount"`
	AssetCount        int64      `json:"assetCount"`
}

type reportRow struct {
	rowOrder
	FilingID      string     `json:"filingId"`
	SystemName    string     `json:"systemName"`
	Number        string     `json:"number"`
	AssessmentOrg string     `json:"assessmentOrg"`
	Risk          string     `json:"risk"`
	Status        string     `json:"status"`
	SubmittedAt   *time.Time `json:"submittedAt,omitempty"`
}

type remediationRow struct {
	rowOrder
	ReportID     string     `json:"reportId"`
	SystemName   string     `json:"systemName"`
	ReportNumber string     `json:"reportNumber"`
	Deadline     string     `json:"deadline"`
	Status       string     `json:"status"`
	SubmittedAt  *time.Time `json:"submittedAt,omitempty"`
}

type hazardRow struct {
	rowOrder
	PlanID      string     `json:"planId"`
	SystemName  string     `json:"systemName"`
	Severity    string     `json:"severity"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Deadline    string     `json:"deadline"`
	ClosedAt    *time.Time `json:"closedAt,omitempty"`
}

type relatedRecords struct {
	Reports      page[reportRow]      `json:"reports"`
	Remediations page[remediationRow] `json:"remediations"`
	Hazards      page[hazardRow]      `json:"hazards"`
}

type unitAssetRow struct {
	systemAsset
	SystemName string `json:"systemName"`
}

type unitProfile struct {
	Unit         unitRow            `json:"unit"`
	Systems      page[systemRow]    `json:"systems"`
	AssetRecords page[unitAssetRow] `json:"assetRecords"`
	relatedRecords
}

type systemProfile struct {
	System systemRow `json:"system"`
	relatedRecords
}
