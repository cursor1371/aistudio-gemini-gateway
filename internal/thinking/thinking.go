package thinking

import (
	"fmt"
	"strconv"
	"strings"

	cfgpkg "aistudio-gemini-gateway/internal/config"
	"aistudio-gemini-gateway/service"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ---------------------------------------------------------------------------
// Thinking 模式与 Level 定义
// ---------------------------------------------------------------------------

// ThinkingMode 表示 thinking 配置模式。
type ThinkingMode int

const (
	// ModeBudget 表示固定 budget 值。
	ModeBudget ThinkingMode = iota
	// ModeLevel 表示离散等级。
	ModeLevel
	// ModeNone 表示关闭 thinking（budget=0）。
	ModeNone
	// ModeAuto 表示由模型自行决定（budget=-1）。
	ModeAuto
)

// String 返回 thinking mode 的可读字符串。
func (m ThinkingMode) String() string {
	switch m {
	case ModeBudget:
		return "budget"
	case ModeLevel:
		return "level"
	case ModeNone:
		return "none"
	case ModeAuto:
		return "auto"
	default:
		return "unknown"
	}
}

// ThinkingLevel 表示离散 thinking 等级。
type ThinkingLevel string

const (
	LevelNone    ThinkingLevel = "none"
	LevelAuto    ThinkingLevel = "auto"
	LevelMinimal ThinkingLevel = "minimal"
	LevelLow     ThinkingLevel = "low"
	LevelMedium  ThinkingLevel = "medium"
	LevelHigh    ThinkingLevel = "high"
	LevelXHigh   ThinkingLevel = "xhigh"
	LevelMax     ThinkingLevel = "max"
)

// ThinkingConfig 是解析后的统一 thinking 配置。
type ThinkingConfig struct {
	Mode   ThinkingMode
	Budget int64
	Level  ThinkingLevel
}

// ---------------------------------------------------------------------------
// 模型名 Thinking Suffix 解析
// ---------------------------------------------------------------------------

// SuffixResult 表示模型名中 thinking suffix 的解析结果。
type SuffixResult struct {
	// ModelName 是去掉 suffix 后的纯模型名。
	ModelName string
	// HasSuffix 表示原始模型名是否带有 (xxx) 后缀。
	HasSuffix bool
	// RawSuffix 是括号内的原始文本。
	RawSuffix string
}

// ParseSuffix 从模型名中提取 thinking suffix。
// 例如：gemini-2.5-pro(8192) 会被解析为 ModelName="gemini-2.5-pro", RawSuffix="8192"。
func ParseSuffix(model string) SuffixResult {
	model = strings.TrimSpace(model)
	if model == "" {
		return SuffixResult{}
	}

	lastOpen := strings.LastIndex(model, "(")
	if lastOpen == -1 || !strings.HasSuffix(model, ")") {
		return SuffixResult{
			ModelName: model,
			HasSuffix: false,
		}
	}

	return SuffixResult{
		ModelName: strings.TrimSpace(model[:lastOpen]),
		HasSuffix: true,
		RawSuffix: strings.TrimSpace(model[lastOpen+1 : len(model)-1]),
	}
}

// ParseNumericSuffix 解析数字后缀为 budget 值。
// 仅接受非负整数。
func ParseNumericSuffix(rawSuffix string) (budget int64, ok bool) {
	rawSuffix = strings.TrimSpace(rawSuffix)
	if rawSuffix == "" {
		return 0, false
	}
	value, err := strconv.ParseInt(rawSuffix, 10, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

// ParseSpecialSuffix 解析特殊关键字后缀（none / auto / -1）。
func ParseSpecialSuffix(rawSuffix string) (mode ThinkingMode, ok bool) {
	switch strings.ToLower(strings.TrimSpace(rawSuffix)) {
	case "none":
		return ModeNone, true
	case "auto", "-1":
		return ModeAuto, true
	default:
		return ModeBudget, false
	}
}

// ParseLevelSuffix 解析离散 level 后缀。
func ParseLevelSuffix(rawSuffix string) (ThinkingLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(rawSuffix)) {
	case "minimal":
		return LevelMinimal, true
	case "low":
		return LevelLow, true
	case "medium":
		return LevelMedium, true
	case "high":
		return LevelHigh, true
	case "xhigh":
		return LevelXHigh, true
	case "max":
		return LevelMax, true
	default:
		return "", false
	}
}

// ---------------------------------------------------------------------------
// 请求体 ThinkingConfig 提取
// ---------------------------------------------------------------------------

// ExtractConfig 从 Gemini 请求体中提取 thinking 配置。
// 优先级：thinkingLevel > thinkingBudget。
func ExtractConfig(body []byte) ThinkingConfig {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ThinkingConfig{}
	}

	// 优先提取 thinkingLevel。
	level := gjson.GetBytes(body, "generationConfig.thinkingConfig.thinkingLevel")
	if !level.Exists() {
		level = gjson.GetBytes(body, "generationConfig.thinkingConfig.thinking_level")
	}
	if level.Exists() {
		value := strings.ToLower(strings.TrimSpace(level.String()))
		switch value {
		case "none":
			return ThinkingConfig{Mode: ModeNone, Budget: 0}
		case "auto":
			return ThinkingConfig{Mode: ModeAuto, Budget: -1}
		case "":
			return ThinkingConfig{}
		default:
			return ThinkingConfig{Mode: ModeLevel, Level: ThinkingLevel(value)}
		}
	}

	// 若无 level，再提取 thinkingBudget。
	budget := gjson.GetBytes(body, "generationConfig.thinkingConfig.thinkingBudget")
	if !budget.Exists() {
		budget = gjson.GetBytes(body, "generationConfig.thinkingConfig.thinking_budget")
	}
	if budget.Exists() {
		value := budget.Int()
		switch value {
		case 0:
			return ThinkingConfig{Mode: ModeNone, Budget: 0}
		case -1:
			return ThinkingConfig{Mode: ModeAuto, Budget: -1}
		default:
			return ThinkingConfig{Mode: ModeBudget, Budget: value}
		}
	}

	return ThinkingConfig{}
}

// HasThinkingConfig 判断是否存在有效的 thinking 配置。
func HasThinkingConfig(config ThinkingConfig) bool {
	return config.Mode != ModeBudget || config.Budget != 0 || config.Level != ""
}

// StripThinkingConfig 移除 Gemini 请求中的 thinkingConfig 字段。
func StripThinkingConfig(body []byte) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	out, _ := sjson.DeleteBytes(body, "generationConfig.thinkingConfig")
	return out
}

