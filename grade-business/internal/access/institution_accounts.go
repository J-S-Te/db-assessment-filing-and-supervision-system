package access

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"grade-business/internal/httpx"
)

const institutionAccountPattern = "POST /api/v1/institution-accounts"

type institutionAccountInput struct {
	InstitutionID string `json:"institutionId"`
	Username      string `json:"username"`
	Name          string `json:"name"`
}

type institutionAccountReceipt struct {
	ID                string `json:"id"`
	Username          string `json:"username"`
	TemporaryPassword string `json:"temporaryPassword"`
}

// createInstitutionAccount 为在册机构开通唯一账号，并仅在本次响应中交接临时口令。
// 参数 w、r 为 HTTP 响应及系统管理员发送的开户请求。
// 返回值 无。
func (s *Service) createInstitutionAccount(w http.ResponseWriter, r *http.Request) {
	var input institutionAccountInput
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil ||
		strings.IndexFunc(input.Name, unicode.IsControl) >= 0 {
		httpx.Error(w, 400, "invalid_institution_account", "请核对机构、账号和负责人姓名。")
		return
	}
	actor, _ := Current(r.Context())
	password, passwordErr := temporaryPassword()
	if passwordErr != nil {
		slog.Error("机构账号临时口令生成失败", "operatorId", actor.ID)
		httpx.Error(w, 503, "institution_account_unavailable", "暂时无法开通机构账号，请稍后重试。")
		return
	}
	account := AccountInput{
		Role: OrgAdmin, SubjectID: input.InstitutionID, Name: input.Name,
		Username: strings.ToLower(strings.TrimSpace(input.Username)), Password: password,
	}
	id, err := provisionAccount(r.Context(), s.db, account, actor)
	switch {
	case errors.Is(err, errAccountInput):
		httpx.Error(w, 400, "invalid_institution_account", "请核对机构、账号和负责人姓名。")
	case errors.Is(err, errProvision):
		httpx.Error(w, 409, "institution_account_conflict", "账号已被占用、机构已有账号或机构资质无效。")
	case err != nil:
		slog.Error("机构账号开通失败", "operatorId", actor.ID)
		httpx.Error(w, 503, "institution_account_unavailable", "暂时无法开通机构账号，请稍后重试。")
	default:
		httpx.JSON(w, 201, institutionAccountReceipt{ID: id, Username: account.Username, TemporaryPassword: account.Password})
	}
}
