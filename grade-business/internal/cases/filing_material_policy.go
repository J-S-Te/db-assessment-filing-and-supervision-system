package cases

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/filingpolicy"
	"grade-business/internal/httpx"
)

type policyMaterialMissing struct{ label string }

// Error 返回办理人员可据以补传的规则名称。
// 参数 无。
// 返回值 对应缺件的简短业务提示。
func (err policyMaterialMissing) Error() string { return "请补传材料「" + err.label + "」。" }

// registerFilingMaterialPolicy 提供监管端维护当前备案材料规则的查询及保存接口。
// 参数 mux 为应用路由。
// 返回值 无。
func (s *Service) registerFilingMaterialPolicy(mux *http.ServeMux) {
	const path = "/api/v1/filing-material-policy"
	mux.HandleFunc("GET "+path, s.getFilingMaterialPolicy)
	mux.HandleFunc("PUT "+path, s.putFilingMaterialPolicy)
}

// getFilingMaterialPolicy 返回当前监管备案材料配置。
// 参数 w、r 为 HTTP 响应与请求。
// 返回值 无。
func (s *Service) getFilingMaterialPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := filingpolicy.Load(r.Context(), s.db, false)
	respond(w, http.StatusOK, policy, err)
}

// putFilingMaterialPolicy 按已读版本保存规则，避免覆盖他人的配置。
// 参数 w、r 为响应及携带规则、版本的请求。
// 返回值 无。
func (s *Service) putFilingMaterialPolicy(w http.ResponseWriter, r *http.Request) {
	var form struct {
		versionInput
		Rules *[]filingpolicy.Rule `json:"rules"`
	}
	if httpx.Decode(w, r, &form) != nil || form.Rules == nil {
		respondError(w, ErrInvalid)
		return
	}
	result, err := filingpolicy.Replace(r.Context(), s.db, form.ExpectedVersion, *form.Rules)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrConflict
	}
	respond(w, http.StatusOK, result, err)
}

// checkConfiguredMaterials 在提交事务中校验当前规则命中的真实文件。
// 参数 ctx、tx 为办理上下文和事务，stage、id 指定备案事项。
// 返回值 无缺件时为空，否则返回具体缺失规则或查询错误。
func checkConfiguredMaterials(ctx context.Context, tx pgx.Tx, stage, id string) error {
	policy, err := filingpolicy.Load(ctx, tx, true)
	if err != nil {
		return err
	}
	rules := policy.For(stage)
	if len(rules) == 0 {
		return nil
	}
	materials, err := readPolicyMaterials(ctx, tx, stage, id)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if filingpolicy.Match(rule, materials) == "" {
			return policyMaterialMissing{label: rule.Label}
		}
	}
	return nil
}

// readPolicyMaterials 获取当前申报人可用于提交的附件名称与ID。
// 参数 ctx、tx 为办理上下文和事务，stage、id 为提交类型及记录。
// 返回值 可核对的真实附件或查询错误。
func readPolicyMaterials(ctx context.Context, tx pgx.Tx, stage, id string) ([]filingpolicy.Material, error) {
	query := `SELECT id::text,kind,file_name FROM filing_application_attachments
		WHERE application_id=$1 ORDER BY uploaded_at,id`
	args := []any{id}
	if stage == filingpolicy.Initial {
		query = `SELECT a.id::text,a.kind,a.file_name FROM case_attachments a
			JOIN business_cases c ON c.id=a.case_id
			WHERE a.case_id=$1 AND ($2='' OR COALESCE(a.merge_history_scope,c.organization_name)=$2)
			ORDER BY a.uploaded_at,a.id`
		args = append(args, operatorFilingScope(ctx))
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[filingpolicy.Material])
}
