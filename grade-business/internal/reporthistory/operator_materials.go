package reporthistory

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"grade-business/internal/cases"
)

type operatorReportScope struct {
	organization string
	filing       pgtype.UUID
	report       pgtype.UUID
}

type reportRequirement struct {
	Risk        string `json:"risk"`
	Description string `json:"description"`
	Measure     string `json:"measure"`
	Deadline    string `json:"deadline"`
}

type reportRequirements struct {
	Deadline string              `json:"deadline"`
	Items    []reportRequirement `json:"items"`
}

type operatorReportMaterials struct {
	Items        []cases.Attachment  `json:"items"`
	Requirements *reportRequirements `json:"requirements,omitempty"`
}

// 以原报告和当前备案的共同归属控制详情、原件和新的派生分析。
const operatorReleasedReportScope = operatorFilingState + ` AND f.id=$2
	AND ((` + operatorOriginalReport + `) OR (
		r.merge_history_scope IS NULL AND (` + operatorCurrentOwner + `)
		AND COALESCE(NULLIF(r.directory_snapshot#>>'{unit,scopeName}',''),
			NULLIF(r.organization_name,''),f.organization_name)=f.organization_name
		  AND (r.directory_unit_id IS NULL OR f.directory_unit_id IS NULL
			OR r.directory_unit_id=f.directory_unit_id)
		  AND NOT EXISTS (
			SELECT 1 FROM filing_applications a
			WHERE a.filing_id=f.id AND a.application_type='change' AND a.status='approved'
			  AND a.applied_at>=` + reportChronology + `
			  AND a.before_values->>'organizationName' IS DISTINCT FROM a.after_values->>'organizationName'
		  )
		  AND NOT EXISTS (
			SELECT 1 FROM directory_unit_merges m
			WHERE m.merged_at>=` + reportChronology + `
			  AND m.plan->'moves' @> jsonb_build_array(jsonb_build_object('id',f.id::text))
		  )
	))`

const operatorMaterialsSource = `
	WITH available AS (
		SELECT r.id, f.organization_name AS current_organization, approval.occurred_at AS approved_at
		FROM business_cases f
		JOIN business_cases r ON r.filing_id=f.id AND r.kind='report' AND r.status='approved'
			AND r.discarded_at IS NULL
		LEFT JOIN LATERAL (
			SELECT occurred_at FROM case_review_events
			WHERE case_id=r.id AND action='approve' AND to_status='approved' ORDER BY version DESC LIMIT 1
		) approval ON true
		WHERE ` + operatorReleasedReportScope + ` AND r.id=$3
	), released AS (
		SELECT DISTINCT ON (a.kind) a.id,a.kind,a.file_name,a.size_bytes,a.uploaded_at,a.file_key
		FROM available r JOIN case_attachments a ON a.case_id=r.id
		WHERE a.kind IN ('report_file','remediation_plan') AND (r.approved_at IS NULL OR a.uploaded_at<=r.approved_at)
		ORDER BY a.kind,a.uploaded_at DESC,a.id DESC
	)`

const operatorApprovedPlan = `FROM remediation_plans p WHERE p.report_id=r.id AND p.status='approved'
	AND COALESCE(p.frozen_organization_name,r.current_organization)=` + operatorAssessmentScope + `
	ORDER BY p.submitted_at DESC NULLS LAST,p.id DESC LIMIT 1`

const operatorMaterialsList = operatorMaterialsSource + `
	SELECT COALESCE((
		SELECT jsonb_agg(jsonb_build_object('id',id,'kind',kind,'fileName',file_name,
			'size',size_bytes,'uploadedAt',uploaded_at) ORDER BY (kind='report_file') DESC)
		FROM released
	),'[]'::jsonb), (
		SELECT jsonb_build_object('deadline',p.deadline,'items',p.risks)
		` + operatorApprovedPlan + `
	) FROM available r`

const operatorMaterialDownload = operatorMaterialsSource + `
	SELECT id::text,kind,file_name,size_bytes,uploaded_at,file_key FROM released WHERE id=$4`

// registerOperatorMaterials 挂载对单份终审报告的材料查看和受控下载。
// 参数 mux 为服务使用的同源路由表。
// 返回值 无。
func (s *Service) registerOperatorMaterials(mux *http.ServeMux) {
	const path = operatorArchiveEndpoint + "/{id}/reports/{reportId}/materials"
	mux.HandleFunc("GET "+path, s.operatorMaterialsHTTP)
	mux.HandleFunc("GET "+path+"/{attachmentId}", s.operatorDownloadHTTP)
}

// parseOperatorReportScope 核对单份归档报告的备案、报告和所选运营单位。
// 参数 r 携带路径标识和唯一允许的运营单位参数。
// 返回值 规范业务范围、找不到合法标识或查询输入错误。
func parseOperatorReportScope(r *http.Request) (operatorReportScope, error) {
	filing, filingOK := parseHistoryUUID(r.PathValue("id"))
	report, reportOK := parseHistoryUUID(r.PathValue("reportId"))
	if !filingOK || !reportOK {
		return operatorReportScope{}, pgx.ErrNoRows
	}
	_, organization, err := operatorAssessmentQuery(r.URL.RawQuery)
	return operatorReportScope{organization, filing, report}, err
}

// operatorMaterialsHTTP 读取当期已终审报告开放的文件和已审批整改项目。
// 参数 w、r 为业务响应及携带单位范围和报告标识的读取请求。
// 返回值 无。
func (s *Service) operatorMaterialsHTTP(w http.ResponseWriter, r *http.Request) {
	scope, err := parseOperatorReportScope(r)
	if err != nil {
		respondHistory(w, nil, err)
		return
	}
	var view operatorReportMaterials
	err = s.db.QueryRow(r.Context(), operatorMaterialsList, scope.organization, scope.filing, scope.report).
		Scan(&view.Items, &view.Requirements)
	respondHistory(w, view, err)
}

// operatorDownloadHTTP 按当前归属、当期报告和公布附件重新核验每次下载。
// 参数 w、r 为下载响应和携带当前单位及材料标识的请求。
// 返回值 无，下载由现有公共附件存储发送。
func (s *Service) operatorDownloadHTTP(w http.ResponseWriter, r *http.Request) {
	scope, err := parseOperatorReportScope(r)
	if err != nil {
		respondHistory(w, nil, err)
		return
	}
	attachment, ok := parseHistoryUUID(r.PathValue("attachmentId"))
	if !ok || s.files == nil {
		respondHistory(w, nil, pgx.ErrNoRows)
		return
	}
	var file cases.Attachment
	var key string
	err = s.db.QueryRow(r.Context(), operatorMaterialDownload, scope.organization, scope.filing,
		scope.report, attachment).Scan(&file.ID, &file.Kind, &file.FileName, &file.Size, &file.UploadedAt, &key)
	if err == nil {
		err = s.files.ServeBusinessAttachment(w, r, file, key)
	}
	if errors.Is(err, cases.ErrNotFound) {
		err = pgx.ErrNoRows
	}
	if err != nil {
		respondHistory(w, nil, err)
	}
}
