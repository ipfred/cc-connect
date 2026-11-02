package acp

import (
	"encoding/json"

	"github.com/chenhg5/cc-connect/core"
)

// TurnUsage is the token accounting for one session/prompt turn.
type TurnUsage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	TotalTokens      int
}

func (u TurnUsage) isZero() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheReadTokens == 0 &&
		u.CacheWriteTokens == 0 && u.TotalTokens == 0
}

// Hooks lets wrapper agents supply backend-specific data that the ACP
// wire protocol does not carry for their backend. All fields are optional.
type Hooks struct {
	// BeforePrompt runs right before each session/prompt request.
	BeforePrompt func(sessionID string)
	// TurnUsage supplies token usage for the turn that just finished when
	// the session/prompt response carries no `usage` field.
	TurnUsage func(sessionID string) (TurnUsage, bool)
}

// NewWithHooks builds an ACP agent like New and attaches hooks.
func NewWithHooks(opts map[string]any, hooks Hooks) (core.Agent, error) {
	a, err := newAgent(opts)
	if err != nil {
		return nil, err
	}
	a.hooks = hooks
	return a, nil
}

// parsePromptUsage reads the standard ACP `usage` block of a
// session/prompt response.
func parsePromptUsage(res json.RawMessage) (TurnUsage, bool) {
	var pr struct {
		Usage *struct {
			InputTokens       int `json:"inputTokens"`
			OutputTokens      int `json:"outputTokens"`
			TotalTokens       int `json:"totalTokens"`
			CachedReadTokens  int `json:"cachedReadTokens"`
			CachedWriteTokens int `json:"cachedWriteTokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(res, &pr) != nil || pr.Usage == nil {
		return TurnUsage{}, false
	}
	u := TurnUsage{
		InputTokens:      pr.Usage.InputTokens,
		OutputTokens:     pr.Usage.OutputTokens,
		CacheReadTokens:  pr.Usage.CachedReadTokens,
		CacheWriteTokens: pr.Usage.CachedWriteTokens,
		TotalTokens:      pr.Usage.TotalTokens,
	}
	if u.isZero() {
		return TurnUsage{}, false
	}
	return u, true
}
