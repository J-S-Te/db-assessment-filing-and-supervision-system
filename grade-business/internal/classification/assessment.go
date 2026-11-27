package classification

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const Standard = "GB/T 22240-2020"

var ErrInvalid = errors.New("定级自评需要填写两类影响及事实依据，每类受侵害客体只能登记一次")

type Impact struct {
	Subject string `json:"subject"`
	Damage  string `json:"damage"`
}

type Input struct {
	Information        []Impact `json:"information"`
	Service            []Impact `json:"service"`
	Basis              string   `json:"basis"`
	CompetentAuthority string   `json:"competentAuthority,omitempty"`
	FilingAuthority    string   `json:"filingAuthority,omitempty"`
}

type Assessment struct {
	Input
	InformationLevel int    `json:"informationLevel"`
	ServiceLevel     int    `json:"serviceLevel"`
	SuggestedLevel   int    `json:"suggestedLevel"`
	ExpertReview     bool   `json:"expertReview"`
	Standard         string `json:"standard"`
}

var matrix = map[string][3]int{
	"rights":   {1, 2, 2},
	"public":   {2, 3, 4},
	"national": {3, 4, 5},
}

var damageIndex = map[string]int{"general": 0, "serious": 1, "severe": 2}

// Evaluate 根据 GB/T 22240-2020 表 2、表 3 核算初步定级建议。
// 参数 input 为申报人填写的业务信息和系统服务影响、事实依据及可选机关。
// 返回值 输出各维度等级、较高的建议等级及评审提示，事实无效时返回 ErrInvalid。
func Evaluate(input Input) (Assessment, error) {
	input.Basis = strings.TrimSpace(input.Basis)
	input.CompetentAuthority = strings.TrimSpace(input.CompetentAuthority)
	input.FilingAuthority = strings.TrimSpace(input.FilingAuthority)
	if input.Basis == "" || !validText(input.Basis, 2000) ||
		!validText(input.CompetentAuthority, 200) || !validText(input.FilingAuthority, 200) {
		return Assessment{}, ErrInvalid
	}
	information, err := impactLevel(input.Information)
	if err != nil {
		return Assessment{}, err
	}
	service, err := impactLevel(input.Service)
	if err != nil {
		return Assessment{}, err
	}
	suggestion := max(information, service)
	return Assessment{Input: input, InformationLevel: information, ServiceLevel: service,
		SuggestedLevel: suggestion, ExpertReview: suggestion >= 2, Standard: Standard}, nil
}

// impactLevel 依据损害矩阵选择一个维度中影响最高的客体等级。
// 参数 impacts 为该维度实际登记的客体及损害程度。
// 返回值 最高等级，缺少客体、重复或未知取值时返回 ErrInvalid。
func impactLevel(impacts []Impact) (int, error) {
	if len(impacts) == 0 || len(impacts) > len(matrix) {
		return 0, ErrInvalid
	}
	seen := make(map[string]bool, len(impacts))
	var level int
	for _, impact := range impacts {
		row, subjectOK := matrix[impact.Subject]
		index, damageOK := damageIndex[impact.Damage]
		if !subjectOK || !damageOK || seen[impact.Subject] {
			return 0, ErrInvalid
		}
		seen[impact.Subject] = true
		level = max(level, row[index])
	}
	return level, nil
}

// validText 限定事实说明和机关字段的有效 UTF-8 长度。
// 参数 text 为录入文本，limit 为最大字符数。
// 返回值 文本符合存储约束时为真。
func validText(text string, limit int) bool {
	return utf8.ValidString(text) && utf8.RuneCountInString(text) <= limit && !strings.ContainsRune(text, 0)
}
