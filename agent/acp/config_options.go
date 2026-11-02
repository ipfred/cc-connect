package acp

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

// acpConfigOption mirrors one entry of the ACP `configOptions` array
// returned by session/new, session/resume, session/set_config_option and
// the config_option_update notification.
type acpConfigOption struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Category     string            `json:"category,omitempty"`
	Type         string            `json:"type,omitempty"`
	CurrentValue json.RawMessage   `json:"currentValue,omitempty"`
	Options      []acpConfigChoice `json:"options,omitempty"`
}

// acpConfigChoice is a select value. ACP also allows grouped selects,
// where an entry carries nested Options instead of a Value.
type acpConfigChoice struct {
	Value       string            `json:"value"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Options     []acpConfigChoice `json:"options,omitempty"`
}

func (o acpConfigOption) current() string {
	if len(o.CurrentValue) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(o.CurrentValue, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(o.CurrentValue))
}

func (o acpConfigOption) choices() []acpConfigChoice {
	var out []acpConfigChoice
	var walk func([]acpConfigChoice)
	walk = func(list []acpConfigChoice) {
		for _, c := range list {
			if len(c.Options) > 0 {
				walk(c.Options)
				continue
			}
			if c.Value != "" {
				out = append(out, c)
			}
		}
	}
	walk(o.Options)
	return out
}

// resolve maps user input to a canonical choice value (case-insensitive
// on value, then display name). Empty when nothing matches.
func (o acpConfigOption) resolve(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	choices := o.choices()
	for _, c := range choices {
		if strings.EqualFold(c.Value, input) {
			return c.Value
		}
	}
	for _, c := range choices {
		if strings.EqualFold(c.Name, input) {
			return c.Value
		}
	}
	return ""
}

func isModelConfigOption(o acpConfigOption) bool {
	return strings.EqualFold(o.Category, "model") || strings.EqualFold(o.ID, "model")
}

func isReasoningConfigOption(o acpConfigOption) bool {
	if isModelConfigOption(o) {
		return false
	}
	return strings.Contains(strings.ToLower(o.ID), "reasoning") || strings.EqualFold(o.Category, "thought_level")
}

func findConfigOption(opts []acpConfigOption, match func(acpConfigOption) bool) (acpConfigOption, bool) {
	for _, o := range opts {
		if match(o) {
			return o, true
		}
	}
	return acpConfigOption{}, false
}

// configOptionCallbacks is the optional half of sessionCallbacks that
// carries configOptions. Kept separate so callbacks that only care about
// modes (tests, integration harnesses) need not implement it.
type configOptionCallbacks interface {
	reportConfigOptions(opts []acpConfigOption)
	// desiredConfigValues returns the user's pending model and reasoning
	// effort; empty means "keep whatever the agent selected".
	desiredConfigValues() (model, effort string)
}

var _ configOptionCallbacks = (*Agent)(nil)

// -- session side --

func (s *acpSession) absorbConfigOptions(opts []acpConfigOption) {
	if len(opts) == 0 {
		return
	}
	cp := append([]acpConfigOption(nil), opts...)
	s.configMu.Lock()
	s.configOptions = cp
	s.configMu.Unlock()
	if cb, ok := s.callbacks.(configOptionCallbacks); ok {
		cb.reportConfigOptions(cp)
	}
}

// maybeAbsorbConfigOptionUpdate keeps the cache in sync with
// server-driven changes (e.g. the reasoning list changing after a model
// switch).
func (s *acpSession) maybeAbsorbConfigOptionUpdate(params json.RawMessage) {
	var wrap struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &wrap) != nil || len(wrap.Update) == 0 {
		return
	}
	var u struct {
		Kind          string            `json:"sessionUpdate"`
		ConfigOptions []acpConfigOption `json:"configOptions"`
	}
	if json.Unmarshal(wrap.Update, &u) != nil || u.Kind != "config_option_update" {
		return
	}
	s.absorbConfigOptions(u.ConfigOptions)
}

func (s *acpSession) configOption(match func(acpConfigOption) bool) (acpConfigOption, bool) {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return findConfigOption(s.configOptions, match)
}

// GetModel reports the model the running session is using, for the reply footer.
func (s *acpSession) GetModel() string {
	if o, ok := s.configOption(isModelConfigOption); ok {
		return o.current()
	}
	return ""
}

// GetReasoningEffort reports the running session's reasoning effort, for the reply footer.
func (s *acpSession) GetReasoningEffort() string {
	if o, ok := s.configOption(isReasoningConfigOption); ok {
		return o.current()
	}
	return ""
}

func (s *acpSession) setConfigOption(configID, value string) error {
	sid := s.currentACPSessionID()
	res, err := s.tr.call(s.ctx, "session/set_config_option", map[string]any{
		"sessionId": sid,
		"configId":  configID,
		"value":     value,
	})
	if err != nil {
		return err
	}
	var out struct {
		ConfigOptions []acpConfigOption `json:"configOptions"`
	}
	if json.Unmarshal(res, &out) == nil && len(out.ConfigOptions) > 0 {
		s.absorbConfigOptions(out.ConfigOptions)
		return nil
	}
	// Server returned no snapshot; reflect the change locally.
	s.configMu.Lock()
	for i := range s.configOptions {
		if s.configOptions[i].ID == configID {
			raw, _ := json.Marshal(value)
			s.configOptions[i].CurrentValue = raw
		}
	}
	cp := append([]acpConfigOption(nil), s.configOptions...)
	s.configMu.Unlock()
	if cb, ok := s.callbacks.(configOptionCallbacks); ok {
		cb.reportConfigOptions(cp)
	}
	return nil
}

// applyDesiredConfig pushes the agent's pending model / reasoning effort
// to the session when they differ from what the session reports. It is
// cheap when nothing changed (no RPC), so it runs after the handshake and
// before every prompt.
func (s *acpSession) applyDesiredConfig() {
	cb, ok := s.callbacks.(configOptionCallbacks)
	if !ok || !s.alive.Load() {
		return
	}
	model, effort := cb.desiredConfigValues()
	// Model first: switching models may change the reasoning choices.
	s.applyDesiredValue(isModelConfigOption, model)
	s.applyDesiredValue(isReasoningConfigOption, effort)
}

func (s *acpSession) applyDesiredValue(match func(acpConfigOption) bool, want string) {
	want = strings.TrimSpace(want)
	if want == "" {
		return
	}
	opt, ok := s.configOption(match)
	if !ok {
		return
	}
	target := opt.resolve(want)
	if target == "" {
		key := opt.ID + "=" + want
		s.configMu.Lock()
		warned := s.configRejected[key]
		if s.configRejected == nil {
			s.configRejected = make(map[string]bool)
		}
		s.configRejected[key] = true
		s.configMu.Unlock()
		if !warned {
			slog.Warn("acp: config value not offered by agent, skipping",
				"config_id", opt.ID, "value", want, "session_id", s.currentACPSessionID())
		}
		return
	}
	if strings.EqualFold(opt.current(), target) {
		return
	}
	if err := s.setConfigOption(opt.ID, target); err != nil {
		slog.Warn("acp: session/set_config_option failed",
			"config_id", opt.ID, "value", target, "session_id", s.currentACPSessionID(), "error", err)
		return
	}
	slog.Info("acp: config option applied", "config_id", opt.ID, "value", target, "session_id", s.currentACPSessionID())
}

// -- agent side: ModelSwitcher / ReasoningEffortSwitcher --

var (
	_ core.ModelSwitcher           = (*Agent)(nil)
	_ core.ReasoningEffortSwitcher = (*Agent)(nil)
)

func (a *Agent) reportConfigOptions(opts []acpConfigOption) {
	a.configMu.Lock()
	a.configCache = append([]acpConfigOption(nil), opts...)
	a.configMu.Unlock()
}

func (a *Agent) desiredConfigValues() (string, string) {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.pendingModel, a.pendingEffort
}

func (a *Agent) cachedConfigOption(match func(acpConfigOption) bool) (acpConfigOption, bool) {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return findConfigOption(a.configCache, match)
}

// SetModel records the model to use; it is applied to the session via
// session/set_config_option after the next handshake or before the next
// prompt, whichever comes first.
func (a *Agent) SetModel(model string) {
	model = strings.TrimSpace(model)
	if o, ok := a.cachedConfigOption(isModelConfigOption); ok {
		if v := o.resolve(model); v != "" {
			model = v
		}
	}
	a.configMu.Lock()
	a.pendingModel = model
	a.configMu.Unlock()
	slog.Info("acp: model changed", "model", model)
}

func (a *Agent) GetModel() string {
	a.configMu.RLock()
	pending := a.pendingModel
	a.configMu.RUnlock()
	if pending != "" {
		return pending
	}
	if o, ok := a.cachedConfigOption(isModelConfigOption); ok {
		return o.current()
	}
	return ""
}

// AvailableModels lists the model choices advertised in the latest
// configOptions. Empty until a session has completed its handshake.
func (a *Agent) AvailableModels(_ context.Context) []core.ModelOption {
	o, ok := a.cachedConfigOption(isModelConfigOption)
	if !ok {
		return nil
	}
	var out []core.ModelOption
	for _, c := range o.choices() {
		desc := c.Name
		if desc == c.Value {
			desc = ""
		}
		if c.Description != "" {
			if desc != "" {
				desc += " · "
			}
			desc += c.Description
		}
		out = append(out, core.ModelOption{Name: c.Value, Desc: desc})
	}
	return out
}

func (a *Agent) SetReasoningEffort(effort string) {
	effort = strings.TrimSpace(effort)
	if o, ok := a.cachedConfigOption(isReasoningConfigOption); ok {
		if v := o.resolve(effort); v != "" {
			effort = v
		}
	}
	a.configMu.Lock()
	a.pendingEffort = effort
	a.configMu.Unlock()
	slog.Info("acp: reasoning effort changed", "effort", effort)
}

func (a *Agent) GetReasoningEffort() string {
	a.configMu.RLock()
	pending := a.pendingEffort
	a.configMu.RUnlock()
	if pending != "" {
		return pending
	}
	if o, ok := a.cachedConfigOption(isReasoningConfigOption); ok {
		return o.current()
	}
	return ""
}

func (a *Agent) AvailableReasoningEfforts() []string {
	o, ok := a.cachedConfigOption(isReasoningConfigOption)
	if !ok {
		return nil
	}
	var out []string
	for _, c := range o.choices() {
		out = append(out, c.Value)
	}
	return out
}