// ---------------------------------------------------------------------------
// Apply：Thinking 处理主入口
// ---------------------------------------------------------------------------

// Apply 是 Gemini-only thinking 处理主入口。
// 处理顺序：
//  1. 检查全局 thinking mode 是否启用
//  2. 解析模型名中的 suffix（suffix 优先于请求体 thinkingConfig）
//  3. 从请求体中提取 thinkingConfig
//  4. 校验模型 thinking 能力
//  5. 将校验后的配置写入请求体
//
// 注意：
//   - 未知模型（modelInfo == nil）不允许携带 thinking 配置，会直接返回错误。
//   - 模型不支持 thinking 时，会静默剥离 thinkingConfig。
func Apply(body []byte, model string, modelInfo *service.ModelInfo, cfg cfgpkg.GeminiThinkingConfig) ([]byte, error) {
	// 全局关闭 thinking 时直接返回。
	if strings.EqualFold(strings.TrimSpace(cfg.Mode), "off") {
		return body, nil
	}

	suffix := ParseSuffix(model)

	var (
		thinkingConfig ThinkingConfig
		err            error
	)

	// Suffix 优先：若模型名带有 thinking suffix，则从 suffix 解析配置。
	if suffix.HasSuffix {
		thinkingConfig, err = parseSuffixToConfig(suffix.RawSuffix, suffix.ModelName)
		if err != nil {
			return body, err
		}
	} else {
		thinkingConfig = ExtractConfig(body)
	}

	// 无有效 thinking 配置时跳过。
	if !HasThinkingConfig(thinkingConfig) {
		return body, nil
	}

	// 需要修改请求体时，必须保证 body 是合法 JSON。
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body, service.NewRequestError(
			"thinking 处理要求请求体必须是合法 JSON",
			nil,
			service.WithPublicMessage("请求 JSON 非法"),
			service.WithModel(strings.TrimSpace(model)),
		)
	}

	// 未知模型不允许 thinking 配置透传。
	if modelInfo == nil {
		return body, service.NewRequestError(
			"未知模型不允许使用 thinking 配置",
			nil,
			service.WithPublicMessage("未知模型不允许使用 thinking 配置"),
			service.WithModel(strings.TrimSpace(model)),
		)
	}

	// 模型不支持 thinking 时，静默剥离 thinkingConfig。
	if modelInfo.Thinking == nil || !modelInfo.SupportsThinking {
		return StripThinkingConfig(body), nil
	}

	// 校验并归一化 thinking 配置。
	validated, err := ValidateConfig(thinkingConfig, modelInfo, cfg.StrictValidation, suffix.HasSuffix)
	if err != nil {
		return body, err
	}
	if validated == nil {
		return body, nil
	}

	return applyGemini(body, *validated, modelInfo), nil
}

