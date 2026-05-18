package gemini

import (
	"fmt"
	"strings"

	"aistudio-gemini-gateway/internal/config"
	"aistudio-gemini-gateway/service"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Options 是 Gemini 请求规范化选项。
type Options struct {
	// SafetyMode 安全设置注入模式：
	// - auto：若请求未显式指定 safetySettings，则自动补充默认设置
	// - off：不自动补充
	SafetyMode string

	// SafetySettings 允许由外部配置注入的默认 safety setting 列表。
	// 若为空且 SafetyMode=auto，则使用内置默认值。
	SafetySettings []config.SafetySetting
}

// DefaultSafetySettings 返回默认 Gemini safety settings。
func DefaultSafetySettings() []config.SafetySetting {
	return []config.SafetySetting{
		{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "OFF"},
		{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "OFF"},
		{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "OFF"},
		{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "OFF"},
		{Category: "HARM_CATEGORY_CIVIC_INTEGRITY", Threshold: "BLOCK_NONE"},
	}
}

// NormalizeRequest 对 Gemini 请求做规范化处理。
// 核心步骤：
// 1. 校验 JSON 合法性
// 2. role 自动修正（确保 user/model 交替）
// 3. tool declaration 字段名归一化
// 4. responseSchema -> responseJsonSchema 转换
// 5. functionResponse.name 回填
// 6. thoughtSignature 修正
// 7. 默认 safetySettings 注入
func NormalizeRequest(rawJSON []byte, opts Options) ([]byte, error) {
	if len(strings.TrimSpace(string(rawJSON))) == 0 {
		return nil, service.NewRequestError("Gemini 请求体不能为空", nil)
	}
	if !gjson.ValidBytes(rawJSON) {
		return nil, service.NewRequestError(
			"Gemini 请求 JSON 非法",
			nil,
			service.WithPublicMessage("请求 JSON 非法"),
		)
	}

	out := cloneBytes(rawJSON)

	// 先做字段重命名，避免后续遍历遗漏。
	out = normalizeToolDeclarations(out)
	out = renameJSONPathRaw(out, "generationConfig.responseSchema", "generationConfig.responseJsonSchema")

	// 处理 contents 内部的规范化。
	contents := gjson.GetBytes(out, "contents")
	if contents.Exists() {
		if !contents.IsArray() {
			return nil, service.NewRequestError(
				"Gemini contents 字段必须是数组",
				nil,
				service.WithPublicMessage("请求体 contents 字段格式不正确"),
			)
		}
		out = normalizeContentRoles(out)
		out = normalizeThoughtSignatures(out)
		out = backfillEmptyFunctionResponseNames(out)
	}

	// 注入默认 safetySettings。
	out = AttachSafetySettings(out, opts)
	return out, nil
}

// AttachSafetySettings 在请求未显式提供 safetySettings 时补充默认值。
func AttachSafetySettings(rawJSON []byte, opts Options) []byte {
	mode := strings.ToLower(strings.TrimSpace(opts.SafetyMode))
	if mode == "" {
		mode = "auto"
	}
	if mode == "off" {
		return rawJSON
	}
	// 请求已显式提供 safetySettings 时不覆盖。
	if gjson.GetBytes(rawJSON, "safetySettings").Exists() {
		return rawJSON
	}

	settings := cloneSafetySettings(opts.SafetySettings)
	if len(settings) == 0 {
		settings = DefaultSafetySettings()
	}

	out, err := sjson.SetBytes(rawJSON, "safetySettings", settings)
	if err != nil {
		return rawJSON
	}
	return out
}

// normalizeToolDeclarations 统一 tool declaration 字段名。
// 将 functionDeclarations -> function_declarations，parameters -> parametersJsonSchema。
func normalizeToolDeclarations(rawJSON []byte) []byte {
	out := rawJSON
	tools := gjson.GetBytes(out, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return out
	}

	toolArray := tools.Array()
	for i := range toolArray {
		out = renameJSONPathRaw(out,
			fmt.Sprintf("tools.%d.functionDeclarations", i),
			fmt.Sprintf("tools.%d.function_declarations", i),
		)

		functionDecls := gjson.GetBytes(out, fmt.Sprintf("tools.%d.function_declarations", i))
		if !functionDecls.Exists() || !functionDecls.IsArray() {
			continue
		}

		declArray := functionDecls.Array()
		for j := range declArray {
			out = renameJSONPathRaw(out,
				fmt.Sprintf("tools.%d.function_declarations.%d.parameters", i, j),
				fmt.Sprintf("tools.%d.function_declarations.%d.parametersJsonSchema", i, j),
			)
		}
	}
	return out
}

// normalizeContentRoles 自动修正 contents 中缺失或非法的 role 字段。
// 确保 role 在 user / model 之间交替，首条默认为 user。
func normalizeContentRoles(rawJSON []byte) []byte {
	contents := gjson.GetBytes(rawJSON, "contents")
	if !contents.Exists() || !contents.IsArray() {
		return rawJSON
	}

	out := rawJSON
	prevRole := ""

	idx := 0
	contents.ForEach(func(_, value gjson.Result) bool {
		role := strings.TrimSpace(value.Get("role").String())
		valid := role == "user" || role == "model"

		if role == "" || !valid {
			var newRole string
			switch prevRole {
			case "":
				newRole = "user"
			case "user":
				newRole = "model"
			default:
				newRole = "user"
			}
			path := fmt.Sprintf("contents.%d.role", idx)
			out, _ = sjson.SetBytes(out, path, newRole)
			role = newRole
		}

		prevRole = role
		idx++
		return true
	})

	return out
}

// normalizeThoughtSignatures 修正 model 消息中的 thoughtSignature。
// 对包含 functionCall 或已有 thoughtSignature 的 model part，
// 统一设置 thoughtSignature 跳过校验值。
func normalizeThoughtSignatures(rawJSON []byte) []byte {
	out := rawJSON

	gjson.GetBytes(out, "contents").ForEach(func(contentIdx, content gjson.Result) bool {
		if content.Get("role").String() != "model" {
			return true
		}

		content.Get("parts").ForEach(func(partIdx, part gjson.Result) bool {
			if part.Get("functionCall").Exists() || part.Get("thoughtSignature").Exists() {
				path := fmt.Sprintf("contents.%d.parts.%d.thoughtSignature", contentIdx.Int(), partIdx.Int())
				out, _ = sjson.SetBytes(out, path, "skip_thought_signature_validator")
			}
			return true
		})
		return true
	})

	return out
}

// backfillEmptyFunctionResponseNames 回填 functionResponse 中缺失的 name 字段。
// 当 model 消息中包含 functionCall 后，紧跟的 functionResponse 若 name 为空，
// 则用对应 functionCall 的 name 进行回填。
func backfillEmptyFunctionResponseNames(data []byte) []byte {
	contents := gjson.GetBytes(data, "contents")
	if !contents.Exists() || !contents.IsArray() {
		return data
	}

	out := data
	var pendingCallNames []string

	contents.ForEach(func(contentIdx, content gjson.Result) bool {
		role := content.Get("role").String()

		// 收集 model 消息中的 functionCall name 列表。
		if role == "model" {
			var names []string
			content.Get("parts").ForEach(func(_, part gjson.Result) bool {
				if part.Get("functionCall").Exists() {
					name := strings.TrimSpace(part.Get("functionCall.name").String())
					if name != "" {
						names = append(names, name)
					}
				}
				return true
			})
			if len(names) > 0 {
				pendingCallNames = names
			} else {
				pendingCallNames = nil
			}
			return true
		}

		// 对非 model 消息进行 functionResponse name 回填。
		if len(pendingCallNames) == 0 {
			return true
		}

		responseIndex := 0
		content.Get("parts").ForEach(func(partIdx, part gjson.Result) bool {
			if !part.Get("functionResponse").Exists() {
				return true
			}

			name := strings.TrimSpace(part.Get("functionResponse.name").String())
			if name == "" && responseIndex < len(pendingCallNames) {
				path := fmt.Sprintf("contents.%d.parts.%d.functionResponse.name", contentIdx.Int(), partIdx.Int())
				out, _ = sjson.SetBytes(out, path, pendingCallNames[responseIndex])
			}
			responseIndex++
			return true
		})

		pendingCallNames = nil
		return true
	})

	return out
}

// renameJSONPathRaw 将 JSON 中的一个路径重命名为另一个路径。
// 若旧路径不存在则不做操作。
func renameJSONPathRaw(rawJSON []byte, oldPath, newPath string) []byte {
	if oldPath == "" || newPath == "" || oldPath == newPath {
		return rawJSON
	}

	oldValue := gjson.GetBytes(rawJSON, oldPath)
	if !oldValue.Exists() {
		return rawJSON
	}

	out, err := sjson.SetRawBytes(rawJSON, newPath, []byte(oldValue.Raw))
	if err != nil {
		return rawJSON
	}
	out, _ = sjson.DeleteBytes(out, oldPath)
	return out
}

// cloneSafetySettings 深拷贝 SafetySetting 切片。
func cloneSafetySettings(in []config.SafetySetting) []config.SafetySetting {
	if len(in) == 0 {
		return nil
	}
	out := make([]config.SafetySetting, len(in))
	copy(out, in)
	return out
}

// cloneBytes 深拷贝字节切片。
func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
