package publications

import (
	"net/url"
	"slices"
	"strings"
	"time"
)

type recipient struct {
	Audience  string `json:"audience"`
	Recipient string `json:"recipient"`
}

type recipientOption struct {
	recipient
	DisplayName string `json:"displayName,omitempty"`
}

type readState struct {
	ReadAt         *time.Time `json:"readAt,omitempty"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
}

type delivery struct {
	recipientOption
	DeliveredAt *time.Time `json:"deliveredAt"`
	readState
	sequence int64
}

type receiptInput struct {
	ExpectedVersion int64 `json:"expectedVersion"`
}

type deliveryFilter struct {
	version, before int64
	audience, query string
}

type deliveryCounts struct {
	Delivered    int64 `json:"delivered"`
	Read         int64 `json:"read"`
	Acknowledged int64 `json:"acknowledged"`
}

type receiptPage struct {
	Version     int64                `json:"version"`
	Counts      deliveryCounts       `json:"counts"`
	Attachments []documentAttachment `json:"attachments,omitempty"`
	page[delivery]
}

// validRecipientAudience 判断门户角色是否可以成为明确收件方。
// 参数 audience 为策略或查询中的角色名称。
// 返回值 运营者或测评机构角色返回真。
func validRecipientAudience(audience string) bool {
	return audience == operatorAudience || audience == institutionAudience
}

// normalizePolicy 校验发布范围变更，并将明确目标以角色和规范名称去重。
// 参数 item 为拟保存的草稿或修订内容。
// 返回值 策略结构有效时为真，名单的业务资格另行查验。
func normalizePolicy(item *content) bool {
	if item.Audience == nil {
		return len(item.Targets) == 0
	}
	switch *item.Audience {
	case allAudience, operatorAudience, institutionAudience:
		return len(item.Targets) == 0
	case selectedAudience:
		unique := make(map[recipient]struct{}, len(item.Targets))
		targets := item.Targets[:0]
		for _, member := range item.Targets {
			member.Recipient = strings.TrimSpace(member.Recipient)
			if !validRecipientAudience(member.Audience) || !boundedText(member.Recipient, recipientLimit, false) {
				return false
			}
			if _, found := unique[member]; !found {
				unique[member] = struct{}{}
				targets = append(targets, member)
			}
		}
		item.Targets = targets
		return len(targets) > 0
	default:
		return false
	}
}

// publicationQuery 解析单值查询参数并只允许当前接口声明的字段。
// 参数 raw 为原始查询，fields 为接口允许的参数名。
// 返回值 解析后的参数，或非法查询错误。
func publicationQuery(raw string, fields ...string) (url.Values, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, errInvalid
	}
	for field, entries := range values {
		if len(entries) != 1 || !slices.Contains(fields, field) {
			return nil, errInvalid
		}
	}
	return values, nil
}

// scopeField 给出指定门户角色的本地收件方参数名。
// 参数 audience 为已注册的门户角色。
// 返回值 运营单位或测评机构的参数名。
func scopeField(audience string) string {
	if audience == institutionAudience {
		return "institution"
	}
	return "organization"
}

// queryScope 验证门户查阅或写入操作的本地名称范围。
// 参数 values 为已核对查询参数，audience 为当前门户角色，required 表示必须选择单位。
// 返回值 门户范围，或名称无效、缺失导致的参数错误。
func queryScope(values url.Values, audience string, required bool) (recipient, error) {
	field := scopeField(audience)
	name := strings.TrimSpace(values.Get(field))
	if (required || values.Has(field)) && !boundedText(name, recipientLimit, false) {
		return recipient{}, errInvalid
	}
	return recipient{Audience: audience, Recipient: name}, nil
}

// parseScope 读取只接收收件范围的门户接口查询。
// 参数 raw 为原始查询，audience 为门户角色，required 表示名称必填。
// 返回值 规范化收件范围，或参数错误。
func parseScope(raw, audience string, required bool) (recipient, error) {
	values, err := publicationQuery(raw, scopeField(audience))
	if err != nil {
		return recipient{}, err
	}
	return queryScope(values, audience, required)
}

// parseRecipientSearch 解析监管收件名册和回执查询的角色与关键词。
// 参数 values 为已经核对的查询参数，required 表示角色必填。
// 返回值 角色、关键词及校验错误。
func parseRecipientSearch(values url.Values, required bool) (string, string, error) {
	audience := values.Get("audience")
	query := strings.TrimSpace(values.Get("q"))
	if ((required || values.Has("audience")) && !validRecipientAudience(audience)) ||
		(query != "" && !boundedText(query, recipientLimit, false)) {
		return "", "", errInvalid
	}
	return audience, query, nil
}

// parseDeliveryFilter 解析回执筛选与绑定发布版本的分页游标。
// 参数 raw 为监管回执接口的原始查询。
// 返回值 指定回执版本、分页和过滤条件，或参数错误。
func parseDeliveryFilter(raw string) (deliveryFilter, error) {
	values, err := publicationQuery(raw, "audience", "q", "version", "before")
	if err != nil {
		return deliveryFilter{}, err
	}
	audience, query, err := parseRecipientSearch(values, false)
	filter := deliveryFilter{audience: audience, query: query}
	if err != nil {
		return filter, err
	}
	if values.Has("version") {
		filter.version, err = positiveNumber(values.Get("version"))
		if err != nil {
			return filter, err
		}
	}
	if values.Has("before") {
		version, sequence, ok := strings.Cut(values.Get("before"), ":")
		number, versionErr := positiveNumber(version)
		if !ok || versionErr != nil || (filter.version != 0 && filter.version != number) {
			return filter, errInvalid
		}
		filter.version = number
		filter.before, err = positiveNumber(sequence)
	}
	return filter, err
}