// ---------------------------------------------------------------------------
// ValidateConfig：Thinking 配置校验与归一化
// ---------------------------------------------------------------------------

// ValidateConfig 校验 thinking 配置并归一化。
// 主要职责：
//  1. 根据模型能力类型（budget-only / level-only / hybrid）做格式转换
//  2. 处理 none / auto 等特殊模式
//  3. 根据模型 ThinkingSupport 做范围 clamp
//  4. 严格模式下额外校验 budget 范围
func ValidateConfig(config ThinkingConfig, modelInfo *service.ModelInfo, strictValidation bool, fromSuffix bool) (*ThinkingConfig, error) {
	if modelInfo == nil || modelInfo.Thinking == nil {
		if config.Mode != ModeNone {
			return nil, newThinkingError(modelInfo, "该模型不支持 thinking")
		}
		return &config, nil
	}

	support := modelInfo.Thinking
	capability := detectModelCapability(modelInfo)

	allowClampUnsupported := !strictValidation || fromSuffix
	strictBudget := strictValidation && !fromSuffix
	budgetDerivedFromLevel := false

	// 根据模型能力类型做格式转换。
	switch capability {
	case capabilityBudgetOnly:
		// 仅支持 budget 的模型，需要把 level 转成 budget。
		if config.Mode == ModeLevel {
			if config.Level == LevelAuto {
				break
			}
			budget, ok := ConvertLevelToBudget(string(config.Level))
			if !ok {
				return nil, newThinkingError(modelInfo, fmt.Sprintf("未知 thinking level: %s", config.Level))
			}
			config.Mode = ModeBudget
			config.Budget = budget
			config.Level = ""
			budgetDerivedFromLevel = true
		}

	case capabilityLevelOnly:
		// 仅支持 level 的模型，需要把 budget 转成 level。
		if config.Mode == ModeBudget {
			level, ok := ConvertBudgetToLevel(config.Budget)
			if !ok {
				return nil, newThinkingError(modelInfo, fmt.Sprintf("thinking budget %d 无法转换为有效 level", config.Budget))
			}
			config.Mode = ModeLevel
			config.Level = clampLevel(ThinkingLevel(level), support.Levels)
			config.Budget = 0
		}

	case capabilityHybrid:
		// 混合能力模型（如 Gemini 3.x）：保留原始格式不做转换。
	}

	// 处理特殊等级到模式的归一化。
	if config.Mode == ModeLevel && config.Level == LevelNone {
		config.Mode = ModeNone
		config.Budget = 0
		config.Level = ""
	}
	if config.Mode == ModeLevel && config.Level == LevelAuto {
		config.Mode = ModeAuto
		config.Budget = -1
		config.Level = ""
	}
	if config.Mode == ModeBudget && config.Budget == 0 {
		config.Mode = ModeNone
		config.Level = ""
	}

	// 若模型声明了支持的 level 列表，则校验当前 level 是否在列表中。
	if len(support.Levels) > 0 && config.Mode == ModeLevel {
		if !hasLevel(support.Levels, string(config.Level)) {
			if allowClampUnsupported {
				config.Level = clampLevel(config.Level, support.Levels)
			}
			if !hasLevel(support.Levels, string(config.Level)) {
				return nil, newThinkingError(modelInfo,
					fmt.Sprintf("thinking level %q 不被该模型支持，允许值：%s",
						string(config.Level),
						strings.Join(normalizeLevels(support.Levels), ", "),
					),
				)
			}
		}
	}

	// 严格模式下，额外校验 budget 范围。
	if strictBudget && config.Mode == ModeBudget && !budgetDerivedFromLevel {
		min, max := support.Min, support.Max
		if min != 0 || max != 0 {
			if config.Budget < min || config.Budget > max || (config.Budget == 0 && !support.ZeroAllowed) {
				return nil, newThinkingError(modelInfo,
					fmt.Sprintf("thinking budget %d 超出允许范围 [%d, %d]", config.Budget, min, max),
				)
			}
		}
	}

	// auto 模式在模型不支持动态时，退化为中间值。
	if config.Mode == ModeAuto && !support.DynamicAllowed {
		config = convertAutoToMidRange(config, support)
	}

	// 对 budget / auto / none 模式统一 clamp。
	switch config.Mode {
	case ModeBudget, ModeAuto, ModeNone:
		config.Budget = clampBudget(config.Budget, support)
	}

	// 对 level-only / hybrid 模型，当 ModeNone 需要"思考但不显示"时，
	// 给出最低 level，后续 applyLevelFormat 会同时设 includeThoughts=false。
	if config.Mode == ModeNone && len(support.Levels) > 0 && config.Level == "" {
		config.Level = ThinkingLevel(strings.ToLower(strings.TrimSpace(support.Levels[0])))
	}

	return &config, nil
}

