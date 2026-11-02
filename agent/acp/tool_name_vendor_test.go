package acp

import (
	"encoding/json"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

// Droid sets title to the full shell command and carries the tool id in a
// vendor "name" field; the tool name shown in IM must be the id.
func TestMapToolCall_PrefersVendorNameOverTitle(t *testing.T) {
	update := json.RawMessage(`{"sessionUpdate":"tool_call","toolCallId":"t1","title":"cat \"$HOME/.factory/x.json\"","kind":"execute","name":"Execute","status":"pending","rawInput":{"command":"cat \"$HOME/.factory/x.json\""}}`)
	evs := mapToolCall("s", update)
	if len(evs) != 1 || evs[0].Type != core.EventToolUse {
		t.Fatalf("events = %+v", evs)
	}
	if evs[0].ToolName != "Execute" || evs[0].ToolInput != `cat "$HOME/.factory/x.json"` {
		t.Fatalf("tool = %q input = %q", evs[0].ToolName, evs[0].ToolInput)
	}

	noName := json.RawMessage(`{"sessionUpdate":"tool_call","toolCallId":"t2","title":"Read file","kind":"read","rawInput":{"path":"/a"}}`)
	if evs := mapToolCall("s", noName); evs[0].ToolName != "Read file" {
		t.Fatalf("without name the title must still be used, got %q", evs[0].ToolName)
	}
}
