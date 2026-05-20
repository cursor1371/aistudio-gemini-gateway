// Package models 提供静态模型注册表实现。
//
// 该包是轻量 Gemini 网关的模型真值源：
//   - 支持 embedded（内置）和 custom（用户自定义）两种模型来源
//   - 提供模型名解析（含 alias、thinking suffix 归一化）
//   - 为 /v1beta/models* 接口与 Pipeline 执行链提供统一模型元信息
//
// 该包不依赖 Provider 运行时状态，模型定义完全静态。
package models

import "aistudio-gemini-gateway/service"

// embeddedModels 是内置静态模型集。
// 当配置 models.source=embedded 时，注册表将使用该列表作为模型真值源。
//
// 每个模型的 SupportedGenerationMethods 会在注册时自动推导出对应 SupportedActions；
// Thinking 字段非 nil 时，模型自动标记为 SupportsThinking=true。
// embeddedModels 是内置静态模型集。
var embeddedModels = []*service.ModelInfo{
	// =========================================================================
	// Gemini 2.5 系列
	// =========================================================================
	{
		BaseName:                   "gemini-2.5-pro",
		Name:                       "models/gemini-2.5-pro",
		Object:                     "model",
		Created:                    1750118400,
		OwnedBy:                    "google",
		Type:                       "gemini",
		DisplayName:                "Gemini 2.5 Pro",
		Version:                    "2.5",
		Description:                "Gemini 2.5 Pro",
		InputTokenLimit:            1048576,
		OutputTokenLimit:           65536,
		SupportedGenerationMethods: []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"},
		Thinking: &service.ThinkingSupport{
			Min:            128,
			Max:            32768,
			DynamicAllowed: true,
		},
	},
	{
		BaseName:                   "gemini-2.5-flash",
		Name:                       "models/gemini-2.5-flash",
		Object:                     "model",
		Created:                    1750118400,
		OwnedBy:                    "google",
		Type:                       "gemini",
		DisplayName:                "Gemini 2.5 Flash",
		Version:                    "2.5",
		Description:                "Gemini 2.5 Flash",
		InputTokenLimit:            1048576,
		OutputTokenLimit:           65536,
		SupportedGenerationMethods: []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"},
		Thinking: &service.ThinkingSupport{
			Max:            24576,
			ZeroAllowed:    true,
			DynamicAllowed: true,
		},
	},
	{
		BaseName:                   "gemini-2.5-flash-lite",
		Name:                       "models/gemini-2.5-flash-lite",
		Object:                     "model",
		Created:                    1753142400,
		OwnedBy:                    "google",
		Type:                       "gemini",
		DisplayName:                "Gemini 2.5 Flash Lite",
		Version:                    "2.5",
		Description:                "Gemini 2.5 Flash Lite",
		InputTokenLimit:            1048576,
		OutputTokenLimit:           65536,
		SupportedGenerationMethods: []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"},
		Thinking: &service.ThinkingSupport{
			Max:            24576,
			ZeroAllowed:    true,
			DynamicAllowed: true,
		},
	},
	{
		BaseName:                   "gemini-2.5-flash-image",
		Name:                       "models/gemini-2.5-flash-image",
		Object:                     "model",
		Created:                    1759363200,
		OwnedBy:                    "google",
		Type:                       "gemini",
		DisplayName:                "Gemini 2.5 Flash Image",
		Version:                    "2.5",
		Description:                "Gemini 2.5 Flash Image",
		InputTokenLimit:            1048576,
		OutputTokenLimit:           8192,
		SupportedGenerationMethods: []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"},
	},

	// =========================================================================
	// Gemini 3.x 系列
	// =========================================================================
	{
		BaseName:                   "gemini-3.1-pro-preview",
		Name:                       "models/gemini-3.1-pro-preview",
		Object:                     "model",
		Created:                    1771459200,
		OwnedBy:                    "google",
		Type:                       "gemini",
		DisplayName:                "Gemini 3.1 Pro Preview",
		Version:                    "3.1",
		Description:                "Gemini 3.1 Pro Preview",
		InputTokenLimit:            1048576,
		OutputTokenLimit:           65536,
		SupportedGenerationMethods: []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"},
		Thinking: &service.ThinkingSupport{
			Min:            128,
			Max:            32768,
			DynamicAllowed: true,
			Levels:         []string{"low", "medium", "high"},
		},
	},
	{
		BaseName:                   "gemini-3-flash-preview",
		Name:                       "models/gemini-3-flash-preview",
		Object:                     "model",
		Created:                    1765929600,
		OwnedBy:                    "google",
		Type:                       "gemini",
		DisplayName:                "Gemini 3 Flash Preview",
		Version:                    "3.0",
		Description:                "Gemini 3 Flash Preview",
		InputTokenLimit:            1048576,
		OutputTokenLimit:           65536,
		SupportedGenerationMethods: []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"},
		Thinking: &service.ThinkingSupport{
			Min:            128,
			Max:            32768,
			DynamicAllowed: true,
			Levels:         []string{"minimal", "low", "medium", "high"},
		},
	},
	{
		BaseName:                   "gemini-3.5-flash",
		Name:                       "models/gemini-3.5-flash",
		Object:                     "model",
		Created:                    1765929600,
		OwnedBy:                    "google",
		Type:                       "gemini",
		DisplayName:                "Gemini 3.5 Flash",
		Version:                    "3.5",
		Description:                "Gemini 3.5 Flash",
		InputTokenLimit:            1048576,
		OutputTokenLimit:           65536,
		SupportedGenerationMethods: []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"},
		Thinking: &service.ThinkingSupport{
			Min:            128,
			Max:            32768,
			DynamicAllowed: true,
			Levels:         []string{"minimal", "low", "medium", "high"},
		},
	},
	{
		BaseName:                   "gemini-3.1-flash-lite",
		Name:                       "models/gemini-3.1-flash-lite",
		Object:                     "model",
		Created:                    1776288000,
		OwnedBy:                    "google",
		Type:                       "gemini",
		DisplayName:                "Gemini 3.1 Flash Lite",
		Version:                    "3.1",
		Description:                "Gemini 3.1 Flash Lite",
		InputTokenLimit:            1048576,
		OutputTokenLimit:           65536,
		SupportedGenerationMethods: []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"},
		Thinking: &service.ThinkingSupport{
			Min:            128,
			Max:            32768,
			DynamicAllowed: true,
			Levels:         []string{"minimal", "low", "medium", "high"},
		},
	},

	// =========================================================================
	// Latest 指向系列
	// =========================================================================
	{
		// gemini-pro-latest：除名称外，配置与 gemini-3.1-pro-preview 一致
		BaseName:                   "gemini-pro-latest",
		Name:                       "models/gemini-pro-latest",
		Object:                     "model",
		Created:                    1771459200,
		OwnedBy:                    "google",
		Type:                       "gemini",
		DisplayName:                "Gemini Pro Latest",
		Version:                    "3.1",
		Description:                "Gemini Pro Latest",
		InputTokenLimit:            1048576,
		OutputTokenLimit:           65536,
		SupportedGenerationMethods: []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"},
		Thinking: &service.ThinkingSupport{
			Min:            128,
			Max:            32768,
			DynamicAllowed: true,
			Levels:         []string{"low", "medium", "high"},
		},
	},
	{
		// gemini-flash-latest：除名称外，配置与 gemini-3.5-flash 一致
		BaseName:                   "gemini-flash-latest",
		Name:                       "models/gemini-flash-latest",
		Object:                     "model",
		Created:                    1765929600,
		OwnedBy:                    "google",
		Type:                       "gemini",
		DisplayName:                "Gemini Flash Latest",
		Version:                    "3.5",
		Description:                "Gemini Flash Latest",
		InputTokenLimit:            1048576,
		OutputTokenLimit:           65536,
		SupportedGenerationMethods: []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"},
		Thinking: &service.ThinkingSupport{
			Min:            128,
			Max:            32768,
			DynamicAllowed: true,
			Levels:         []string{"minimal", "low", "medium", "high"},
		},
	},
	{
		// gemini-flash-lite-latest：除名称外，配置与 gemini-3.1-flash-lite 一致
		BaseName:                   "gemini-flash-lite-latest",
		Name:                       "models/gemini-flash-lite-latest",
		Object:                     "model",
		Created:                    1776288000,
		OwnedBy:                    "google",
		Type:                       "gemini",
		DisplayName:                "Gemini Flash-Lite Latest",
		Version:                    "3.1",
		Description:                "Gemini Flash-Lite Latest",
		InputTokenLimit:            1048576,
		OutputTokenLimit:           65536,
		SupportedGenerationMethods: []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"},
		Thinking: &service.ThinkingSupport{
			Min:            128,
			Max:            32768,
			DynamicAllowed: true,
			Levels:         []string{"minimal", "low", "medium", "high"},
		},
	},
}