// ---------------------------------------------------------------------------
// Level <-> Budget 转换
// ---------------------------------------------------------------------------

// ConvertLevelToBudget 将 thinking level 转换为对应的 budget 值。
func ConvertLevelToBudget(level string) (int64, bool) {
	budget, ok := levelToBudgetMap[strings.ToLower(strings.TrimSpace(level))]
	return budget, ok
}

// ConvertBudgetToLevel 将 thinking budget 转换为最接近的 level 名称。
func ConvertBudgetToLevel(budget int64) (string, bool) {
	switch {
	case budget < -1:
		return "", false
	case budget == -1:
		return string(LevelAuto), true
	case budget == 0:
		return string(LevelNone), true
	case budget <= thresholdMinimal:
		return string(LevelMinimal), true
	case budget <= thresholdLow:
		return string(LevelLow), true
	case budget <= thresholdMedium:
		return string(LevelMedium), true
	case budget <= thresholdHigh:
		return string(LevelHigh), true
	default:
		return string(LevelXHigh), true
	}
}

// ---------------------------------------------------------------------------
// 请求体写入：将校验后的 thinking 配置应用到 Gemini 请求
// ---------------------------------------------------------------------------

// applyCompatible 在模型能力信息不完整时做兼容性写入。
func applyCompatible(body []byte, config ThinkingConfig) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		body = []byte(`{}`)
	}
	if config.Mode == ModeAuto {
		return applyBudgetFormat(body, config)
	}
	if config.Mode == ModeLevel || (config.Mode == ModeNone && config.Level != "") {
		return applyLevelFormat(body, config)
	}
	return applyBudgetFormat(body, config)
}

