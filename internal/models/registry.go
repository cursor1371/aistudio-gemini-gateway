package models

import (
	"fmt"
	"sort"
	"strings"

	"aistudio-gemini-gateway/internal/config"
	"aistudio-gemini-gateway/service"
)

// AliasRule 表示一个模型别名规则。
// Target 是 alias 指向的真实模型名。
// Expose 为 true 时，该 alias 会以独立条目出现在 /v1beta/models 列表中。
type AliasRule struct {
	Target string
	Expose bool
}

// ResolveResult 表示一次模型名解析的结果。
type ResolveResult struct {
	// Requested 是原始请求中传入的模型名（未归一化）。
	Requested string
	// Canonical 是归一化后的真实模型名，用于后续执行链。
	Canonical string
	// Aliased 表示本次解析是否命中了 alias。
	Aliased bool
	// ExposedAlias 表示命中的 alias 是否配置了 Expose=true。
	ExposedAlias bool
	// Info 是解析命中的模型元信息快照；若未命中则为 nil。
	Info *service.ModelInfo
}

// Registry 是静态模型注册表。
// 它是整个网关的模型真值源，负责：
//  1. 维护全量静态模型定义
//  2. 维护模型别名映射（含内置兼容别名和用户配置别名）
//  3. 提供模型名解析（支持 models/ 前缀、thinking suffix、alias 命中）
//  4. 为 /v1beta/models* 接口提供可见模型列表
//
// Provider 不再声明模型支持范围，所有在线 Provider 默认支持注册表中全部模型。
type Registry struct {
	// models 以归一化后的 BaseName 为键，存储全量静态模型定义。
	models map[string]*service.ModelInfo
	// aliases 以归一化后的 alias 名为键，存储别名规则。
	aliases map[string]AliasRule
	// aliasViews 存储 Expose=true 的 alias 视图模型，用于 List() 输出。
	aliasViews map[string]*service.ModelInfo
	// list 是预构建的对外可见模型列表（canonical models + exposed aliases）。
	list []*service.ModelInfo
}

// builtInAliases 是内置兼容别名。
// 这些别名独立于用户配置，用于保持关键模型名的向后兼容。
var builtInAliases = []config.ModelAlias{
	{
		Alias:  "gemini-2.5-flash-image-preview",
		Target: "gemini-2.5-flash-image",
		Expose: false,
	},
}

// New 根据配置创建静态模型注册表。
//
// 支持两种模型来源：
//   - embedded：使用内置静态模型集
//   - custom：使用配置中的 entries 自定义模型集
//
// 创建过程：
//  1. 根据 source 加载基础模型列表
//  2. 对每个模型执行 prepareModel 归一化
//  3. 若为 embedded 模式，补齐内置兼容 alias
//  4. 应用用户配置 alias
//  5. 构建对外可见模型列表
func New(cfg config.ModelsConfig) (*Registry, error) {
	source := strings.ToLower(strings.TrimSpace(cfg.Source))
	if source == "" {
		source = "embedded"
	}

	r := &Registry{
		models:     make(map[string]*service.ModelInfo),
		aliases:    make(map[string]AliasRule),
		aliasViews: make(map[string]*service.ModelInfo),
	}

	// 1. 根据来源加载基础模型。
	var baseModels []*service.ModelInfo

	switch source {
	case "embedded":
		for _, item := range embeddedModels {
			if item != nil {
				baseModels = append(baseModels, item.Clone())
			}
		}

	case "custom":
		for _, entry := range cfg.Entries {
			model, err := modelFromEntry(entry)
			if err != nil {
				return nil, err
			}
			baseModels = append(baseModels, model)
		}

	default:
		return nil, fmt.Errorf("unsupported models source: %s", source)
	}

	// 2. 归一化并注册模型。
	for _, item := range baseModels {
		prepared := prepareModel(item)
		if prepared == nil {
			continue
		}
		if prepared.BaseName == "" {
			return nil, fmt.Errorf("model base name cannot be empty")
		}
		if _, exists := r.models[prepared.BaseName]; exists {
			return nil, fmt.Errorf("duplicate model name: %s", prepared.BaseName)
		}
		r.models[prepared.BaseName] = prepared
	}

	// 3. embedded 模式下补齐内置兼容 alias。
	if source == "embedded" {
		for _, alias := range builtInAliases {
			if err := r.applyAlias(alias); err != nil {
				return nil, err
			}
		}
	}

	// 4. 应用用户配置 alias。
	for _, alias := range cfg.Aliases {
		if err := r.applyAlias(alias); err != nil {
			return nil, err
		}
	}

	// 5. 构建对外可见模型列表。
	r.rebuildList()
	return r, nil
}

