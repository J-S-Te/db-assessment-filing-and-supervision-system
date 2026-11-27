package cases

import (
	"time"

	"grade-business/internal/classification"
	"grade-business/internal/directoryref"
	"grade-business/internal/filingpolicy"
)

// Attachment 提供可公开的材料元数据。
type Attachment struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	FileName   string    `json:"fileName"`
	Size       int64     `json:"size"`
	UploadedAt time.Time `json:"uploadedAt"`
}

// MaterialSnapshot 在同一个数据库快照中提供实际附件及其版本。
type MaterialSnapshot struct {
	Revision    int64
	Attachments []Attachment
}

type recordRow struct {
	ID                    string     `json:"id"`
	Number                string     `json:"number"`
	SystemName            string     `json:"systemName"`
	OrganizationName      string     `json:"organizationName"`
	AssessmentOrg         string     `json:"assessmentOrg"`
	Level                 string     `json:"level,omitempty"`
	FilingNumber          string     `json:"filingNumber,omitempty"`
	Risk                  string     `json:"risk,omitempty"`
	Status                string     `json:"status"`
	SubmittedAt           *time.Time `json:"submittedAt,omitempty"`
	CancelledAt           *time.Time `json:"cancelledAt,omitempty"`
	InitialReviewAudience string     `json:"initialReviewAudience,omitempty"`
	PrecheckStatus        string     `json:"precheckStatus,omitempty"`
	BatchVersion          *int64     `json:"version,omitempty"`
}

type recordDetail struct {
	recordRow
	FilingID                string                     `json:"filingId,omitempty"`
	Version                 int64                      `json:"version"`
	AllowedActions          []string                   `json:"allowedActions"`
	RequiredMaterials       []string                   `json:"requiredMaterials,omitempty"`
	ReceiptAvailable        *bool                      `json:"receiptAvailable,omitempty"`
	AssessmentPersonID      *string                    `json:"assessmentPersonId,omitempty"`
	DirectorySnapshot       *directoryref.Snapshot     `json:"directorySnapshot,omitempty"`
	Classification          *classification.Assessment `json:"classificationAssessment,omitempty"`
	PolicyMaterials         []filingpolicy.Rule        `json:"policyMaterials,omitempty"`
	FilingNumberProvisional bool                       `json:"filingNumberProvisional,omitempty"`
	operatorOrigin          bool
	withdrawalPending       bool
}

type listResponse struct {
	Items      []recordRow `json:"items"`
	NextCursor string      `json:"nextCursor,omitempty"`
}

type filingInput struct {
	Number           string                `json:"number"`
	SystemName       string                `json:"systemName"`
	OrganizationName string                `json:"organizationName"`
	AssessmentOrg    string                `json:"assessmentOrg"`
	Level            string                `json:"level"`
	Classification   *classification.Input `json:"classificationAssessment,omitempty"`
}

type reportInput struct {
	Number             string  `json:"number"`
	FilingID           string  `json:"filingId"`
	AssessmentOrg      string  `json:"assessmentOrg"`
	Risk               string  `json:"risk"`
	AssessmentPersonID *string `json:"assessmentPersonId,omitempty"`
}

type versionInput struct {
	ExpectedVersion int64 `json:"expectedVersion"`
}

type reviewInput struct {
	versionInput
	Decision            string `json:"decision"`
	Comment             string `json:"comment"`
	FilingNumber        string `json:"filingNumber,omitempty"`
	institution         string
	institutionReviewer string
}

type mutation struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Version int64  `json:"version"`
}

type reviewEvent struct {
	Action            string                 `json:"action"`
	FromStatus        string                 `json:"fromStatus"`
	ToStatus          string                 `json:"toStatus"`
	Operator          string                 `json:"operator"`
	Comment           string                 `json:"comment,omitempty"`
	OccurredAt        time.Time              `json:"occurredAt"`
	DirectorySnapshot *directoryref.Snapshot `json:"directorySnapshot,omitempty"`
}

type listQuery struct {
	keyword, status, lifecycle, risk, institution string
	assignedReview                                bool
	includePrecheckStatus                         bool
	cursor                                        listCursor
	limit                                         int
}

type listCursor struct {
	CreatedAt time.Time
	ID        string
}

type attachmentRow struct {
	Attachment
	fileKey, digest, issueID string
}
