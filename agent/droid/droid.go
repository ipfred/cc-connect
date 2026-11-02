// Package droid integrates Factory Droid (https://factory.ai) as a
// first-class cc-connect agent.
//
// Droid speaks the Agent Client Protocol over stdio via
// `droid exec -o acp`, so transport and session handling are shared with
// the generic agent/acp package. This wrapper:
//
//  1. Registers `type = "droid"` with Droid defaults (binary, ACP args,
//     display name, a permission-prompting autonomy mode).
//  2. Reads conversation history from Droid's local session store so
//     /history works after /switch.
//  3. Reads per-turn token usage from Droid's session settings file,
//     because Droid's session/prompt response carries no `usage` block.
//
// Authentication is delegated to the local Droid CLI (prior `droid`
// login or FACTORY_API_KEY in the agent env option).
package droid

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhg5/cc-connect/agent/acp"
	"github.com/chenhg5/cc-connect/core"
)

func init() {
	core.RegisterAgent("droid", New)
}

// Agent embeds *acp.Agent and overrides Name and GetSessionHistory.
type Agent struct {
	*acp.Agent
	sessionsDir string
}

var _ core.HistoryProvider = (*Agent)(nil)

// Name returns the stable agent type identifier used in config and
// session store keys.
func (a *Agent) Name() string { return "droid" }

// GetSessionHistory reads the transcript Droid keeps under
// ~/.factory/sessions.
func (a *Agent) GetSessionHistory(_ context.Context, sessionID string, limit int) ([]core.HistoryEntry, error) {
	return readSessionHistory(a.sessionsDir, sessionID, limit)
}

// New builds a Droid agent from project options. Defaults:
// cmd "droid", args ["exec", "-o", "acp"], display_name "Droid",
// mode "normal" (Droid asks before edits and commands). All other ACP
// options pass through to agent/acp.
func New(opts map[string]any) (core.Agent, error) {
	sessionsDir := defaultSessionsDir()
	tracker := newUsageTracker(sessionsDir)
	a, err := acp.NewWithHooks(applyDroidDefaults(opts), acp.Hooks{
		BeforePrompt: tracker.snapshot,
		TurnUsage:    tracker.turnUsage,
	})
	if err != nil {
		return nil, err
	}
	base, ok := a.(*acp.Agent)
	if !ok {
		return a, nil
	}
	return &Agent{Agent: base, sessionsDir: sessionsDir}, nil
}

func defaultSessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".factory", "sessions")
}

// applyDroidDefaults fills Droid defaults for missing or blank options.
func applyDroidDefaults(opts map[string]any) map[string]any {
	if opts == nil {
		opts = make(map[string]any)
	}
	userCmd := optString(opts, "cmd")
	if userCmd == "" {
		userCmd = optString(opts, "command")
	}
	if userCmd == "" {
		opts["cmd"] = "droid"
		if _, ok := opts["args"]; !ok {
			opts["args"] = []string{"exec", "-o", "acp"}
		}
	} else if _, ok := opts["args"]; !ok && len(strings.Fields(userCmd)) == 1 {
		// A bare binary path still needs the ACP subcommand; a full
		// command line (e.g. "droid exec -o acp") is used as-is.
		opts["args"] = []string{"exec", "-o", "acp"}
	}
	if optString(opts, "display_name") == "" {
		opts["display_name"] = "Droid"
	}
	if optString(opts, "mode") == "" {
		opts["mode"] = "normal"
	}
	return opts
}

func optString(opts map[string]any, key string) string {
	s, _ := opts[key].(string)
	return strings.TrimSpace(s)
}
