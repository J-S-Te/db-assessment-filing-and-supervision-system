package implementations

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/httpx"
	"grade-business/internal/reviewinbox"
)

const operatorPath = "/api/v1/operator/implementations"
const operatorApplicantPrefix = "本地运营者："

type implementationOrganizationKey struct{}

// RegisterOperatorRoutes 注册运营者实施报备处理入口。
// 参数 mux 为应用业务路由。
// 返回值 无。
func (s *Service) RegisterOperatorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+operatorPath, operatorEndpoint(reportEndpoint(s.listHTTP, false), listFilterKeys...))
	mux.HandleFunc("POST "+operatorPath, operatorEndpoint(reportEndpoint(s.createHTTP, false)))
	mux.HandleFunc("GET "+operatorPath+"/filing-options", operatorEndpoint(reportEndpoint(s.choicesHTTP, false), "q", "filingId"))
	mux.HandleFunc("GET "+operatorPath+"/quota", operatorEndpoint(reportEndpoint(s.quotaHTTP, false), "filingId"))
	mux.HandleFunc("GET "+operatorPath+"/{id}", operatorEndpoint(reportEndpoint(s.detailHTTP, false)))
	mux.HandleFunc("GET "+operatorPath+"/{id}/precheck", operatorEndpoint(reportEndpoint(s.precheckHTTP, false), "expectedVersion"))
	mux.HandleFunc("PATCH "+operatorPath+"/{id}", operatorEndpoint(reportEndpoint(s.editHTTP, false)))
	mux.HandleFunc("POST "+operatorPath+"/{id}/submit", operatorEndpoint(s.operatorActionHTTP("submit")))
	mux.HandleFunc("POST "+operatorPath+"/{id}/withdraw", operatorEndpoint(s.operatorActionHTTP("withdraw")))
	mux.HandleFunc("GET "+operatorPath+"/{id}/review-history", operatorEndpoint(s.historyHTTP, "beforeVersion", "limit"))
}

// operatorEndpoint 验证本地单位范围并通过请求上下文交给共用处理器。
// 参数 handle 为业务端点，filters 为该端点允许的请求筛选字段。
// 返回值 可注册的运营者 HTTP 处理函数。
func operatorEndpoint(handle http.HandlerFunc, filters ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		checked, organization, err := scopedImplementationRequest(r, "organization", filters)
		if err != nil {
			writeError(w, errInvalid)
			return
		}
		ctx := context.WithValue(checked.Context(), implementationOrganizationKey{}, organization)
		handle(w, checked.WithContext(ctx))
	}
}

// operatorOrganization 读取运营者入口校验过的单位范围。
// 参数 ctx 为当前业务请求上下文。
// 返回值 运营者名称，监管或机构请求时为空。
func operatorOrganization(ctx context.Context) string {
	value, _ := ctx.Value(implementationOrganizationKey{}).(string)
	return value
}

// operatorActionHTTP 将版本化的运营者操作交给现有实施状态机。
// 参数 action 为该路由固定的提交或撤销命令。
// 返回值 仅接受报备版本的操作端点。
func (s *Service) operatorActionHTTP(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, valid := requestID(w, r)
		if !valid {
			return
		}
		var input struct {
			ExpectedVersion int64 `json:"expectedVersion"`
		}
		if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
			writeError(w, errInvalid)
			return
		}
		result, err := s.execute(r.Context(), id, "", command{Action: action, ExpectedVersion: input.ExpectedVersion})
		respond(w, http.StatusOK, result, err)
	}
}

// operatorInitiated 判断报备是否由本地运营者申请流程创建。
// 参数 record 为已加载报备及其创建者标记。
// 返回值 运营者发起时为真。
func operatorInitiated(record storedRecord) bool {
	return strings.HasPrefix(record.CreatedBy, operatorApplicantPrefix)
}

// canOperatorEdit 判断本单位发起人是否可以编辑或提交当前报备。
// 参数 record 为已通过当前单位范围校验的报备，operator 为服务端发起人标记。
// 返回值 本人发起且处于草稿或退回状态时为真。
func canOperatorEdit(record storedRecord, operator string) bool {
	return record.CreatedBy == operator && (record.Status == draft || record.Status == returned)
}

// operatorActions 列出运营者可以办理的动作。
// 参数 record 为本单位报备，operator 为服务端发起人标记，now 为业务时刻。
// 返回值 当前可用的操作代码。
func operatorActions(record storedRecord, operator string, now time.Time) []string {
	return applicantActions(record, operator, now, canOperatorEdit(record, operator))
}

// validateOperatorChange 核对换机构复测申请的类型及拟换入机构。
// 参数 kind 与 target 为报备类型和拟委托机构，filing 为锁定的当前备案。
// 返回值 类型或机构不符合申请条件时的领域错误。
func validateOperatorChange(kind, target string, filing choice) error {
	if kind != change {
		return errInvalid
	}
	if target == "" || strings.EqualFold(target, strings.TrimSpace(filing.AssessmentOrg)) {
		return errTarget
	}
	return nil
}

// operatorEditableFiling 校验申请人补正或修改草稿时锁定的有效备案。
// 参数 ctx 控制查询，tx 持有报备写锁，record 是当前报备，operator 标识申请人，item 为变更字段。
// 返回值 可用于回填的备案快照，或领域校验错误。
func operatorEditableFiling(ctx context.Context, tx pgx.Tx, record storedRecord, operator string,
	item *input) (choice, error) {
	if !canOperatorEdit(record, operator) || item.FilingID != record.FilingID || item.Type != record.Type {
		return choice{}, errConflict
	}
	item.AssessmentOrg = fallbackOrg(item.AssessmentOrg, record.AssessmentOrg)
	if record.Status == returned && item.AssessmentOrg != record.AssessmentOrg {
		return choice{}, errConflict
	}
	filing, err := lockActiveFiling(ctx, tx, record.FilingID, "")
	if err != nil {
		return choice{}, err
	}
	return filing, validateOperatorChange(item.Type, item.AssessmentOrg, filing)
}

// checkOperatorImplementationAction 将提交与撤销范围限定在发起人的报备。
// 参数 record 为已锁定报备，operator 为当前运营者经办标记，action 为命令。
// 返回值 业务范围冲突或未知命令错误。
func checkOperatorImplementationAction(record storedRecord, operator, action string) error {
	switch action {
	case "submit":
		if !canOperatorEdit(record, operator) {
			return errConflict
		}
	case "withdraw":
		// 撤销的发起人、受理状态和时限由共用状态机校验。
	default:
		return errInvalid
	}
	return nil
}

// deliverOperatorImplementationReview 把退回或通过结论送达实施报备对应的当前备案单位。
// 参数 ctx 控制查询及送达，tx 为审核事务，record 为审核前记录，result、comment 为审核结论。
// 返回值 当前归属查询或通知写入错误。
func deliverOperatorImplementationReview(ctx context.Context, tx pgx.Tx, record storedRecord,
	result mutation, comment string) error {
	notice := reviewinbox.Notice{
		Audience: "operator", Resource: "implementations", ResourceID: result.ID,
		State: result.Status, Version: result.Version, Reference: record.FilingNumber,
		Subject: record.SystemName, Comment: comment,
	}
	if err := tx.QueryRow(ctx, `SELECT organization_name FROM business_cases
	  WHERE id=$1 AND kind='filing' FOR SHARE`, record.FilingID).Scan(&notice.Recipient); err != nil {
		return err
	}
	return reviewinbox.Deliver(ctx, tx, notice)
}
