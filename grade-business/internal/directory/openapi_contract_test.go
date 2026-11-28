package directory

import (
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type directorySchema struct {
	Ref                  string              `yaml:"$ref"`
	Type                 string              `yaml:"type"`
	Required             []string            `yaml:"required"`
	Properties           map[string]struct{} `yaml:"properties"`
	AdditionalProperties *bool               `yaml:"additionalProperties"`
	MinProperties        int                 `yaml:"minProperties"`
}

type directoryContractBody struct {
	Ref     string `yaml:"$ref"`
	Content map[string]struct {
		Schema directorySchema `yaml:"schema"`
	} `yaml:"content"`
}

type directoryOperation struct {
	Request   directoryContractBody            `yaml:"requestBody"`
	Responses map[string]directoryContractBody `yaml:"responses"`
}

type directoryContract struct {
	Paths map[string]struct {
		Post  *directoryOperation `yaml:"post"`
		Patch *directoryOperation `yaml:"patch"`
	} `yaml:"paths"`
	Components struct {
		Schemas   map[string]directorySchema       `yaml:"schemas"`
		Responses map[string]directoryContractBody `yaml:"responses"`
	} `yaml:"components"`
}

// TestAssessorOpenAPIRejectsCode 核对两端登记与更新的闭合请求契约及真实拒绝响应。
// 参数 t 为无需 PostgreSQL 的名录契约测试上下文。
// 返回值 无。
func TestAssessorOpenAPIRejectsCode(t *testing.T) {
	doc := readDirectoryContract(t)
	mux := http.NewServeMux()
	Register(mux, nil)
	const id = "12345678-1234-1234-1234-123456789abc"
	for _, scoped := range []bool{false, true} {
		for _, method := range []string{http.MethodPost, http.MethodPatch} {
			pattern, path, valid := assessorWriteFixture(scoped, method, id)
			t.Run(method+" "+pattern, func(t *testing.T) {
				op := doc.operation(t, pattern, method)
				request := doc.schema(t, op.Request)
				if !request.acceptsFields(valid) {
					t.Fatalf("合法请求与 OpenAPI 不一致: %+v", request)
				}
				assertAssessorRequiredFields(t, request, valid, scoped, method)
				for key, field := range map[string]any{"code": "fixture", "Code": "", "CODE": nil} {
					submitted := maps.Clone(valid)
					submitted[key] = field
					if request.acceptsFields(submitted) {
						t.Fatalf("OpenAPI 接受了禁止字段 %q", key)
					}
					response := testJSON[map[string]any](t,
						testRequest(t, mux, method, path, submitted, http.StatusBadRequest))
					if response["code"] != "validation_error" {
						t.Fatalf("拒绝响应错误码不正确: %v", response)
					}
					doc.assertResponse(t, op, http.StatusBadRequest, response)
				}
			})
		}
	}
}

// assessorWriteFixture 构造两端人员写入所需的实际路径及合法参数。
// 参数 scoped 为是否机构端，method 为写入方法，id 为测试目录标识。
// 返回值 文档路径、请求路径及请求字段。
func assessorWriteFixture(scoped bool, method, id string) (string, string, map[string]any) {
	pattern := "/directory/people"
	if scoped {
		pattern = "/institution" + pattern
	}
	path := "/api/v1" + pattern
	body := map[string]any{"name": "契约测评师"}
	var record string
	if method == http.MethodPatch {
		pattern += "/{id}"
		path += "/" + id
		record = id
		body["expectedVersion"] = 1
	} else if !scoped {
		body["institutionId"] = id
	}
	if scoped {
		path = scopedDirectory("people", record, "测试机构")
	}
	return pattern, path, body
}

// assertAssessorRequiredFields 验证测评师请求缺少关键字段时不符合规范。
// 参数 t、schema 为测试及请求声明，valid 为合法输入，scoped、method 标记入口。
// 返回值 无，缺少必填数据仍通过验证时终止测试。
func assertAssessorRequiredFields(t *testing.T, schema directorySchema,
	valid map[string]any, scoped bool, method string,
) {
	t.Helper()
	required := []string{"name"}
	if method == http.MethodPatch {
		required = []string{"expectedVersion"}
	} else if !scoped {
		required = append(required, "institutionId")
	}
	for _, key := range required {
		missing := maps.Clone(valid)
		delete(missing, key)
		if !slices.Contains(schema.Required, key) || schema.acceptsFields(missing) {
			t.Errorf("OpenAPI 未要求字段 %q", key)
		}
	}
	if method == http.MethodPatch && schema.acceptsFields(map[string]any{"expectedVersion": 1}) {
		t.Error("OpenAPI 接受了未提交任何编辑字段的请求")
	}
}

// readDirectoryContract 从项目规范读取人员目录相关的 OpenAPI 结构。
// 参数 t 为报告依赖文件读取及解析失败的测试上下文。
// 返回值 解析后的实际 OpenAPI 文档。
func readDirectoryContract(t *testing.T) directoryContract {
	t.Helper()
	data, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc directoryContract
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// operation 取得具体人员写入操作，禁止回退到通用名录路径。
// 参数 t 为测试上下文，path、method 指定文档路径和请求方法。
// 返回值 对应的 OpenAPI 操作。
func (doc directoryContract) operation(t *testing.T, path, method string) directoryOperation {
	t.Helper()
	endpoint := doc.Paths[path].Post
	if method == http.MethodPatch {
		endpoint = doc.Paths[path].Patch
	}
	if endpoint == nil {
		t.Fatalf("OpenAPI 缺少 %s %s", method, path)
	}
	return *endpoint
}

// schema 解析 JSON 请求或响应的对象 schema，支持项目中的组件引用。
// 参数 t 为测试上下文，body 为文档声明的请求体或响应。
// 返回值 目标对象的白名单与必填规则。
func (doc directoryContract) schema(t *testing.T, body directoryContractBody) directorySchema {
	t.Helper()
	if name, ok := strings.CutPrefix(body.Ref, "#/components/responses/"); ok {
		body = doc.Components.Responses[name]
	}
	result := body.Content["application/json"].Schema
	if name, ok := strings.CutPrefix(result.Ref, "#/components/schemas/"); ok {
		result = doc.Components.Schemas[name]
	}
	if result.Type != "object" || result.AdditionalProperties == nil || *result.AdditionalProperties {
		t.Fatalf("OpenAPI 未声明封闭的 JSON 对象: %+v", result)
	}
	return result
}

// acceptsFields 核对闭合对象的必填字段和字段白名单。
// 参数 body 为实际 JSON 请求或响应解码后的字段。
// 返回值 字段集合符合选定的 OpenAPI 对象时为真。
func (shape directorySchema) acceptsFields(body map[string]any) bool {
	if len(body) < shape.MinProperties {
		return false
	}
	for _, key := range shape.Required {
		if body[key] == nil {
			return false
		}
	}
	for key := range body {
		if _, ok := shape.Properties[key]; !ok {
			return false
		}
	}
	return true
}

// assertResponse 核对指定状态码明确存在且实际响应字段符合规范。
// 参数 t 为测试上下文，op、status、response 对应操作、状态码和 JSON 响应。
// 返回值 无，契约不匹配时终止测试。
func (doc directoryContract) assertResponse(t *testing.T, op directoryOperation,
	status int, response map[string]any,
) {
	t.Helper()
	if !doc.schema(t, op.Responses[strconv.Itoa(status)]).acceptsFields(response) {
		t.Fatalf("HTTP %d 响应与 OpenAPI 不一致: %v", status, response)
	}
}

// assessorContractRequest 发送实际人员写入请求并核对成功请求和响应的字段契约。
// 参数 t、mux 为测试和路由，pattern、method、path 指定文档及实际入口，input、status 为请求和预期状态。
// 返回值 通过契约检验的目录档案。
func assessorContractRequest(t *testing.T, doc directoryContract, mux *http.ServeMux,
	pattern, method, path string, input map[string]any, status int) entry {
	t.Helper()
	op := doc.operation(t, pattern, method)
	if !doc.schema(t, op.Request).acceptsFields(input) {
		t.Fatalf("合法请求未通过 OpenAPI: %v", input)
	}
	body := testRequest(t, mux, method, path, input, status)
	doc.assertResponse(t, op, status, testJSON[map[string]any](t, body))
	return testJSON[entry](t, body)
}
