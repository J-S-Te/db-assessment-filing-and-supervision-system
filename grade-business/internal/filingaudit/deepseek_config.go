package filingaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DeepSeekConfig 承载可调整的 DeepSeek 预审参数，不含凭据。
type DeepSeekConfig struct {
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	IncludeImages   bool   `json:"include_images"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	MaxTokens       int    `json:"max_tokens"`
}

type deepSeekFile struct {
	DeepSeekConfig
	APIKey string `json:"api_key"`
}

const LocalDeepSeekConfig = "./config/filing-audit.deepseek.local.json"

// LoadDeepSeek 读取指定配置并用运行时凭据构造客户端。
// 参数 path 为可选配置文件位置，key 为进程环境中的 DeepSeek API 密钥，
// 显式路径经 checkDeepSeekConfigPath 校验为不含 .. 分量的 JSON 文件。
// 返回值 已配置的客户端，未启用时为空，配置异常时为错误。
func LoadDeepSeek(path, key string) (*DeepSeek, error) {
	if strings.TrimSpace(path) == "" {
		path = LocalDeepSeekConfig
	}
	if err := checkDeepSeekConfigPath(path); err != nil {
		return nil, err
	}
	config := deepSeekFile{DeepSeekConfig: defaultDeepSeekConfig()}
	root, rootErr := os.OpenRoot(filepath.Dir(path))
	if rootErr != nil {
		if !errors.Is(rootErr, os.ErrNotExist) || path != LocalDeepSeekConfig {
			return nil, errors.New("无法读取 DeepSeek 预审配置文件")
		}
	} else {
		defer root.Close()
		file, err := root.Open(filepath.Base(path))
		if err == nil {
			defer file.Close()
			config, err = readDeepSeekConfig(file)
			if err != nil {
				return nil, err
			}
		} else if !errors.Is(err, os.ErrNotExist) || path != LocalDeepSeekConfig {
			return nil, errors.New("无法读取 DeepSeek 预审配置文件")
		}
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = strings.TrimSpace(config.APIKey)
	}
	if key == "" {
		return nil, nil
	}
	if strings.ContainsAny(key, "\r\n\x00") {
		return nil, errors.New("DeepSeek API Key 包含非法字符")
	}
	return newDeepSeek(config.DeepSeekConfig, key), nil
}

// checkDeepSeekConfigPath 校验部署方显式指定的预审配置文件位置。
// 参数 path 为环境变量给出的配置路径，允许绝对路径或工作目录相对路径。
// 返回值 路径不含 .. 目录分量且指向 .json 文件时为 nil，否则为错误。
func checkDeepSeekConfigPath(path string) error {
	for _, element := range strings.FieldsFunc(path, func(char rune) bool {
		return char == '/' || char == '\\'
	}) {
		if element == ".." {
			return errors.New("DeepSeek 预审配置路径不允许包含 .. 目录分量")
		}
	}
	if strings.ToLower(filepath.Ext(path)) != ".json" {
		return errors.New("DeepSeek 预审配置路径须指向 .json 配置文件")
	}
	return nil
}

// defaultDeepSeekConfig 提供仅设置运行时密钥时使用的预审参数。
// 参数 无。
// 返回值 DeepSeek 模型、思考档位、超时和输出长度的默认值。
func defaultDeepSeekConfig() DeepSeekConfig {
	return DeepSeekConfig{
		Model: "deepseek-flash", ReasoningEffort: "high", TimeoutSeconds: 180, MaxTokens: 16384,
	}
}

// readDeepSeekConfig 校验可在官方请求中生效的模型和思考档位。
// 参数 source 为 JSON 配置文件内容。
// 返回值 补齐默认值的配置，或静态配置错误。
func readDeepSeekConfig(source io.Reader) (deepSeekFile, error) {
	config := deepSeekFile{DeepSeekConfig: defaultDeepSeekConfig()}
	decoder := json.NewDecoder(io.LimitReader(source, 4097))
	decoder.DisallowUnknownFields()
	var extra json.RawMessage
	if err := decoder.Decode(&config); err != nil || decoder.Decode(&extra) != io.EOF {
		return config, errors.New("DeepSeek 预审配置必须是一个 JSON 对象")
	}
	if config.Model != "deepseek-flash" {
		return config, errors.New("DeepSeek-V4.1-Flash 的 API 模型标识是 deepseek-flash")
	}
	config.ReasoningEffort = strings.ToLower(strings.TrimSpace(config.ReasoningEffort))
	switch config.ReasoningEffort {
	case "none", "low", "high", "max":
	default:
		return config, errors.New("DeepSeek reasoning_effort 只接受 none、low、high 或 max")
	}
	if config.TimeoutSeconds < 5 || config.TimeoutSeconds > 900 || config.MaxTokens < 2048 ||
		config.MaxTokens > 393216 {
		return config, errors.New("DeepSeek timeout_seconds 须为 5–900，max_tokens 须为 2048–393216")
	}
	return config, nil
}

// Profile 生成用于材料预审缓存的参数指纹。
// 参数 无。
// 返回值 随模型参数或提示协议变化的标识。
func (d *DeepSeek) Profile() string {
	raw, _ := json.Marshal(d.config)
	digest := sha256.Sum256(raw)
	return "deepseek-v2-" + hex.EncodeToString(digest[:])
}

// Timeout 返回一次 DeepSeek API 调用允许的最长时长。
// 参数 无。
// 返回值 配置中已校验的调用期限。
func (d *DeepSeek) Timeout() time.Duration {
	return time.Duration(d.config.TimeoutSeconds) * time.Second
}

// UsesImages 表示是否发送选定的原件图像。
// 参数 无。
// 返回值 已启用图片分析时为真。
func (d *DeepSeek) UsesImages() bool { return d != nil && d.config.IncludeImages }
