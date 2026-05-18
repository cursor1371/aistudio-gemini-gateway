package imagepreview

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/draw"
	"image/png"
	"strings"

	"aistudio-gemini-gateway/service"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Apply 对 Gemini 图片预览模型执行兼容修复。
// 针对 gemini-2.5-flash-image / gemini-2.5-flash-image-preview 模型：
// 当用户只传了 aspectRatio 但没有图片输入时，自动构造一张白底占位图，
// 并转换为"在上传图片中生成内容"的形式，以满足上游 API 要求。
//
// 参数说明：
//   - model: 已解析的规范化模型名
//   - rawJSON: 请求体 JSON
//   - enabled: 是否启用兼容修复（由配置 gemini.image-preview-compatibility 控制）
func Apply(model string, rawJSON []byte, enabled bool) ([]byte, error) {
	if !enabled {
		return rawJSON, nil
	}

	model = normalizeModel(model)
	if model != "gemini-2.5-flash-image-preview" && model != "gemini-2.5-flash-image" {
		return rawJSON, nil
	}

	if len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) {
		return rawJSON, service.NewRequestError(
			"image preview 兼容修复要求请求体必须是合法 JSON",
			nil,
			service.WithPublicMessage("请求 JSON 非法"),
			service.WithModel(model),
		)
	}

	aspectRatio := strings.TrimSpace(gjson.GetBytes(rawJSON, "generationConfig.imageConfig.aspectRatio").String())
	if aspectRatio == "" {
		return rawJSON, nil
	}

	contents := gjson.GetBytes(rawJSON, "contents")
	if !contents.Exists() || !contents.IsArray() {
		// 请求体不完整时不做激进修正，直接返回原请求。
		return rawJSON, nil
	}

	// 若已存在 inlineData，说明用户已上传图片，无需注入占位图。
	// 此时只需移除 imageConfig，让上游按用户图片处理。
	if hasInlineData(contents.Array()) {
		out, _ := sjson.DeleteBytes(rawJSON, "generationConfig.imageConfig")
		return out, nil
	}

	contentArray := contents.Array()
	if len(contentArray) == 0 {
		return rawJSON, nil
	}

	// 根据 aspectRatio 构造白底占位图。
	whiteImageBase64, err := createWhiteImageBase64(aspectRatio)
	if err != nil {
		return rawJSON, err
	}

	// 构造占位图 part。
	emptyImagePart := []byte(`{"inlineData":{"mime_type":"image/png","data":""}}`)
	emptyImagePart, _ = sjson.SetBytes(emptyImagePart, "inlineData.data", whiteImageBase64)

	// 构造新的 parts 数组：
	// 1. 引导提示文本
	// 2. 白底占位图
	// 3. 原始用户 parts
	newParts := []byte(`[]`)
	newParts, _ = sjson.SetRawBytes(newParts, "-1", []byte(
		`{"text":"Based on the following requirements, create an image within the uploaded picture. `+
			`The new content *MUST* completely cover the entire area of the original picture, `+
			`maintaining its exact proportions, and *NO* blank areas should appear."}`,
	))
	newParts, _ = sjson.SetRawBytes(newParts, "-1", emptyImagePart)

	// 追加用户原始 parts。
	parts := contentArray[0].Get("parts").Array()
	for i := range parts {
		newParts, _ = sjson.SetRawBytes(newParts, "-1", []byte(parts[i].Raw))
	}

	// 写回请求体。
	out := rawJSON
	out, _ = sjson.SetRawBytes(out, "contents.0.parts", newParts)
	out, _ = sjson.SetRawBytes(out, "generationConfig.responseModalities", []byte(`["IMAGE","TEXT"]`))
	out, _ = sjson.DeleteBytes(out, "generationConfig.imageConfig")

	return out, nil
}

// hasInlineData 检查 contents 中是否已存在 inlineData 部分。
func hasInlineData(contents []gjson.Result) bool {
	for i := range contents {
		parts := contents[i].Get("parts").Array()
		for j := range parts {
			if parts[j].Get("inlineData").Exists() {
				return true
			}
		}
	}
	return false
}

// normalizeModel 将模型名归一化为小写基础名。
// 去掉 models/ 前缀和 thinking suffix。
func normalizeModel(model string) string {
	model = strings.TrimSpace(model)
	lower := strings.ToLower(model)
	if strings.HasPrefix(lower, "models/") {
		model = model[len("models/"):]
	}
	if strings.HasSuffix(model, ")") {
		if idx := strings.LastIndex(model, "("); idx > 0 {
			model = model[:idx]
		}
	}
	return strings.ToLower(strings.TrimSpace(model))
}

// createWhiteImageBase64 根据宽高比创建白底 PNG 图片，并返回 base64 编码。
// 支持的宽高比包括常见的 1:1、2:3、3:2、3:4、4:3、4:5、5:4、9:16、16:9、21:9。
func createWhiteImageBase64(aspectRatio string) (string, error) {
	width := 1024
	height := 1024

	switch strings.TrimSpace(aspectRatio) {
	case "1:1":
		width, height = 1024, 1024
	case "2:3":
		width, height = 832, 1248
	case "3:2":
		width, height = 1248, 832
	case "3:4":
		width, height = 864, 1184
	case "4:3":
		width, height = 1184, 864
	case "4:5":
		width, height = 896, 1152
	case "5:4":
		width, height = 1152, 896
	case "9:16":
		width, height = 768, 1344
	case "16:9":
		width, height = 1344, 768
	case "21:9":
		width, height = 1536, 672
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
