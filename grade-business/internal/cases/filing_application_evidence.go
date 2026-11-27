package cases

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

const (
	changeApplicationStatement = "change_statement"
	cancelApplicationStatement = "cancellation_statement"
)

var (
	errApplicationMaterials = errors.New("filing application explanation required")
	errApplicationRetest    = errors.New("approved reassessment implementation required")
)

const unusedRetestScope = `i.filing_id=$1 AND i.assessment_org=$2 AND i.report_type='change'
	AND NOT EXISTS (SELECT 1 FROM filing_applications used WHERE used.implementation_id=i.id AND used.status='approved')`

type applicationRetest struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// requiredApplicationMaterial 返回变更、注销所需的业务说明材料类别。
// 参数 kind 为申请类型。
// 返回值 当前类型的必传说明类别。
func requiredApplicationMaterial(kind string) string {
	if kind == applicationCancel {
		return cancelApplicationStatement
	}
	return changeApplicationStatement
}

// checkApplicationEvidence 在提交或通过前校验必传材料，并为换机构复测锁定报备依据。
// 参数 ctx、tx 为操作上下文及已锁主备案的事务，id、app 和 action 为申请及操作。
// 返回值 满足办理条件时为 nil，否则为材料、复测前置或数据库错误。
func checkApplicationEvidence(ctx context.Context, tx pgx.Tx, id string, app *applicationState, action string) error {
	if action != submit && action != approve {
		return nil
	}
	if app.operatorOrigin || applicationOrganization(ctx) != "" {
		var found bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM filing_application_attachments
			WHERE application_id=$1 AND kind=$2)`, id, requiredApplicationMaterial(app.kind)).Scan(&found); err != nil {
			return err
		}
		if !found {
			return errApplicationMaterials
		}
		if action == submit {
			if err := checkConfiguredMaterials(ctx, tx, app.kind, id); err != nil {
				return err
			}
		}
	}
	if app.kind != applicationChange || app.before.AssessmentOrg == app.after.AssessmentOrg {
		return nil
	}
	if action == approve && app.operatorOrigin && app.implementationID == nil {
		return errApplicationRetest
	}
	return bindApplicationRetest(ctx, tx, app, action == submit)
}

// bindApplicationRetest 选择或复核同备案、目标机构的已通过换机构实施报备。
// 参数 ctx、tx 为查询上下文及持锁事务，app 为申请，reselect 表示本次提交重新确定依据。
// 返回值 已锁定并回填依据时为 nil，依据缺失或不再有效时返回前置条件错误。
func bindApplicationRetest(ctx context.Context, tx pgx.Tx, app *applicationState, reselect bool) error {
	preferred := app.implementationID
	if reselect {
		preferred = nil
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT i.id::text FROM implementation_reports i WHERE `+unusedRetestScope+`
		AND i.status='approved' AND ($3::uuid IS NULL OR i.id=$3)
		ORDER BY i.accepted_at DESC,i.created_at DESC,i.id DESC LIMIT 1 FOR SHARE OF i`,
		app.filingID, app.after.AssessmentOrg, preferred).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errApplicationRetest
	}
	if err == nil {
		app.implementationID = &id
	}
	return err
}

// availableApplicationRetest 返回本次换机构可继续办理的报备进度，优先已通过结果。
// 参数 ctx 为已核对申请及当前备案归属的请求，filingID 和 target 为备案和目标测评机构。
// 返回值 未使用且可继续使用的实际报备，或查询错误。
func (s *Service) availableApplicationRetest(ctx context.Context, filingID, target string) (*applicationRetest, error) {
	var item applicationRetest
	err := s.db.QueryRow(ctx, `SELECT i.id::text,i.status FROM implementation_reports i WHERE `+unusedRetestScope+`
		AND i.status IN ('approved','draft','submitted','pending_initial','pending_final','pending_supervision','returned')
		AND EXISTS (SELECT 1 FROM business_cases f WHERE f.id=i.filing_id AND f.kind='filing'
		  AND f.organization_name=$3 AND f.status='approved' AND f.cancelled_at IS NULL)
		ORDER BY (i.status='approved') DESC,
		  CASE WHEN i.status='approved' THEN i.accepted_at ELSE COALESCE(i.submitted_at,i.created_at) END DESC,
		  i.created_at DESC,i.id DESC LIMIT 1`, filingID, target, applicationOrganization(ctx)).Scan(&item.ID, &item.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}