// Resolve 解析模型名，返回归一化后的 canonical 模型名和元信息安全副本。
//
// 解析规则：
//  1. 去掉 models/ 前缀
//  2. 去掉 thinking suffix，如 gemini-2.5-pro(8192)
//  3. 若命中 alias，则解析到 alias 指向的真实模型
//  4. 若既没有命中 alias 也不在注册表中，Info 为 nil
//
// 该方法返回 Info 的深拷贝，适合对外公开 API 和 SDK 调用。
// 内部执行热路径应优先使用 ResolveForExecution 避免不必要的 Clone。
func (r *Registry) Resolve(model string) ResolveResult {
	result := r.resolveReadOnly(model)
	if result.Info != nil {
		result.Info = result.Info.Clone()
	}
	return result
}

// resolveReadOnly 是内部只读解析实现。
// 返回的 Info 是注册表中的原始指针，调用方不得修改。
// 该方法供 ResolveForExecution 和 Resolve 共同使用。
func (r *Registry) resolveReadOnly(model string) ResolveResult {
	result := ResolveResult{
		Requested: strings.TrimSpace(model),
	}
	if r == nil {
		return result
	}

	key := normalizeModelKey(model)
	if key == "" {
		return result
	}

	// 优先检查 alias。
	if alias, ok := r.aliases[key]; ok {
		result.Aliased = true
		result.ExposedAlias = alias.Expose
		result.Canonical = alias.Target
		if info, exists := r.models[alias.Target]; exists && info != nil {
			result.Info = info
		}
		return result
	}

	// 直接匹配 canonical model。
	result.Canonical = key
	if info, exists := r.models[key]; exists && info != nil {
		result.Info = info
	}
	return result
}

// ResolveForExecution 是内部执行链专用的模型解析入口。
// 与 Resolve 的区别：
//   - 返回的 ModelInfo 是注册表中的只读引用，不做 Clone
//   - 仅适用于 pipeline 等内部执行热路径
//   - 调用方必须保证不修改返回的 ModelInfo
//
// 该函数位于 internal/models 包，模块外部无法直接 import 使用，
// 由 gateway 装配层把它桥接到 pipeline.ResolveModelFunc。
func ResolveForExecution(r *Registry, model string) (string, *service.ModelInfo, bool) {
	if r == nil {
		return "", nil, false
	}

	result := r.resolveReadOnly(model)
	if strings.TrimSpace(result.Canonical) == "" || result.Info == nil {
		return "", nil, false
	}
	return result.Canonical, result.Info, true
}

// Get 获取单个模型的元信息快照。
//
// 行为：
//   - 若请求的是 Expose=true 的 alias，返回 alias 独立视图
//   - 若请求的是 Expose=false 的 alias，返回其 target 模型的视图
//   - 若请求的是 canonical model，返回 canonical 视图
//   - 不存在时返回 nil, false
func (r *Registry) Get(model string) (*service.ModelInfo, bool) {
	if r == nil {
		return nil, false
	}

	key := normalizeModelKey(model)
	if key == "" {
		return nil, false
	}

	// 优先检查 alias。
	if alias, ok := r.aliases[key]; ok {
		if alias.Expose {
			if item := r.aliasViews[key]; item != nil {
				return item.Clone(), true
			}
		}
		if item := r.models[alias.Target]; item != nil {
			return item.Clone(), true
		}
		return nil, false
	}

	// 直接匹配 canonical model。
	item := r.models[key]
	if item == nil {
		return nil, false
	}
	return item.Clone(), true
}

// List 返回对外可见的模型列表。
// 包含全部 canonical models 和 Expose=true 的 alias models，
// 按 DisplayName / BaseName 字母序排列。
func (r *Registry) List() []*service.ModelInfo {
	if r == nil || len(r.list) == 0 {
		return nil
	}
	out := make([]*service.ModelInfo, 0, len(r.list))
	for _, item := range r.list {
		if item != nil {
			out = append(out, item.Clone())
		}
	}
	return out
}

// applyAlias 注册一个模型别名。
// 该方法会校验 alias/target 非空、不循环、target 存在、alias 不重复。
func (r *Registry) applyAlias(alias config.ModelAlias) error {
	if r == nil {
		return fmt.Errorf("model registry is nil")
	}

	aliasName := normalizeModelKey(alias.Alias)
	targetName := normalizeModelKey(alias.Target)

	if aliasName == "" || targetName == "" {
		return fmt.Errorf("model alias and target cannot be empty")
	}
	if aliasName == targetName {
		return fmt.Errorf("model alias and target cannot be the same: %s", aliasName)
	}
	target, ok := r.models[targetName]
	if !ok || target == nil {
		return fmt.Errorf("model alias target not found: %s", targetName)
	}
	if _, exists := r.aliases[aliasName]; exists {
		return fmt.Errorf("duplicate model alias: %s", aliasName)
	}

	r.aliases[aliasName] = AliasRule{
		Target: targetName,
		Expose: alias.Expose,
	}
	if alias.Expose {
		r.aliasViews[aliasName] = cloneAliasModel(target, aliasName, true)
	}
	return nil
}

