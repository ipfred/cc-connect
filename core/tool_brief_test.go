package core

import (
	"strings"
	"testing"
	"time"
)

func TestBriefToolPreview(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{`cat "$HOME/.factory/sessions/x.settings.json"`, 20, `cat "$HOME/.factory/…`},
		{"short", 20, "short"},
		{"# List files\nls -la /tmp", 20, "ls -la /tmp"},
		{"line1\n  line2", 20, "line1 line2"},
		{"echo `date`", 20, "echo 'date'"},
		{"工具调用的命令非常非常非常非常长的中文输入内容", 5, "工具调用的…"},
	}
	for _, c := range cases {
		if got := briefToolPreview(c.in, c.max); got != c.want {
			t.Errorf("briefToolPreview(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestBriefToolLine_DropsNameEqualToInput(t *testing.T) {
	cmd := `cat "$HOME/.factory/sessions/x.settings.json"`
	if got := briefToolLine(cmd, cmd, 20); got != "`cat \"$HOME/.factory/…`" {
		t.Fatalf("got %q", got)
	}
	if got := briefToolLine("Execute", cmd, 20); got != "Execute `cat \"$HOME/.factory/…`" {
		t.Fatalf("got %q", got)
	}
	if got := briefToolLine("TodoWrite", "", 20); got != "TodoWrite" {
		t.Fatalf("got %q", got)
	}
}

// Consecutive tool calls under tool_brief_len render as compact one-line
// entries (no "---" heading) carrying the running tool index, and tool
// results are not relayed.
func TestProcessInteractiveEvents_ToolBriefLen(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangChinese)
	e.SetDisplayConfig(DisplayCfg{ThinkingMessages: true, ThinkingMaxLen: 300, ToolMaxLen: 500, ToolMessages: true, ToolBriefLen: 20})

	sessionKey := "plain:user-brief"
	session := e.sessions.GetOrCreateActive(sessionKey)
	agentSession := newControllableSession("s-brief")
	state := &interactiveState{agentSession: agentSession, platform: p, replyCtx: "ctx-brief"}
	e.interactiveStates[sessionKey] = state

	agentSession.events <- Event{Type: EventToolUse, ToolName: "Execute", ToolInput: `cat "$HOME/.factory/sessions/140f642d.settings.json"`}
	agentSession.events <- Event{Type: EventToolResult, ToolName: "Execute", ToolResult: "{\"model\":\"x\"}", ToolStatus: "completed"}
	agentSession.events <- Event{Type: EventToolUse, ToolName: "Read", ToolInput: "/Users/fred/Desktop/my_work/github/cc-connect/core/engine.go"}
	agentSession.events <- Event{Type: EventResult, Content: "done", Done: true}

	e.processInteractiveEvents(state, session, e.sessions, sessionKey, "m-brief", time.Now(), nil, nil, state.replyCtx, 0)

	sent := strings.Join(p.getSent(), "\n====\n")
	for _, want := range []string{
		"🔧 工具 #1: Execute `cat \"$HOME/.factory/…`",
		"🔧 工具 #2: Read `/Users/fred/Desktop/…`",
	} {
		if !strings.Contains(sent, want) {
			t.Errorf("missing %q in sent messages:\n%s", want, sent)
		}
	}
	if strings.Contains(sent, "---") {
		t.Errorf("brief tool lines must not use the heading layout:\n%s", sent)
	}
	if strings.Contains(sent, "\"model\"") {
		t.Errorf("tool results must be hidden in brief mode:\n%s", sent)
	}
}