// applyGemini 根据模型能力写入 thinking 配置。
func applyGemini(body []byte, config ThinkingConfig, modelInfo *service.ModelInfo) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		body = []byte(`{}`)
	}
	if modelInfo == nil || modelInfo.Thinking == nil {
		return applyCompatible(body, config)
	}

	switch config.Mode {
	case ModeLevel:
		return applyLevelFormat(body, config)
	case ModeNone:
		if len(modelInfo.Thinking.Levels) > 0 {
			return applyLevelFormat(body, config)
		}
		return applyBudgetFormat(body, config)
	default:
		return applyBudgetFormat(body, config)
	}
}

// applyLevelFormat 以 thinkingLevel 格式写入请求体。
func applyLevelFormat(body []byte, config ThinkingConfig) []byte {
	// 先清除所有可能冲突的旧字段。
	result, _ := sjson.DeleteBytes(body, "generationConfig.thinkingConfig.thinkingBudget")
	result, _ = sjson.DeleteBytes(result, "generationConfig.thinkingConfig.thinking_budget")
	result, _ = sjson.DeleteBytes(result, "generationConfig.thinkingConfig.thinking_level")
	result, _ = sjson.DeleteBytes(result, "generationConfig.thinkingConfig.include_thoughts")

	if config.Mode == ModeNone {
		// "思考但不显示"：设置 level 但 includeThoughts=false。
		result, _ = sjson.SetBytes(result, "generationConfig.thinkingConfig.includeThoughts", false)
		if config.Level != "" {
			result, _ = sjson.SetBytes(result, "generationConfig.thinkingConfig.thinkingLevel", string(config.Level))
		}
		return result
	}

	if config.Mode != ModeLevel {
		return result
	}

	result, _ = sjson.SetBytes(result, "generationConfig.thinkingConfig.thinkingLevel", string(config.Level))

	includeThoughts := extractIncludeThoughts(body, true)
	result, _ = sjson.SetBytes(result, "generationConfig.thinkingConfig.includeThoughts", includeThoughts)
	return result
}

// applyBudgetFormat 以 thinkingBudget 格式写入请求体。
func applyBudgetFormat(body []byte, config ThinkingConfig) []byte {
	// 先清除所有可能冲突的旧字段。
	result, _ := sjson.DeleteBytes(body, "generationConfig.thinkingConfig.thinkingLevel")
	result, _ = sjson.DeleteBytes(result, "generationConfig.thinkingConfig.thinking_level")
	result, _ = sjson.DeleteBytes(result, "generationConfig.thinkingConfig.thinking_budget")
	result, _ = sjson.DeleteBytes(result, "generationConfig.thinkingConfig.include_thoughts")

	if config.Mode == ModeNone {
		result, _ = sjson.SetBytes(result, "generationConfig.thinkingConfig.thinkingBudget", config.Budget)
		result, _ = sjson.SetBytes(result, "generationConfig.thinkingConfig.includeThoughts", false)
		return result
	}

	// 检查用户是否显式设置了 includeThoughts。
	includeThoughts, explicitlySet := extractIncludeThoughtsWithPresence(body)
	if !explicitlySet {
		if config.Mode == ModeAuto {
			includeThoughts = true
		} else {
			includeThoughts = config.Budget > 0
		}
	}

	result, _ = sjson.SetBytes(result, "generationConfig.thinkingConfig.thinkingBudget", config.Budget)
	result, _ = sjson.SetBytes(result, "generationConfig.thinkingConfig.includeThoughts", includeThoughts)
	return result
}

// ---------------------------------------------------------------------------
// includeThoughts 提取辅助
// ---------------------------------------------------------------------------

// extractIncludeThoughts 提取 includeThoughts 字段，缺失时返回默认值。
func extractIncludeThoughts(body []byte, defaultValue bool) bool {
	value, ok := extractIncludeThoughtsWithPresence(body)
	if !ok {
		return defaultValue
	}
	return value
}

