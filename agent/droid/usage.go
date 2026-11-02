package droid

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/chenhg5/cc-connect/agent/acp"
)

// tokenUsage mirrors the cumulative `tokenUsage` block Droid writes to
// <sessionID>.settings.json.
type tokenUsage struct {
	InputTokens         int `json:"inputTokens"`
	OutputTokens        int `json:"outputTokens"`
	CacheCreationTokens int `json:"cacheCreationTokens"`
	CacheReadTokens     int `json:"cacheReadTokens"`
}

func (u tokenUsage) minus(base tokenUsage) tokenUsage {
	d := tokenUsage{
		InputTokens:         u.InputTokens - base.InputTokens,
		OutputTokens:        u.OutputTokens - base.OutputTokens,
		CacheCreationTokens: u.CacheCreationTokens - base.CacheCreationTokens,
		CacheReadTokens:     u.CacheReadTokens - base.CacheReadTokens,
	}
	// Counters only grow within a session; a decrease means the file was
	// reset (e.g. after compaction), so the whole current value is new.
	if d.InputTokens < 0 || d.OutputTokens < 0 || d.CacheCreationTokens < 0 || d.CacheReadTokens < 0 {
		return u
	}
	return d
}

// usageTracker turns Droid's cumulative per-session token counters into
// per-turn deltas: snapshot before the prompt, diff after it.
type usageTracker struct {
	sessionsDir  string
	pollInterval time.Duration
	pollAttempts int

	mu       sync.Mutex
	baseline map[string]tokenUsage
	paths    map[string]string
}

func newUsageTracker(sessionsDir string) *usageTracker {
	return &usageTracker{
		sessionsDir:  sessionsDir,
		pollInterval: 100 * time.Millisecond,
		pollAttempts: 8,
		baseline:     make(map[string]tokenUsage),
		paths:        make(map[string]string),
	}
}

func (t *usageTracker) settingsPath(sessionID string) (string, bool) {
	t.mu.Lock()
	p, ok := t.paths[sessionID]
	t.mu.Unlock()
	if ok {
		return p, true
	}
	p, err := findSessionFile(t.sessionsDir, sessionID, ".settings.json")
	if err != nil {
		return "", false
	}
	t.mu.Lock()
	t.paths[sessionID] = p
	t.mu.Unlock()
	return p, true
}

func (t *usageTracker) read(sessionID string) (tokenUsage, bool) {
	p, ok := t.settingsPath(sessionID)
	if !ok {
		return tokenUsage{}, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return tokenUsage{}, false
	}
	var s struct {
		TokenUsage *tokenUsage `json:"tokenUsage"`
	}
	// A partial write fails to parse; callers poll again.
	if json.Unmarshal(data, &s) != nil || s.TokenUsage == nil {
		return tokenUsage{}, false
	}
	return *s.TokenUsage, true
}

// snapshot records the counters before a prompt. A brand-new session has
// no settings file yet, which correctly yields a zero baseline.
func (t *usageTracker) snapshot(sessionID string) {
	cur, _ := t.read(sessionID)
	t.mu.Lock()
	t.baseline[sessionID] = cur
	t.mu.Unlock()
}

// turnUsage returns the token delta since the last snapshot. Droid may
// flush the settings file slightly after the prompt response, so poll
// briefly until the counters move.
func (t *usageTracker) turnUsage(sessionID string) (acp.TurnUsage, bool) {
	t.mu.Lock()
	base := t.baseline[sessionID]
	t.mu.Unlock()

	var cur tokenUsage
	changed := false
	for i := 0; i < t.pollAttempts; i++ {
		if u, ok := t.read(sessionID); ok && u != base {
			cur, changed = u, true
			break
		}
		if i+1 < t.pollAttempts {
			time.Sleep(t.pollInterval)
		}
	}
	if !changed {
		slog.Debug("droid: no token usage change observed for turn", "session_id", sessionID)
		return acp.TurnUsage{}, false
	}
	t.mu.Lock()
	t.baseline[sessionID] = cur
	t.mu.Unlock()

	d := cur.minus(base)
	return acp.TurnUsage{
		InputTokens:      d.InputTokens,
		OutputTokens:     d.OutputTokens,
		CacheReadTokens:  d.CacheReadTokens,
		CacheWriteTokens: d.CacheCreationTokens,
	}, true
}