// rebuildList 重新构建对外可见模型列表。
func (r *Registry) rebuildList() {
	items := make([]*service.ModelInfo, 0, len(r.models)+len(r.aliasViews))
	for _, item := range r.models {
		if item != nil {
			items = append(items, item.Clone())
		}
	}
	for _, item := range r.aliasViews {
		if item != nil {
			items = append(items, item.Clone())
		}
	}
	sortModelInfos(items)
	r.list = items
}

// modelFromEntry 从用户自定义配置 ModelEntry 构造 ModelInfo。
func modelFromEntry(entry config.ModelEntry) (*service.ModelInfo, error) {
	baseName := normalizeModelKey(entry.Name)
	if baseName == "" {
		return nil, fmt.Errorf("custom model name cannot be empty")
	}

	thinking := convertThinking(entry.Thinking)

	// 合并 supported-actions 和 supported-generation-methods 的推导结果。
	actions := parseSupportedActions(entry.SupportedActions)
	derivedActions := actionsFromGenerationMethods(entry.SupportedGenerationMethods)
	actions = mergeActions(actions, derivedActions)

	methods := normalizeNonEmptyStrings(entry.SupportedGenerationMethods)
	if len(methods) == 0 {
		methods = generationMethodsFromActions(actions)
	} else {
		methods = mergeGenerationMethods(methods, generationMethodsFromActions(actions))
	}

	info := &service.ModelInfo{
		Name:                       "models/" + baseName,
		BaseName:                   baseName,
		DisplayName:                strings.TrimSpace(entry.DisplayName),
		Description:                strings.TrimSpace(entry.Description),
		Version:                    strings.TrimSpace(entry.Version),
		InputTokenLimit:            entry.InputTokenLimit,
		OutputTokenLimit:           entry.OutputTokenLimit,
		SupportedActions:           actions,
		SupportedGenerationMethods: methods,
		SupportedInputModalities:   normalizeNonEmptyStrings(entry.SupportedInputModalities),
		SupportedOutputModalities:  normalizeNonEmptyStrings(entry.SupportedOutputModalities),
		Thinking:                   thinking,
		SupportsThinking:           thinking != nil,
		UserDefined:                true,
		Object:                     "model",
		Type:                       "gemini",
	}
	return prepareModel(info), nil
}

// convertThinking 将配置层 ModelThinkingConfig 转换为业务层 ThinkingSupport。
func convertThinking(in *config.ModelThinkingConfig) *service.ThinkingSupport {
	if in == nil {
		return nil
	}
	return &service.ThinkingSupport{
		Min:            in.Min,
		Max:            in.Max,
		ZeroAllowed:    in.ZeroAllowed,
		DynamicAllowed: in.DynamicAllowed,
		Levels:         normalizeLowercaseStrings(in.Levels),
	}
}

// prepareModel 对模型信息执行归一化与默认值补齐。
// 包括：
//   - BaseName / Name 归一化
//   - Object / Type 默认值
//   - ContextLength / MaxCompletionTokens 自动推导
//   - SupportedActions 与 SupportedGenerationMethods 互推
//   - SupportsThinking 标记
//   - DisplayName 回退
func prepareModel(model *service.ModelInfo) *service.ModelInfo {
	if model == nil {
		return nil
	}

	out := model.Clone()
	if out == nil {
		return nil
	}

	base := normalizeModelKey(firstNonEmpty(out.BaseName, out.Name))
	if base == "" {
		return nil
	}
	out.BaseName = base

	if strings.TrimSpace(out.Name) == "" {
		out.Name = "models/" + base
	}
	if strings.TrimSpace(out.Object) == "" {
		out.Object = "model"
	}
	if strings.TrimSpace(out.Type) == "" {
		out.Type = "gemini"
	}
	if out.ContextLength <= 0 && out.InputTokenLimit > 0 {
		out.ContextLength = out.InputTokenLimit
	}
	if out.MaxCompletionTokens <= 0 && out.OutputTokenLimit > 0 {
		out.MaxCompletionTokens = out.OutputTokenLimit
	}
	if out.Thinking != nil {
		out.SupportsThinking = true
	}
	if len(out.SupportedActions) == 0 {
		out.SupportedActions = actionsFromGenerationMethods(out.SupportedGenerationMethods)
	}
	if len(out.SupportedGenerationMethods) == 0 {
		out.SupportedGenerationMethods = generationMethodsFromActions(out.SupportedActions)
	}
	if strings.TrimSpace(out.DisplayName) == "" {
		out.DisplayName = out.BaseName
	}
	return out
}