// extractIncludeThoughtsWithPresence 提取 includeThoughts 字段及其是否存在。
func extractIncludeThoughtsWithPresence(body []byte) (bool, bool) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false, false
	}
	if item := gjson.GetBytes(body, "generationConfig.thinkingConfig.includeThoughts"); item.Exists() {
		return item.Bool(), true
	}
	if item := gjson.GetBytes(body, "generationConfig.thinkingConfig.include_thoughts"); item.Exists() {
		return item.Bool(), true
	}
	return false, false
}

// ---------------------------------------------------------------------------
// Suffix 到 ThinkingConfig 的转换
// ---------------------------------------------------------------------------

// parseSuffixToConfig 将模型名 suffix 解析为 ThinkingConfig。
func parseSuffixToConfig(rawSuffix, model string) (ThinkingConfig, error) {
	// 先尝试特殊关键字。
	if mode, ok := ParseSpecialSuffix(rawSuffix); ok {
		switch mode {
		case ModeNone:
			return ThinkingConfig{Mode: ModeNone, Budget: 0}, nil
		case ModeAuto:
			return ThinkingConfig{Mode: ModeAuto, Budget: -1}, nil
		}
	}

	// 再尝试离散 level。
	if level, ok := ParseLevelSuffix(rawSuffix); ok {
		return ThinkingConfig{Mode: ModeLevel, Level: level}, nil
	}

	// 再尝试数字 budget。
	if budget, ok := ParseNumericSuffix(rawSuffix); ok {
		if budget == 0 {
			return ThinkingConfig{Mode: ModeNone, Budget: 0}, nil
		}
		return ThinkingConfig{Mode: ModeBudget, Budget: budget}, nil
	}

	return ThinkingConfig{}, service.NewRequestError(
		fmt.Sprintf("模型 %s 的 thinking suffix 非法：(%s)", strings.TrimSpace(model), strings.TrimSpace(rawSuffix)),
		nil,
		service.WithPublicMessage("模型 thinking 后缀格式非法"),
		service.WithModel(strings.TrimSpace(model)),
	)
}

// ---------------------------------------------------------------------------
// 模型能力检测与辅助函数
// ---------------------------------------------------------------------------

// newThinkingError 构造 thinking 专用的请求错误。
func newThinkingError(modelInfo *service.ModelInfo, message string) error {
	model := "unknown"
	if modelInfo != nil {
		model = firstNonEmpty(modelInfo.BaseName, modelInfo.Name)
	}
	return service.NewRequestError(
		message,
		nil,
		service.WithPublicMessage(message),
		service.WithModel(model),
	)
}

// level -> budget 转换阈值。
const (
	thresholdMinimal int64 = 512
	thresholdLow     int64 = 1024
	thresholdMedium  int64 = 8192
	thresholdHigh    int64 = 24576
)

// levelToBudgetMap 定义了 level 到 budget 的精确映射。
var levelToBudgetMap = map[string]int64{
	"none":    0,
	"auto":    -1,
	"minimal": 512,
	"low":     1024,
	"medium":  8192,
	"high":    24576,
	"xhigh":   32768,
	"max":     128000,
}

// modelCapability 表示模型支持的 thinking 能力类型。
type modelCapability int

const (
	capabilityUnknown    modelCapability = iota - 1 // 未知
	capabilityNone                                   // 不支持 thinking
	capabilityBudgetOnly                             // 仅支持 budget
	capabilityLevelOnly                              // 仅支持 level
	capabilityHybrid                                 // 同时支持 budget 与 level
)

// detectModelCapability 根据模型元信息判断 thinking 能力类型。
func detectModelCapability(modelInfo *service.ModelInfo) modelCapability {
	if modelInfo == nil {
		return capabilityUnknown
	}
	if modelInfo.Thinking == nil {
		return capabilityNone
	}

	support := modelInfo.Thinking
	hasBudget := support.Min > 0 || support.Max > 0
	hasLevels := len(support.Levels) > 0

	switch {
	case hasBudget && hasLevels:
		return capabilityHybrid
	case hasBudget:
		return capabilityBudgetOnly
	case hasLevels:
		return capabilityLevelOnly
	default:
		return capabilityNone
	}
}

