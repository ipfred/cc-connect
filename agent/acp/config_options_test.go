package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func droidConfigOptions(t *testing.T) []acpConfigOption {
	t.Helper()
	var res struct {
		ConfigOptions []acpConfigOption `json:"configOptions"`
	}
	if err := json.Unmarshal(fixtureField(t, loadDroidFixture(t, "session_new_response"), "result"), &res); err != nil {
		t.Fatal(err)
	}
	return res.ConfigOptions
}

func TestAgent_ConfigOptions_ModelAndReasoning(t *testing.T) {
	a := &Agent{}
	if len(a.AvailableModels(context.Background())) != 0 || len(a.AvailableReasoningEfforts()) != 0 {
		t.Fatal("lists must be empty before any handshake")
	}
	a.reportConfigOptions(droidConfigOptions(t))

	models := a.AvailableModels(context.Background())
	if len(models) != 5 || models[3].Name != "claude-haiku-5-5" || !strings.Contains(models[3].Desc, "Haiku 5.5") {
		t.Fatalf("models = %+v", models)
	}
	if got := strings.Join(a.AvailableReasoningEfforts(), ","); got != "off,low,medium,high,xhigh,max" {
		t.Fatalf("efforts = %s", got)
	}
	if a.GetModel() != "claude-sonnet-5-5" || a.GetReasoningEffort() != "high" {
		t.Fatalf("current = %q / %q", a.GetModel(), a.GetReasoningEffort())
	}

	a.SetModel("Haiku 5.5")
	if got := a.GetModel(); got != "claude-haiku-5-5" {
		t.Fatalf("SetModel by display name must resolve to the id, got %q", got)
	}
	a.SetReasoningEffort("LOW")
	if got := a.GetReasoningEffort(); got != "low" {
		t.Fatalf("effort = %q", got)
	}
	if m, e := a.desiredConfigValues(); m != "claude-haiku-5-5" || e != "low" {
		t.Fatalf("desired = %q %q", m, e)
	}
}

func TestNew_ReadsModelAndReasoningOptions(t *testing.T) {
	a, err := New(map[string]any{"cmd": "true", "model": "gpt-5.5", "reasoning_effort": "max"})
	if err != nil {
		t.Skipf("'true' not available: %v", err)
	}
	ag := a.(*Agent)
	if ag.GetModel() != "gpt-5.5" || ag.GetReasoningEffort() != "max" {
		t.Fatalf("model/effort from opts = %q / %q", ag.GetModel(), ag.GetReasoningEffort())
	}
	opts := ag.WorkspaceAgentOptions()
	if opts["model"] != "gpt-5.5" || opts["reasoning_effort"] != "max" {
		t.Fatalf("workspace options must carry model/effort: %v", opts)
	}
}

func TestSession_ApplyDesiredConfig_SendsSetConfigOption(t *testing.T) {
	a := &Agent{}
	s, wResp, rReq := newTestSession(t, a)
	setRes := fixtureField(t, loadDroidFixture(t, "set_config_option_response"), "result")
	fs := startFakeServer(t, wResp, rReq, func(m rpcMsg) string {
		if m.Method == "session/set_config_option" {
			return string(setRes)
		}
		return `{}`
	})
	s.absorbConfigOptions(droidConfigOptions(t))
	a.SetModel("claude-haiku-5-5")

	s.applyDesiredConfig()

	req := fs.waitFor(t, func(m rpcMsg) bool { return m.Method == "session/set_config_option" })
	var p struct {
		SessionID string `json:"sessionId"`
		ConfigID  string `json:"configId"`
		Value     string `json:"value"`
	}
	_ = json.Unmarshal(req.Params, &p)
	if p.SessionID != "test-session-id" || p.ConfigID != "model" || p.Value != "claude-haiku-5-5" {
		t.Fatalf("set_config_option params = %+v", p)
	}
	if got := s.GetModel(); got != "claude-haiku-5-5" {
		t.Fatalf("session model after set = %q", got)
	}

	// Already applied: a second call must not issue another RPC.
	s.applyDesiredConfig()
	fs.mu.Lock()
	n := 0
	for _, m := range fs.lines {
		if m.Method == "session/set_config_option" {
			n++
		}
	}
	fs.mu.Unlock()
	if n != 1 {
		t.Fatalf("set_config_option sent %d times, want 1", n)
	}
}

