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
	"fmt"
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
	sessionsDir          string
	configuredWorkspaces []core.ConfiguredWorkspace
}

var (
	_ core.HistoryProvider             = (*Agent)(nil)
	_ core.ConfiguredWorkspaceProvider = (*Agent)(nil)
)

// Name returns the stable agent type identifier used in config and
// session store keys.
func (a *Agent) Name() string { return "droid" }

// GetSessionHistory reads the transcript Droid keeps under
// ~/.factory/sessions.
func (a *Agent) GetSessionHistory(_ context.Context, sessionID string, limit int) ([]core.HistoryEntry, error) {
	return readSessionHistory(a.sessionsDir, sessionID, limit)
}

// ConfiguredWorkspaces returns the project directories configured through
// project_paths. The engine uses these as selectable workspaces before the
// first conversation starts.
func (a *Agent) ConfiguredWorkspaces() []core.ConfiguredWorkspace {
	return append([]core.ConfiguredWorkspace(nil), a.configuredWorkspaces...)
}

// New builds a Droid agent from project options. Defaults:
// cmd "droid", args ["exec", "-o", "acp"], display_name "Droid",
// mode "auto-low" (file edits and low-risk commands run without asking;
// riskier actions still raise a permission card). All other ACP options
// pass through to agent/acp.
func New(opts map[string]any) (core.Agent, error) {
	opts = applyDroidDefaults(opts)
	configuredWorkspaces, err := parseConfiguredWorkspaces(
		opts["project_paths"],
		optString(opts, "work_dir"),
	)
	if err != nil {
		return nil, err
	}

	sessionsDir := defaultSessionsDir()
	tracker := newUsageTracker(sessionsDir)
	a, err := acp.NewWithHooks(opts, acp.Hooks{
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
	return &Agent{
		Agent:                base,
		sessionsDir:          sessionsDir,
		configuredWorkspaces: configuredWorkspaces,
	}, nil
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
		opts["mode"] = "auto-low"
	}
	return opts
}

func parseConfiguredWorkspaces(raw any, workDir string) ([]core.ConfiguredWorkspace, error) {
	if raw == nil {
		return nil, nil
	}

	var entries []any
	switch v := raw.(type) {
	case string:
		entries = []any{v}
	case []string:
		entries = make([]any, len(v))
		for i := range v {
			entries[i] = v[i]
		}
	case []any:
		entries = v
	case []map[string]any:
		entries = make([]any, len(v))
		for i := range v {
			entries[i] = v[i]
		}
	case []map[string]string:
		entries = make([]any, len(v))
		for i := range v {
			entries[i] = v[i]
		}
	case map[string]any, map[string]string:
		entries = []any{v}
	default:
		return nil, fmt.Errorf("droid: project_paths must be a path or an array of paths/tables, got %T", raw)
	}

	out := make([]core.ConfiguredWorkspace, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for i, entry := range entries {
		name, path, err := parseConfiguredWorkspaceEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("droid: project_paths[%d]: %w", i, err)
		}
		path = resolveConfiguredWorkspacePath(path, workDir)
		if path == "" {
			return nil, fmt.Errorf("droid: project_paths[%d]: path is empty", i)
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if strings.TrimSpace(name) == "" {
			name = filepath.Base(path)
		}
		out = append(out, core.ConfiguredWorkspace{
			Name: strings.TrimSpace(name),
			Path: path,
		})
	}
	return out, nil
}

func parseConfiguredWorkspaceEntry(entry any) (name, path string, err error) {
	switch v := entry.(type) {
	case string:
		return "", strings.TrimSpace(v), nil
	case map[string]any:
		path, _ = v["path"].(string)
		if strings.TrimSpace(path) == "" {
			path, _ = v["dir"].(string)
		}
		name, _ = v["name"].(string)
	case map[string]string:
		path = v["path"]
		if strings.TrimSpace(path) == "" {
			path = v["dir"]
		}
		name = v["name"]
	default:
		return "", "", fmt.Errorf("entry must be a string or table, got %T", entry)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", fmt.Errorf("table entry requires a non-empty path")
	}
	return strings.TrimSpace(name), path, nil
}

func resolveConfiguredWorkspacePath(path, workDir string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if path == "~" {
				path = home
			} else {
				path = filepath.Join(home, path[2:])
			}
		}
	}
	if !filepath.IsAbs(path) {
		base := strings.TrimSpace(workDir)
		if base == "" || base == "." {
			base, _ = os.Getwd()
		}
		if base != "" {
			path = filepath.Join(base, path)
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func optString(opts map[string]any, key string) string {
	s, _ := opts[key].(string)
	return strings.TrimSpace(s)
}
