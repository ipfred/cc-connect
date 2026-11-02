package droid

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/agent/acp"
)

const fixtureSessionID = "11111111-2222-4333-8444-555555555555"

func TestApplyDroidDefaults_FillsUnsetFields(t *testing.T) {
	got := applyDroidDefaults(map[string]any{})
	if got["cmd"] != "droid" {
		t.Errorf("cmd = %v, want droid", got["cmd"])
	}
	args, ok := got["args"].([]string)
	if !ok || strings.Join(args, " ") != "exec -o acp" {
		t.Errorf("args = %v, want [exec -o acp]", got["args"])
	}
	if got["display_name"] != "Droid" {
		t.Errorf("display_name = %v", got["display_name"])
	}
	if got["mode"] != "auto-low" {
		t.Errorf("mode = %v, want auto-low", got["mode"])
	}
	if applyDroidDefaults(nil)["cmd"] != "droid" {
		t.Error("nil opts must get defaults")
	}
}

func TestApplyDroidDefaults_UserOptsWin(t *testing.T) {
	got := applyDroidDefaults(map[string]any{
		"cmd":          "/opt/bin/droid",
		"display_name": "Droid (prod)",
		"mode":         "auto-low",
		"model":        "claude-opus-5-5",
	})
	if got["cmd"] != "/opt/bin/droid" || got["display_name"] != "Droid (prod)" || got["mode"] != "auto-low" || got["model"] != "claude-opus-5-5" {
		t.Fatalf("user options overwritten: %v", got)
	}
	if args, _ := got["args"].([]string); strings.Join(args, " ") != "exec -o acp" {
		t.Fatalf("bare binary path still needs ACP args, got %v", got["args"])
	}

	full := applyDroidDefaults(map[string]any{"cmd": "droid exec -o acp --auto low"})
	if _, ok := full["args"]; ok {
		t.Fatalf("full command line must not get extra args, got %v", full["args"])
	}

	custom := applyDroidDefaults(map[string]any{"args": []string{"exec", "-o", "acp", "-m", "x"}})
	if args := custom["args"].([]string); len(args) != 5 {
		t.Fatalf("explicit args overwritten: %v", args)
	}
}

func TestNew_ReturnsDroidWrapper(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("'true' not in PATH")
	}
	a, err := New(map[string]any{"cmd": "true"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.Name() != "droid" {
		t.Fatalf("Name = %q, want droid", a.Name())
	}
	w, ok := a.(*Agent)
	if !ok {
		t.Fatalf("New returned %T", a)
	}
	var _ *acp.Agent = w.Agent
	if w.CLIDisplayName() != "Droid" {
		t.Fatalf("CLIDisplayName = %q", w.CLIDisplayName())
	}
	if w.GetMode() != "auto-low" {
		t.Fatalf("GetMode = %q, want auto-low default", w.GetMode())
	}
}

func writeSessionFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	proj := filepath.Join(dir, "-tmp-project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, fixtureSessionID+".jsonl"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGetSessionHistory_FiltersHiddenEntries(t *testing.T) {
	dir := writeSessionFixture(t)
	a := &Agent{sessionsDir: dir}
	entries, err := a.GetSessionHistory(context.Background(), fixtureSessionID, 0)
	if err != nil {
		t.Fatalf("GetSessionHistory: %v", err)
	}
	want := []struct{ role, content string }{
		{"user", "Fix the login bug please"},
		{"assistant", "Let me look at the code."},
		{"assistant", "Fixed: the session cookie was not refreshed."},
		{"user", "Thanks, add a test"},
		{"assistant", "Test added."},
	}
	if len(entries) != len(want) {
		for _, e := range entries {
			t.Logf("%s: %q", e.Role, e.Content)
		}
		t.Fatalf("got %d entries, want %d", len(entries), len(want))
	}
	for i, w := range want {
		if entries[i].Role != w.role || entries[i].Content != w.content {
			t.Errorf("entry %d = %s %q, want %s %q", i, entries[i].Role, entries[i].Content, w.role, w.content)
		}
	}
	if entries[0].Timestamp.IsZero() {
		t.Error("timestamp not parsed")
	}

	last2, err := a.GetSessionHistory(context.Background(), fixtureSessionID, 2)
	if err != nil || len(last2) != 2 || last2[1].Content != "Test added." {
		t.Fatalf("limit=2 -> %+v, %v", last2, err)
	}
}

func TestGetSessionHistory_NotFoundAndInvalidID(t *testing.T) {
	a := &Agent{sessionsDir: t.TempDir()}
	if _, err := a.GetSessionHistory(context.Background(), "missing", 0); err == nil {
		t.Fatal("want error for unknown session")
	}
	if _, err := a.GetSessionHistory(context.Background(), "../etc/passwd", 0); err == nil {
		t.Fatal("want error for path-like session id")
	}
}

func writeSettings(t *testing.T, path string, input, output, cw, cr int) {
	t.Helper()
	body := fmt.Sprintf(`{"model":"claude-haiku-5-5","tokenUsage":{"inputTokens":%d,"outputTokens":%d,"cacheCreationTokens":%d,"cacheReadTokens":%d,"thinkingTokens":1,"factoryCredits":2}}`,
		input, output, cw, cr)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUsageTracker_TurnDelta(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "-tmp-project")
	_ = os.MkdirAll(proj, 0o755)
	settings := filepath.Join(proj, fixtureSessionID+".settings.json")

	tr := newUsageTracker(dir)
	tr.pollInterval = time.Millisecond
	tr.pollAttempts = 3

	// First turn of a brand-new session: no settings file yet.
	tr.snapshot(fixtureSessionID)
	writeSettings(t, settings, 407, 392, 8286, 59293)
	u, ok := tr.turnUsage(fixtureSessionID)
	if !ok || u.InputTokens != 407 || u.OutputTokens != 392 || u.CacheWriteTokens != 8286 || u.CacheReadTokens != 59293 {
		t.Fatalf("turn 1 = %+v ok=%v", u, ok)
	}

	// Second turn: only the delta is reported.
	tr.snapshot(fixtureSessionID)
	writeSettings(t, settings, 443, 402, 8286, 81934)
	u, ok = tr.turnUsage(fixtureSessionID)
	if !ok || u.InputTokens != 36 || u.OutputTokens != 10 || u.CacheWriteTokens != 0 || u.CacheReadTokens != 22641 {
		t.Fatalf("turn 2 = %+v ok=%v", u, ok)
	}

	// No change (e.g. cancelled turn): nothing to report.
	tr.snapshot(fixtureSessionID)
	if _, ok := tr.turnUsage(fixtureSessionID); ok {
		t.Fatal("unchanged counters must report no usage")
	}

	// Counters reset: report the new totals rather than negative numbers.
	tr.snapshot(fixtureSessionID)
	writeSettings(t, settings, 5, 6, 7, 8)
	u, ok = tr.turnUsage(fixtureSessionID)
	if !ok || u.InputTokens != 5 || u.CacheReadTokens != 8 {
		t.Fatalf("after reset = %+v ok=%v", u, ok)
	}
}
