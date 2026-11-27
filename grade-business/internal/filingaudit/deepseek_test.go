package filingaudit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDeepSeekAssessment 检查真实 HTTP 契约及单项结论的原件依据。
// 参数 t 提供本地模型替身和合成材料。
// 返回值 无。
func TestDeepSeekAssessment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Model           string `json:"model"`
			ReasoningEffort string `json:"reasoning_effort"`
			Messages        []struct{ Content json.RawMessage }
		}
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer fixture-key" ||
			json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("请求没有通过 chat completions 发往模型")
		}
		if input.Model != "deepseek-flash" || input.ReasoningEffort != "max" ||
			len(input.Messages) != 2 || !strings.Contains(string(input.Messages[1].Content), "盖章状态：未盖章") {
			t.Error("请求未携带配置的模型、思考程度和原文")
		}
		rows := make([]map[string]any, 0, 11)
		for _, rule := range Rules() {
			row := map[string]any{"ruleId": rule.ID, "status": "manual", "message": "缺少可核实证据"}
			switch rule.ID {
			case "seals":
				row["status"], row["message"] = "fail", "报告写明未盖章"
				row["evidence"] = []map[string]string{{"sourceId": "S1", "quote": "盖章状态：未盖章"}}
			case "object_number":
				row["status"], row["message"] = "pass", "编号一致"
				row["evidence"] = []map[string]string{{"sourceId": "S1", "quote": "不存在的原文"}}
			}
			rows = append(rows, row)
		}
		content, _ := json.Marshal(map[string]any{"items": rows})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
			"finish_reason": "stop", "message": map[string]string{
				"content": string(content), "reasoning_content": "内部推理不应进入结果",
			},
		}}})
	}))
	defer server.Close()
	model := newDeepSeek(DeepSeekConfig{Model: "deepseek-flash", ReasoningEffort: "max", MaxTokens: 16384, TimeoutSeconds: 15}, "fixture-key")
	model.endpoint = server.URL + "/chat/completions"
	result, err := model.Assess(t.Context(), []Document{
		auditFixture("classification_report", "定级报告.docx", "盖章状态：未盖章"),
	}, Unit{})
	if err != nil || len(result) != 11 {
		t.Fatalf("逐项结论未返回: %v", err)
	}
	if result[0].Status != "manual" || result[3].Status != "fail" ||
		len(result[3].Evidence) != 1 || result[3].Evidence[0].FileName != "定级报告.docx" {
		t.Fatalf("未正确区分虚构证据和原文引用: %+v %+v", result[0], result[3])
	}
}

// TestDeepSeekConfigEffort 验证文件可设置官方四档思考程度并拒绝拼写错误。
// 参数 t 提供合成配置文本。
// 返回值 无。
func TestDeepSeekConfigEffort(t *testing.T) {
	for _, effort := range []string{"none", "low", "high", "max"} {
		config := `{"model":"deepseek-flash","reasoning_effort":"` + effort + `"}`
		value, err := readDeepSeekConfig(strings.NewReader(config))
		if err != nil || value.ReasoningEffort != effort {
			t.Fatalf("合法参数 %s 被拒绝: %v", effort, err)
		}
	}
	if _, err := readDeepSeekConfig(strings.NewReader(`{"reasoning_effort":"highest"}`)); err == nil {
		t.Fatal("无效的思考程度被接受")
	}
	config, err := readDeepSeekConfig(strings.NewReader(`{"reasoning_effort":" Max "}`))
	if err != nil || config.ReasoningEffort != "max" {
		t.Fatalf("未兼容配置文件中的 Max 写法: %v", err)
	}
}

// TestLoadDeepSeekFromEnvironment 验证没有本地配置文件时可使用部署提供的密钥。
// 参数 t 为隔离工作目录和预审客户端断言的测试上下文。
// 返回值 无。
func TestLoadDeepSeekFromEnvironment(t *testing.T) {
	t.Chdir(t.TempDir())
	client, err := LoadDeepSeek("", "synthetic-deployment-test-key")
	if err != nil || client == nil || client.config != defaultDeepSeekConfig() {
		t.Fatal("仅提供部署密钥时应使用默认参数")
	}
	client, err = LoadDeepSeek("", "")
	if err != nil || client != nil {
		t.Fatal("未配置部署密钥时应使用本地规则")
	}
}

// TestDeepSeekConfigPath 验证预审配置路径接受绝对路径并拒绝目录穿越和非 JSON 文件。
// 参数 t 提供合成路径用例。
// 返回值 无。
func TestDeepSeekConfigPath(t *testing.T) {
	for _, path := range []string{"config/filing-audit.deepseek.local.json",
		"/srv/grade-business/config/filing-audit.deepseek.local.json"} {
		if err := checkDeepSeekConfigPath(path); err != nil {
			t.Fatalf("合法配置路径 %s 被拒绝: %v", path, err)
		}
	}
	for _, path := range []string{"../secret.json", "config/../secret.json",
		`config\..\secret.json`, "config/filing-audit.toml", "/etc/passwd"} {
		if err := checkDeepSeekConfigPath(path); err == nil {
			t.Fatalf("不合规的配置路径 %s 被接受", path)
		}
	}
	if _, err := LoadDeepSeek("../config/filing-audit.deepseek.local.json", ""); err == nil {
		t.Fatal("LoadDeepSeek 接受了包含 .. 的配置路径")
	}
}
