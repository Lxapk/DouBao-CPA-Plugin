package main

import (
	"encoding/json"
	"math/rand"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Account selection.
//
// CPA hands over the candidate list per request and, when the plugin declares the
// scheduler capability, lets it pick one. Four strategies, each answering a
// different question:
//
//	round_robin  spread load evenly          — the safe default
//	fill_first   drain one account first     — what you want when the first
//	                                           account has the larger quota
//	random       no ordering at all          — avoids a predictable pattern
//	by_usage     fewest calls first          — evens out consumption without a
//	                                           cursor, so it survives restarts
const (
	routingRoundRobin = "round_robin"
	routingFillFirst  = "fill_first"
	routingRandom     = "random"
	routingByUsage    = "by_usage"
)

// routingOption is one strategy, with the text the panel shows under the switch.
type routingOption struct {
	Value       string
	Label       string
	Description string
	Tradeoff    string
}

var routingOptions = []routingOption{
	{
		Value:       routingRoundRobin,
		Label:       "轮巡",
		Description: "按顺序轮流使用每个账号。",
		Tradeoff:    "负载最均匀，但请求会分散到所有账号上。",
	},
	{
		Value:       routingFillFirst,
		Label:       "用满再换",
		Description: "始终使用第一个可用账号，直到它失败或进入冷却。",
		Tradeoff:    "配额集中在少数账号，账号多时其余账号长期闲置。",
	},
	{
		Value:       routingRandom,
		Label:       "随机",
		Description: "每次请求随机挑一个可用账号。",
		Tradeoff:    "没有可预测的顺序，难以复现某次调用用了哪个账号。",
	},
	{
		Value:       routingByUsage,
		Label:       "按用量",
		Description: "优先选择调用次数最少的账号。",
		Tradeoff:    "比轮巡更均衡地消耗配额，但依赖本地记录的调用次数。",
	},
}

// normalizeStrategy coerces an unknown value to the default.
func normalizeStrategy(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	for _, opt := range routingOptions {
		if opt.Value == value {
			return value
		}
	}
	return routingRoundRobin
}

func routingLabel(value string) string {
	for _, opt := range routingOptions {
		if opt.Value == value {
			return opt.Label
		}
	}
	return routingRoundRobin
}

// rotation tracks the round-robin cursor.
//
// The host's candidate order is stable, so a cursor over the identifiers is
// enough. It is keyed by account handle rather than position so removing one
// account does not shift every other position.
type rotationState struct {
	mu   sync.Mutex
	last string
}

var rotation = &rotationState{}

func (r *rotationState) nextAfter(order []string) string {
	if len(order) == 0 {
		return ""
	}
	r.mu.Lock()
	last := r.last
	r.mu.Unlock()

	idx := -1
	for i, id := range order {
		if id == last {
			idx = i
			break
		}
	}
	next := order[(idx+1)%len(order)]

	r.mu.Lock()
	r.last = next
	r.mu.Unlock()
	return next
}

func (r *rotationState) remember(id string) {
	r.mu.Lock()
	r.last = id
	r.mu.Unlock()
}

func resetRotation() {
	rotation.mu.Lock()
	rotation.last = ""
	rotation.mu.Unlock()
}

// candidateKey extracts the account handle from a scheduler candidate.
//
// The host reports the credential both as an auth id and as a set of attributes;
// the attributes carry the handle this plugin uses for its counters, and the id
// is the fallback when they are absent.
func candidateKey(candidate pluginapi.SchedulerAuthCandidate) string {
	for _, name := range []string{"sec_user_id", "uid"} {
		if v := strings.TrimSpace(candidate.Attributes[name]); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(candidate.Attributes["realm"]); v != "" {
		_ = v // realm alone does not identify an account
	}
	return strings.TrimSpace(candidate.ID)
}

// relayableCandidates drops candidates this plugin must not route to.
//
// The host only offers credentials it considers available, so the one thing left
// to filter on is the explicit "disabled" status it reports per candidate. An
// empty status means "no opinion" and is kept.
func relayableCandidates(candidates []pluginapi.SchedulerAuthCandidate) []pluginapi.SchedulerAuthCandidate {
	out := make([]pluginapi.SchedulerAuthCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		switch strings.ToLower(strings.TrimSpace(candidate.Status)) {
		case "disabled", "unavailable", "blocked", "expired", "invalid":
			continue
		}
		out = append(out, candidate)
	}
	return out
}

// pickCandidate applies the configured strategy.
func pickCandidate(candidates []pluginapi.SchedulerAuthCandidate) pluginapi.SchedulerAuthCandidate {
	cfg := state.settings.get()
	strategy := normalizeStrategy(cfg.Routing.Strategy)

	if len(candidates) == 1 {
		return candidates[0]
	}

	switch strategy {
	case routingFillFirst:
		// The host's order is its own priority order, so the first entry is the
		// one to drain.
		return candidates[0]

	case routingRandom:
		return candidates[rand.Intn(len(candidates))]

	case routingByUsage:
		best := candidates[0]
		bestUsed := int64(-1)
		for _, candidate := range candidates {
			used := stats.callCount(candidateKey(candidate), candidate.ID)
			if bestUsed < 0 || used < bestUsed {
				best = candidate
				bestUsed = used
			}
		}
		return best

	default: // routingRoundRobin
		order := make([]string, 0, len(candidates))
		byID := map[string]pluginapi.SchedulerAuthCandidate{}
		for _, candidate := range candidates {
			id := strings.TrimSpace(candidate.ID)
			if id == "" {
				id = candidateKey(candidate)
			}
			order = append(order, id)
			byID[id] = candidate
		}
		if candidate, ok := byID[rotation.nextAfter(order)]; ok {
			return candidate
		}
		return candidates[0]
	}
}

// schedulerPick answers scheduler.pick.
//
// Handled=false hands the decision back to the host, which is correct when this
// plugin has nothing to choose from: the host then reports its own "no auth
// available" in its own wording.
func schedulerPick(request []byte) ([]byte, error) {
	var req pluginapi.SchedulerPickRequest
	if len(request) > 0 {
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}

	candidates := relayableCandidates(req.Candidates)
	if len(candidates) == 0 {
		return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
	}

	chosen := pickCandidate(candidates)
	if strings.TrimSpace(chosen.ID) == "" {
		return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
	}
	return okEnvelope(pluginapi.SchedulerPickResponse{AuthID: chosen.ID, Handled: true})
}

// routingStatusJSON describes the current strategy for the panel.
func routingStatusJSON() map[string]any {
	cfg := state.settings.get()
	strategy := normalizeStrategy(cfg.Routing.Strategy)

	options := make([]map[string]any, 0, len(routingOptions))
	for _, opt := range routingOptions {
		options = append(options, map[string]any{
			"value":       opt.Value,
			"label":       opt.Label,
			"description": opt.Description,
			"tradeoff":    opt.Tradeoff,
		})
	}

	rotation.mu.Lock()
	last := rotation.last
	rotation.mu.Unlock()

	usable := 0
	for _, entry := range accounts.get() {
		if !entry.Disabled {
			usable++
		}
	}

	return map[string]any{
		"strategy":       strategy,
		"strategy_label": routingLabel(strategy),
		"options":        options,
		"last":           last,
		"usable":         usable,
	}
}

// applyRoutingConfig stores a strategy chosen in the panel.
//
// The change is applied live and written to the plugin's overlay, so it survives a
// restart without the operator having to edit config.yaml.
func applyRoutingConfig(strategy string) map[string]any {
	next := state.settings.get()
	next.Routing.Strategy = normalizeStrategy(strategy)
	state.settings.set(next)

	if errPersist := persistSettings(map[string]json.RawMessage{
		"routing": json.RawMessage(`{"strategy":"` + next.Routing.Strategy + `"}`),
	}); errPersist != nil {
		logError("doubao: 写入路由策略失败：" + errPersist.Error())
	}
	logInfo("doubao: 路由策略已切换为 " + routingLabel(next.Routing.Strategy))
	return routingStatusJSON()
}

// routingNextHint explains what the next request will do.
func routingNextHint() string {
	switch normalizeStrategy(state.settings.get().Routing.Strategy) {
	case routingFillFirst:
		return "下一个请求会用第一个可用账号"
	case routingRandom:
		return "下一个请求随机挑一个可用账号"
	case routingByUsage:
		return "下一个请求会用调用次数最少的账号"
	default:
		return "下一个请求会用在轮巡中排在后面的账号"
	}
}
