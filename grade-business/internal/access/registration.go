package access

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"grade-business/internal/directory"
)

const registrationRoot = "/api/v1/operator-registrations"
const registrationSubmitPattern = "POST " + authRoot + "/operator-registrations"
const registrationStatusPattern = registrationSubmitPattern + "/status"
const registrationListPattern = "GET " + registrationRoot
const registrationReviewPattern = "POST " + registrationRoot + "/{id}/review-actions"
const registrationPermission = "admin.sys.user.audit"

const creditAlphabet = "0123456789ABCDEFGHJKLMNPQRTUWXY"

var (
	registrationPhone       = regexp.MustCompile(`^1[3-9][0-9]{9}$`)
	registrationArea        = regexp.MustCompile(`^[0-9]{6}$`)
	errRegistrationArea     = errors.New("所属辖区不在当前办理范围")
	errRegistrationUnit     = errors.New("单位名录与申请不一致或不可用")
	errRegistrationAccount  = errors.New("单位已有账号")
	errRegistrationUsername = errors.New("登录账号已被使用")
	errRegistrationPending  = errors.New("已存在在审或已通过申请")
	errRegistrationDaily    = errors.New("此统一社会信用代码今日申请次数已用完")
	errRegistrationReviewed = errors.New("申请已完成审核")
)

type registrationInput struct {
	UnitName    string `json:"unitName"`
	CreditCode  string `json:"creditCode"`
	ContactName string `json:"contactName"`
	Phone       string `json:"phone"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}

type registrationResult struct {
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	ReviewedAt *time.Time `json:"reviewedAt,omitempty"`
	Reason     string     `json:"reason,omitempty"`
}

type registrationItem struct {
	ID          string `json:"id"`
	UnitName    string `json:"unitName"`
	CreditCode  string `json:"creditCode"`
	ContactName string `json:"contactName"`
	Phone       string `json:"phone"`
	Username    string `json:"username"`
	AreaCode    string `json:"areaCode"`
	District    string `json:"district"`
	Reviewer    string `json:"reviewer,omitempty"`
	ReviewRole  Role   `json:"reviewerRole,omitempty"`
	SubjectID   string `json:"subjectId,omitempty"`
	registrationResult
	sequence int64
}

type registrationDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	Verified bool   `json:"verified"`
}

// normalizeRegistration 统一注册字段格式并校验申请资料。
// 参数 input 为申请人填写的单位、联系人和登录凭据。
// 返回值 必填字段、信用代码及密码均有效时为真。
func normalizeRegistration(input *registrationInput) bool {
	input.UnitName = strings.TrimSpace(input.UnitName)
	input.CreditCode = strings.ToUpper(strings.TrimSpace(input.CreditCode))
	input.ContactName = strings.TrimSpace(input.ContactName)
	input.Phone = strings.TrimSpace(input.Phone)
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	return registrationText(input.UnitName, 200, false) && registrationText(input.ContactName, 100, false) &&
		validCreditCode(input.CreditCode) && registrationPhone.MatchString(input.Phone) &&
		usernamePattern.MatchString(input.Username) && validPassword(input.Password) &&
		!strings.ContainsRune(input.Password, 0)
}

// registrationText 检查申请和审核文字可安全存储及展示。
// 参数 value 为已去首尾空白的内容，limit 为字符上限，optional 表示是否允许为空。
// 返回值 内容满足长度和字符约束时为真。
func registrationText(value string, limit int, optional bool) bool {
	return (optional || value != "") && utf8.RuneCountInString(value) <= limit &&
		strings.IndexFunc(value, unicode.IsControl) == -1
}

// validCreditCode 按 GB 32100 校验统一社会信用代码和登记机关行政区划段。
// 参数 code 为规范化后的 18 位信用代码。
// 返回值 代码字符、区划段及校验位正确时为真。
func validCreditCode(code string) bool {
	if len(code) != 18 || !registrationArea.MatchString(code[2:8]) {
		return false
	}
	total, weight := 0, 1
	for i := range 17 {
		digit := strings.IndexByte(creditAlphabet, code[i])
		if digit < 0 {
			return false
		}
		total += digit * weight
		weight = weight * 3 % 31
	}
	return code[17] == creditAlphabet[(31-total%31)%31]
}

// lookupFingerprint 解码申请回执密钥并生成用于数据库查询的摘要。
// 参数 code 为原样保存的私密查询码。
// 返回值 查询摘要以及编码是否合法。
func lookupFingerprint(code string) ([32]byte, bool) {
	if len(code) != 43 {
		return [32]byte{}, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(code)
	if err != nil || len(raw) != 32 {
		return [32]byte{}, false
	}
	return sha256.Sum256(raw), true
}

// registrationStoreError 将预期的数据库约束冲突转换为稳定业务错误。
// 参数 err 为注册事务执行错误。
// 返回值 可向调用方区分的冲突或原始错误。
func registrationStoreError(err error) error {
	if errors.Is(err, directory.ErrRegistrationUnitConflict) {
		return errRegistrationUnit
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return err
	}
	switch pgErr.ConstraintName {
	case "access_accounts_username_key":
		return errRegistrationUsername
	case "access_accounts_subject_id_key":
		return errRegistrationAccount
	case "operator_registrations_open_code":
		return errRegistrationPending
	case "directory_parties_kind_scope", "directory_parties_kind_name", "directory_parties_kind_code":
		return errRegistrationUnit
	default:
		return err
	}
}

// registrationFields 读取审批清单和办理结果共享的安全展示字段。
// 参数 item 为扫描目标，row 为查询行。
// 返回值 查询或扫描错误。
func registrationFields(item *registrationItem, row pgx.Row) error {
	err := row.Scan(&item.ID, &item.sequence, &item.UnitName, &item.CreditCode, &item.ContactName,
		&item.Phone, &item.Username, &item.AreaCode, &item.Status, &item.CreatedAt, &item.ReviewedAt,
		&item.Reason, &item.Reviewer, &item.ReviewRole, &item.SubjectID)
	item.District = districtNames[item.AreaCode]
	return err
}

const registrationColumns = `id::text,sequence,unit_name,credit_code,contact_name,phone,username,
	area_code,status,created_at,reviewed_at,reason,COALESCE(reviewer_name,''),
	COALESCE(reviewer_role,''),COALESCE(subject_id::text,'')`
