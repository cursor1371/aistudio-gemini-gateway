package selector

import (
	"sort"
	"strings"
	"time"

	"aistudio-gemini-gateway/service"
)

// candidateSummary 描述本次选择过程中候选 Provider 的总体情况。
// 主要用于在"没有可用 Provider"时构造更准确的错误信息。
type candidateSummary struct {
	model            string
	totalEligible    int
	coolingCount     int
	earliestCooldown time.Time
}

// collectReadyProviders 从候选列表中筛选当前可用的 Provider。
// 不再按 Provider 的模型声明过滤；selector 自行处理 tried / cooling / priority。
func collectReadyProviders(model string, providers []*service.RuntimeProvider, tried map[string]struct{}, now time.Time) ([]*service.RuntimeProvider, candidateSummary) {
	summary := candidateSummary{
		model: strings.TrimSpace(model),
	}
	if len(providers) == 0 {
		return nil, summary
	}

	triedSet := normalizeTried(tried)
	ready := make([]*service.RuntimeProvider, 0, len(providers))

	for _, item := range providers {
		if item == nil {
			continue
		}
		if containsTried(triedSet, item.ID) {
			continue
		}

		summary.totalEligible++

		state, cooldownUntil := effectiveProviderState(item, now)
		switch state {
		case service.ProviderStateActive:
			ready = append(ready, item)
		case service.ProviderStateCooling:
			summary.coolingCount++
			if !cooldownUntil.IsZero() && (summary.earliestCooldown.IsZero() || cooldownUntil.Before(summary.earliestCooldown)) {
				summary.earliestCooldown = cooldownUntil
			}
		default:
			// disabled / disconnected / invalid 状态不计入 ready。
		}
	}

	if len(ready) == 0 {
		return nil, summary
	}

	sortReadyProviders(ready)

	// 只在当前可用集合里选择最高优先级层。
	bestPriority := ready[0].Priority
	filtered := ready[:0]
	for _, item := range ready {
		if item.Priority == bestPriority {
			filtered = append(filtered, item)
		}
	}
	return filtered, summary
}

// selectionErrorFromSummary 根据候选汇总信息生成选路错误。
func selectionErrorFromSummary(model string, summary candidateSummary, now time.Time) error {
	model = strings.TrimSpace(model)

	if summary.totalEligible > 0 && summary.coolingCount == summary.totalEligible && !summary.earliestCooldown.IsZero() {
		resetIn := summary.earliestCooldown.Sub(now)
		if resetIn < 0 {
			resetIn = 0
		}
		return service.NewProviderCooldownError(
			"all eligible providers are cooling down",
			nil,
			service.WithModel(model),
			service.WithRetryable(true),
			service.WithRetryAfter(resetIn),
			service.WithPublicMessage("所有可用 Provider 当前均处于冷却中"),
			service.WithMetadata(map[string]any{
				"model":         model,
				"reset_in":      resetIn.String(),
				"reset_seconds": int64(resetIn.Seconds()),
			}),
		)
	}

	return service.NewProviderUnavailableError(
		"no eligible provider available",
		nil,
		service.WithModel(model),
		service.WithPublicMessage("当前没有可用的执行通道"),
		service.WithMetadata(map[string]any{
			"model":              model,
			"eligible_providers": summary.totalEligible,
			"cooling_providers":  summary.coolingCount,
		}),
	)
}

// effectiveProviderState 给出 selector 视角下的有效状态。
// 允许把"已过期但定时器尚未触发"的 cooling 视为 active，
// 但不会写回 registry，保证读路径不隐式触发写操作。
func effectiveProviderState(p *service.RuntimeProvider, now time.Time) (service.ProviderState, time.Time) {
	if p == nil {
		return service.ProviderStateDisconnected, time.Time{}
	}

	switch p.State {
	case "", service.ProviderStateActive:
		return service.ProviderStateActive, time.Time{}
	case service.ProviderStateCooling:
		if p.CooldownUntil.IsZero() || !p.CooldownUntil.After(now) {
			return service.ProviderStateActive, time.Time{}
		}
		return service.ProviderStateCooling, p.CooldownUntil
	case service.ProviderStateDisabled:
		return service.ProviderStateDisabled, time.Time{}
	case service.ProviderStateDisconnected:
		return service.ProviderStateDisconnected, time.Time{}
	default:
		return service.ProviderStateDisconnected, time.Time{}
	}
}

func sortReadyProviders(in []*service.RuntimeProvider) {
	sort.SliceStable(in, func(i, j int) bool {
		left := in[i]
		right := in[j]
		if left == nil && right == nil {
			return false
		}
		if left == nil {
			return false
		}
		if right == nil {
			return true
		}
		if left.Priority != right.Priority {
			return left.Priority > right.Priority
		}
		if !left.ConnectedAt.Equal(right.ConnectedAt) {
			return left.ConnectedAt.Before(right.ConnectedAt)
		}
		return strings.ToLower(left.ID) < strings.ToLower(right.ID)
	})
}

func normalizeTried(tried map[string]struct{}) map[string]struct{} {
	if len(tried) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(tried))
	for key := range tried {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		out[key] = struct{}{}
	}
	return out
}

func containsTried(tried map[string]struct{}, providerID string) bool {
	if len(tried) == 0 {
		return false
	}
	_, ok := tried[strings.ToLower(strings.TrimSpace(providerID))]
	return ok
}

func cursorKeyForModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return "*"
	}
	return model
}
