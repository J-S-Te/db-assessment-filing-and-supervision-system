package archives

import (
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgtype"
)

var errRegistrationConflict = errors.New("archive registration conflict")
var domainLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type assetMetadata struct {
	Label       string `json:"label"`
	Source      string `json:"source"`
	Description string `json:"description"`
	OccurredOn  string `json:"occurredOn"`
}

type assetCreate struct {
	Kind            string `json:"kind"`
	Identifier      string `json:"identifier"`
	RelatedEndpoint string `json:"relatedEndpoint"`
	SupplierID      string `json:"supplierId"`
	assetMetadata
}

type assetUpdate struct {
	assetMetadata
	Active     *bool   `json:"active"`
	SupplierID *string `json:"supplierId"`
}

type systemAsset struct {
	rowOrder
	FilingID        string `json:"filingId"`
	Kind            string `json:"kind"`
	Identifier      string `json:"identifier"`
	RelatedEndpoint string `json:"relatedEndpoint"`
	assetMetadata
	Active       bool      `json:"active"`
	Version      int64     `json:"version"`
	UpdatedAt    time.Time `json:"updatedAt"`
	SupplierID   string    `json:"supplierId,omitempty"`
	SupplierName string    `json:"supplierName,omitempty"`
}

// validateAssetCreate 统一地址与域名写法，并核对各类登记的身份和凭据。
// 参数 input 为新登记请求，由校验过程归一化。
// 返回值 非法类型、标识或登记凭据错误。
func validateAssetCreate(input *assetCreate) error {
	input.Identifier = strings.TrimSpace(input.Identifier)
	input.RelatedEndpoint = strings.TrimSpace(input.RelatedEndpoint)
	if err := resolveAssetIdentifier(input); err != nil {
		return err
	}
	if !registrationText(input.Identifier, 253, true) || !registrationText(input.RelatedEndpoint, 253, false) {
		return errInvalid
	}
	if input.Kind != "topology" && input.RelatedEndpoint != "" {
		return errInvalid
	}
	switch input.Kind {
	case "ip":
		address, err := netip.ParseAddr(input.Identifier)
		if err != nil || address.Zone() != "" {
			return errInvalid
		}
		input.Identifier = address.Unmap().String()
	case "domain":
		input.Identifier = strings.TrimSuffix(strings.ToLower(input.Identifier), ".")
		if _, err := netip.ParseAddr(input.Identifier); err == nil || !strings.Contains(input.Identifier, ".") {
			return errInvalid
		}
		for _, label := range strings.Split(input.Identifier, ".") {
			if !domainLabel.MatchString(label) {
				return errInvalid
			}
		}
	case "topology":
		if input.RelatedEndpoint == "" || input.Identifier == input.RelatedEndpoint {
			return errInvalid
		}
	case "supplier", "security_event", "cloud", "big_data":
	default:
		return errInvalid
	}
	return validateAssetMetadata(&input.assetMetadata, input.Kind)
}

// resolveAssetIdentifier 在主档关联登记中使用主档标识建立稳定的备案内身份。
// 参数 input 为登记类型、供应商主档标识及可选登记标识。
// 返回值 独立文字登记和有效供应商关联返回空错误，其他组合返回参数错误。
func resolveAssetIdentifier(input *assetCreate) error {
	if input.SupplierID == "" {
		return nil
	}
	id, err := parseArchiveID(input.SupplierID)
	if err != nil || input.Kind != "supplier" {
		return errInvalid
	}
	input.SupplierID = id.String()
	if input.Identifier != "" && input.Identifier != input.SupplierID {
		return errInvalid
	}
	input.Identifier = input.SupplierID
	return nil
}

// validateAssetMetadata 核对来源、描述以及安全事件的实际发生日期。
// 参数 input 为可编辑登记字段，kind 为已有或新建条目的类别。
// 返回值 非法元数据错误。
func validateAssetMetadata(input *assetMetadata, kind string) error {
	input.Label = strings.TrimSpace(input.Label)
	input.Source = strings.TrimSpace(input.Source)
	input.Description = strings.TrimSpace(input.Description)
	if !registrationText(input.Source, 300, true) || !registrationText(input.Label, 200, false) ||
		!boundedName(input.Description, 2000) {
		return errInvalid
	}
	if kind != "security_event" {
		if input.OccurredOn != "" {
			return errInvalid
		}
		return nil
	}
	_, err := time.Parse(time.DateOnly, input.OccurredOn)
	if err != nil || strings.HasPrefix(input.OccurredOn, "0000") {
		return errInvalid
	}
	return nil
}

// registrationText 校验可作为标识或来源的单行登记内容。
// 参数 value 为已清理首尾空白的文字，maximum 为字符上限，required 表示是否必填。
// 返回值 文字符合登记要求时为真。
func registrationText(value string, maximum int, required bool) bool {
	return (!required || value != "") && boundedName(value, maximum) && !strings.ContainsFunc(value, unicode.IsControl)
}

// optionalArchiveID 读取可空关联标识。
// 参数 raw 为提交的 UUID 字符串或空值。
// 返回值 解析后的可空 UUID，或格式校验错误。
func optionalArchiveID(raw string) (pgtype.UUID, error) {
	if raw == "" {
		return pgtype.UUID{}, nil
	}
	return parseArchiveID(raw)
}