// convertAutoToMidRange 在模型不支持动态 thinking 时，将 auto 退化为中间值。
func convertAutoToMidRange(config ThinkingConfig, support *service.ThinkingSupport) ThinkingConfig {
	if support == nil {
		return config
	}

	// 纯 level 模型：退化为 medium。
	if len(support.Levels) > 0 && support.Min == 0 && support.Max == 0 {
		config.Mode = ModeLevel
		config.Level = LevelMedium
		config.Budget = 0
		return config
	}

	// budget 模型：取 min 和 max 的中间值。
	mid := (support.Min + support.Max) / 2
	if mid <= 0 && support.ZeroAllowed {
		config.Mode = ModeNone
		config.Budget = 0
		return config
	}
	if mid <= 0 {
		config.Mode = ModeBudget
		config.Budget = support.Min
		return config
	}
	config.Mode = ModeBudget
	config.Budget = mid
	return config
}

// clampBudget 将 budget 值约束到模型允许的范围内。
func clampBudget(value int64, support *service.ThinkingSupport) int64 {
	if support == nil {
		return value
	}
	// -1 表示 auto，不做 clamp。
	if value == -1 {
		return value
	}

	min, max := support.Min, support.Max

	// budget=0 且不允许 zero 时，回落到 min。
	if value == 0 && !support.ZeroAllowed {
		if min > 0 {
			return min
		}
		return value
	}

	// 无范围限制时直接返回。
	if min == 0 && max == 0 {
		return value
	}

	if value < min {
		if value == 0 && support.ZeroAllowed {
			return 0
		}
		return min
	}
	if value > max {
		return max
	}
	return value
}

// clampLevel 将不支持的 level 近似到最接近的已支持 level。
func clampLevel(level ThinkingLevel, supported []string) ThinkingLevel {
	if len(supported) == 0 || hasLevel(supported, string(level)) {
		return level
	}

	pos := levelIndex(string(level))
	if pos == -1 {
		return level
	}

	bestIdx := -1
	bestDist := len(standardLevelOrder) + 1

	for _, item := range supported {
		idx := levelIndex(item)
		if idx == -1 {
			continue
		}
		dist := abs(pos - idx)
		if dist < bestDist || (dist == bestDist && idx < bestIdx) {
			bestIdx = idx
			bestDist = dist
		}
	}

	if bestIdx >= 0 {
		return standardLevelOrder[bestIdx]
	}
	return level
}

// hasLevel 检查 target 是否在 levels 列表中（大小写不敏感）。
func hasLevel(levels []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, level := range levels {
		if strings.EqualFold(strings.TrimSpace(level), target) {
			return true
		}
	}
	return false
}

// normalizeLevels 将 level 列表统一为小写。
func normalizeLevels(levels []string) []string {
	if len(levels) == 0 {
		return nil
	}
	out := make([]string, len(levels))
	for i, item := range levels {
		out[i] = strings.ToLower(strings.TrimSpace(item))
	}
	return out
}

// standardLevelOrder 定义了标准的 level 排序。
// 用于 clampLevel 在不支持目标 level 时找最近的替代。
var standardLevelOrder = []ThinkingLevel{
	LevelMinimal,
	LevelLow,
	LevelMedium,
	LevelHigh,
	LevelXHigh,
	LevelMax,
}

// levelIndex 返回 level 在标准排序中的位置索引。
func levelIndex(level string) int {
	level = strings.ToLower(strings.TrimSpace(level))
	for i, item := range standardLevelOrder {
		if strings.EqualFold(string(item), level) {
			return i
		}
	}
	return -1
}

// abs 返回整数的绝对值。
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