// cloneAliasModel 基于 target 模型创建一个 alias 视图。
// alias 视图继承 target 的全部信息，但 BaseName / Name 替换为 alias 名，
// 并在 Metadata 中标注来源信息。
func cloneAliasModel(target *service.ModelInfo, alias string, expose bool) *service.ModelInfo {
	if target == nil {
		return nil
	}
	out := target.Clone()
	out.BaseName = alias
	out.Name = "models/" + alias
	if out.Metadata == nil {
		out.Metadata = make(map[string]any)
	}
	out.Metadata["resolved_model"] = target.BaseName
	out.Metadata["alias"] = true
	out.Metadata["alias_exposed"] = expose
	return out
}

// parseSupportedActions 从字符串列表解析 Action 集合。
func parseSupportedActions(in []string) []service.Action {
	if len(in) == 0 {
		return nil
	}

	seen := make(map[service.Action]struct{}, len(in))
	out := make([]service.Action, 0, len(in))

	for _, item := range in {
		switch strings.TrimSpace(item) {
		case "generateContent":
			if _, ok := seen[service.ActionGenerateContent]; !ok {
				seen[service.ActionGenerateContent] = struct{}{}
				out = append(out, service.ActionGenerateContent)
			}
		case "streamGenerateContent":
			if _, ok := seen[service.ActionStreamGenerateContent]; !ok {
				seen[service.ActionStreamGenerateContent] = struct{}{}
				out = append(out, service.ActionStreamGenerateContent)
			}
		case "countTokens":
			if _, ok := seen[service.ActionCountTokens]; !ok {
				seen[service.ActionCountTokens] = struct{}{}
				out = append(out, service.ActionCountTokens)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// actionsFromGenerationMethods 从 Gemini generation methods 推导 Action 集合。
// 注意：generateContent 同时推导出 ActionGenerateContent 和 ActionStreamGenerateContent。
func actionsFromGenerationMethods(methods []string) []service.Action {
	if len(methods) == 0 {
		return nil
	}

	seen := make(map[service.Action]struct{}, 4)
	out := make([]service.Action, 0, 4)

	add := func(action service.Action) {
		if !action.Valid() {
			return
		}
		if _, ok := seen[action]; ok {
			return
		}
		seen[action] = struct{}{}
		out = append(out, action)
	}

	for _, method := range methods {
		switch strings.TrimSpace(method) {
		case "generateContent":
			add(service.ActionGenerateContent)
			add(service.ActionStreamGenerateContent)
		case "countTokens":
			add(service.ActionCountTokens)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// generationMethodsFromActions 从 Action 集合反推 generation methods。
func generationMethodsFromActions(actions []service.Action) []string {
	if len(actions) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, 4)
	out := make([]string, 0, 4)

	add := func(method string) {
		method = strings.TrimSpace(method)
		if method == "" {
			return
		}
		if _, ok := seen[method]; ok {
			return
		}
		seen[method] = struct{}{}
		out = append(out, method)
	}

	for _, action := range actions {
		switch action {
		case service.ActionGenerateContent, service.ActionStreamGenerateContent:
			add("generateContent")
		case service.ActionCountTokens:
			add("countTokens")
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// mergeActions 合并两个 Action 切片，去重保持顺序。
func mergeActions(base, extra []service.Action) []service.Action {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	seen := make(map[service.Action]struct{}, len(base)+len(extra))
	out := make([]service.Action, 0, len(base)+len(extra))

	for _, item := range base {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	for _, item := range extra {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

// mergeGenerationMethods 合并两个 generation method 切片，去重保持顺序。
func mergeGenerationMethods(base, extra []string) []string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))

	for _, item := range base {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	for _, item := range extra {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

// sortModelInfos 按 DisplayName / BaseName 字母序对模型列表排序。
func sortModelInfos(models []*service.ModelInfo) {
	sort.SliceStable(models, func(i, j int) bool {
		left := models[i]
		right := models[j]
		if left == nil && right == nil {
			return false
		}
		if left == nil {
			return false
		}
		if right == nil {
			return true
		}

		leftName := strings.ToLower(firstNonEmpty(left.DisplayName, left.BaseName))
		rightName := strings.ToLower(firstNonEmpty(right.DisplayName, right.BaseName))
		if leftName != rightName {
			return leftName < rightName
		}
		return strings.ToLower(left.BaseName) < strings.ToLower(right.BaseName)
	})
}

// normalizeModelKey 将模型名归一化为注册表内部使用的键。
// 规则：
//  1. 去掉 models/ 前缀
//  2. 去掉 thinking suffix，如 gemini-2.5-pro(8192)
//  3. 转为小写
func normalizeModelKey(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}

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

// normalizeNonEmptyStrings 对字符串切片做 trim 和去重。
func normalizeNonEmptyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeLowercaseStrings 对字符串切片做 trim、小写转换和去重。
func normalizeLowercaseStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