func TestSession_ConfigOptionUpdateNotification(t *testing.T) {
	a := &Agent{}
	s, _, _ := newTestSession(t, a)
	s.absorbConfigOptions(droidConfigOptions(t))
	upd := loadDroidFixture(t, "config_option_update")
	s.onNotification("session/update", fixtureField(t, upd, "params"))
	if got := s.GetModel(); got != "claude-haiku-5-5" {
		t.Fatalf("session model after config_option_update = %q", got)
	}
	if got := a.GetModel(); got != "claude-haiku-5-5" {
		t.Fatalf("agent model after config_option_update = %q", got)
	}
	select {
	case ev := <-s.events:
		t.Fatalf("config_option_update must not produce events, got %+v", ev)
	default:
	}
}

// --- token usage ---

func TestParsePromptUsage(t *testing.T) {
	u, ok := parsePromptUsage(json.RawMessage(`{"stopReason":"end_turn","usage":{"inputTokens":10,"outputTokens":5,"cachedReadTokens":100,"cachedWriteTokens":7,"totalTokens":122}}`))
	if !ok || u.InputTokens != 10 || u.OutputTokens != 5 || u.CacheReadTokens != 100 || u.CacheWriteTokens != 7 || u.TotalTokens != 122 {
		t.Fatalf("usage = %+v ok=%v", u, ok)
	}
	if _, ok := parsePromptUsage(fixtureField(t, loadDroidFixture(t, "session_prompt_response"), "result")); ok {
		t.Fatal("droid prompt response carries no usage")
	}
}

func TestSend_PromptUsageFillsEventResult(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	startFakeServer(t, wResp, rReq, func(m rpcMsg) string {
		if m.Method == "session/prompt" {
			return `{"stopReason":"end_turn","usage":{"inputTokens":12,"outputTokens":34,"cachedReadTokens":5600,"cachedWriteTokens":78}}`
		}
		return `{}`
	})
	if err := s.Send("hi", "m1", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	ev := nextEvent(t, s, core.EventResult)
	if ev.InputTokens != 12 || ev.OutputTokens != 34 || ev.CacheReadInputTokens != 5600 || ev.CacheCreationInputTokens != 78 {
		t.Fatalf("EventResult tokens = %+v", ev)
	}
	cu := s.GetContextUsage()
	if cu == nil || cu.CachedInputTokens != 5600 || cu.CacheCreationInputTokens != 78 || cu.OutputTokens != 34 {
		t.Fatalf("context usage = %+v", cu)
	}
}

func TestSend_TurnUsageHookFallback(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	promptRes := fixtureField(t, loadDroidFixture(t, "session_prompt_response"), "result")
	startFakeServer(t, wResp, rReq, func(m rpcMsg) string {
		if m.Method == "session/prompt" {
			return string(promptRes)
		}
		return `{}`
	})
	var order []string
	s.hooks = Hooks{
		BeforePrompt: func(sid string) { order = append(order, "before:"+sid) },
		TurnUsage: func(sid string) (TurnUsage, bool) {
			order = append(order, "usage:"+sid)
			return TurnUsage{InputTokens: 407, OutputTokens: 392, CacheReadTokens: 59293, CacheWriteTokens: 8286}, true
		},
	}
	if err := s.Send("hi", "m1", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	ev := nextEvent(t, s, core.EventResult)
	if ev.InputTokens != 407 || ev.OutputTokens != 392 || ev.CacheReadInputTokens != 59293 || ev.CacheCreationInputTokens != 8286 {
		t.Fatalf("EventResult tokens = %+v", ev)
	}
	if fmt.Sprint(order) != "[before:test-session-id usage:test-session-id]" {
		t.Fatalf("hook order = %v", order)
	}
}

func TestUsageUpdateFixtureSetsContextWindow(t *testing.T) {
	s, _, _ := newTestSession(t, nil)
	s.onNotification("session/update", fixtureField(t, loadDroidFixture(t, "usage_update"), "params"))
	cu := s.GetContextUsage()
	if cu == nil || cu.ContextWindow != 400000 || cu.UsedTokens <= 0 {
		t.Fatalf("context usage = %+v", cu)
	}
}
