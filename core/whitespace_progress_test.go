package core

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Agents such as Droid stream bare "\n\n" text chunks between tool calls.
// Each one used to open a stream-preview message that showed up in Feishu
// as an empty reply bubble.
func TestStreamPreview_WhitespaceOnlyTextIsNotSent(t *testing.T) {
	mp := &mockUpdaterPlatform{}
	sp := newStreamPreview(StreamPreviewCfg{Enabled: true, IntervalMs: 10, MinDeltaChars: 1, MaxChars: 500}, mp, "ctx", context.Background(), nil)

	sp.appendText("\n\n")
	time.Sleep(40 * time.Millisecond)
	if msgs := mp.getMessages(); len(msgs) != 0 {
		t.Fatalf("whitespace-only preview must not be sent, got %q", msgs)
	}

	sp.appendText("Hello")
	time.Sleep(40 * time.Millisecond)
	msgs := mp.getMessages()
	if len(msgs) == 0 || !strings.HasPrefix(msgs[0], "start:") || strings.TrimSpace(strings.TrimPrefix(msgs[0], "start:")) != "Hello" {
		t.Fatalf("messages = %q, want a start with Hello", msgs)
	}
}

func TestProcessInteractiveEvents_WhitespaceTextBetweenToolsSendsNoEmptyMessages(t *testing.T) {
	p := &mockUpdaterPlatform{stubPlatformEngine: stubPlatformEngine{n: "plain"}}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangChinese)
	e.SetDisplayConfig(DisplayCfg{ThinkingMessages: false, ThinkingMaxLen: 300, ToolMaxLen: 500, ToolMessages: true, ToolBriefLen: 20})

	sessionKey := "plain:user-ws"
	session := e.sessions.GetOrCreateActive(sessionKey)
	agentSession := newControllableSession("s-ws")
	state := &interactiveState{agentSession: agentSession, platform: p, replyCtx: "ctx-ws"}
	e.interactiveStates[sessionKey] = state

	agentSession.events <- Event{Type: EventText, Content: "\n\n"}
	agentSession.events <- Event{Type: EventToolUse, ToolName: "Grep", ToolInput: "head_limit"}
	agentSession.events <- Event{Type: EventText, Content: "\n\n"}
	agentSession.events <- Event{Type: EventToolUse, ToolName: "Grep", ToolInput: "output_mode"}
	agentSession.events <- Event{Type: EventText, Content: "all done"}
	agentSession.events <- Event{Type: EventResult, Content: "all done", Done: true}
	e.processInteractiveEvents(state, session, e.sessions, sessionKey, "m-ws", time.Now(), nil, nil, state.replyCtx, 0)

	all := append(p.getSent(), p.getMessages()...)
	for _, m := range all {
		body := strings.TrimPrefix(strings.TrimPrefix(m, "start:"), "update:")
		if strings.TrimSpace(body) == "" {
			t.Fatalf("empty message delivered: %q (all: %q)", m, all)
		}
	}
}

func TestCompactProgressWriter_ConsecutiveToolLinesAreTight(t *testing.T) {
	p := &previewCapturePlatform{}
	w := newCompactProgressWriter(context.Background(), p, progressHintReplyCtx{style: "compact"}, "droid", LangChinese, nil)
	w.AppendEvent(ProgressEntryToolUse, "a", "Grep", "🔧 工具 #15: Grep `a`")
	w.AppendEvent(ProgressEntryToolUse, "b", "Grep", "🔧 工具 #16: Grep `b`")
	w.AppendEvent(ProgressEntryThinking, "hmm", "", "💭 hmm")
	w.AppendEvent(ProgressEntryToolUse, "c", "Read", "🔧 工具 #17: Read `c`")

	want := "🔧 工具 #15: Grep `a`\n🔧 工具 #16: Grep `b`\n\n💭 hmm\n\n🔧 工具 #17: Read `c`"
	if w.content != want {
		t.Fatalf("content = %q, want %q", w.content, want)
	}
}
