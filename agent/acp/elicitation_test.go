package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

// loadDroidFixture returns the raw JSON-RPC message recorded from a real
// `droid exec -o acp` run (testdata/droid_protocol.jsonl) for kind.
func loadDroidFixture(t *testing.T, kind string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("testdata/droid_protocol.jsonl")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec struct {
			Kind string          `json:"kind"`
			Msg  json.RawMessage `json:"msg"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("parse fixture line: %v", err)
		}
		if rec.Kind == kind {
			return rec.Msg
		}
	}
	t.Fatalf("fixture kind %q not found", kind)
	return nil
}

func fixtureField(t *testing.T, msg json.RawMessage, field string) json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(msg, &m); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return m[field]
}

// rpcMsg is one client→server line seen by the fake server.
type rpcMsg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

// fakeServer reads every line the client writes and hands it to handle;
// handle returns a raw JSON result for requests ("" = no reply). All
// lines are recorded for later assertions.
type fakeServer struct {
	mu    sync.Mutex
	lines []rpcMsg
	seen  chan rpcMsg
}

func startFakeServer(t *testing.T, w io.Writer, r io.Reader, handle func(m rpcMsg) string) *fakeServer {
	t.Helper()
	fs := &fakeServer{seen: make(chan rpcMsg, 64)}
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for sc.Scan() {
			var m rpcMsg
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			fs.mu.Lock()
			fs.lines = append(fs.lines, m)
			fs.mu.Unlock()
			select {
			case fs.seen <- m:
			default:
			}
			if m.Method != "" && len(m.ID) > 0 && handle != nil {
				if res := handle(m); res != "" {
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", m.ID, res)
				}
			}
		}
	}()
	return fs
}

// waitFor blocks until a recorded line satisfies pred.
func (fs *fakeServer) waitFor(t *testing.T, pred func(rpcMsg) bool) rpcMsg {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		fs.mu.Lock()
		for _, m := range fs.lines {
			if pred(m) {
				fs.mu.Unlock()
				return m
			}
		}
		fs.mu.Unlock()
		select {
		case <-fs.seen:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatal("timed out waiting for client message")
		}
	}
}

func nextEvent(t *testing.T, s *acpSession, typ core.EventType) core.Event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-s.events:
			if ev.Type == typ {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event %v", typ)
		}
	}
}

// --- elicitation schema conversion ---

func TestParseElicitationForm_DroidAskUserFixture(t *testing.T) {
	msg := loadDroidFixture(t, "elicitation_create")
	params := fixtureField(t, msg, "params")
	var p struct {
		RequestedSchema json.RawMessage `json:"requestedSchema"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		t.Fatal(err)
	}
	fields := parseElicitationForm(p.RequestedSchema)
	if len(fields) != 2 {
		t.Fatalf("got %d fields, want 2 (the *_other siblings must fold into their choice)", len(fields))
	}
	q1 := fields[0]
	if q1.prop != "question_1" || q1.otherProp != "question_1_other" {
		t.Fatalf("field[0] = %+v", q1)
	}
	if q1.question.Question != "What is your favorite color?" || q1.question.Header != "Color" {
		t.Fatalf("question[0] = %+v", q1.question)
	}
	var labels []string
	for _, o := range q1.question.Options {
		labels = append(labels, o.Label)
	}
	if strings.Join(labels, ",") != "red,green,blue" {
		t.Fatalf("options = %v, want red,green,blue (\"Other answer\" hidden; free text maps to it)", labels)
	}
	if fields[1].question.Question != "Should I proceed?" || len(fields[1].question.Options) != 2 {
		t.Fatalf("question[1] = %+v", fields[1].question)
	}
}

func TestParseElicitationForm_TypesAndOrder(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{
		"zeta":{"type":"string","title":"Name"},
		"alpha":{"type":"boolean","description":"Ship it?"},
		"size":{"type":"string","enum":["s","m"],"enumNames":["Small","Medium"]},
		"count":{"type":"integer","description":"How many?"},
		"ratio":{"type":"number","description":"Ratio?"},
		"tags":{"type":"array","description":"Tags","items":{"anyOf":[{"const":"a","title":"A"},{"const":"b","title":"B"}]}}
	}}`)
	fields := parseElicitationForm(schema)
	if len(fields) != 6 {
		t.Fatalf("got %d fields", len(fields))
	}
	wantOrder := []string{"zeta", "alpha", "size", "count", "ratio", "tags"}
	for i, f := range fields {
		if f.prop != wantOrder[i] {
			t.Fatalf("field %d prop = %q, want %q (document order must be kept)", i, f.prop, wantOrder[i])
		}
	}
	if fields[0].kind != elicitText || fields[0].question.Question != "Name" || len(fields[0].question.Options) != 0 {
		t.Fatalf("text field = %+v", fields[0])
	}
	if fields[1].kind != elicitBool || len(fields[1].question.Options) != 2 {
		t.Fatalf("bool field = %+v", fields[1])
	}
	if fields[2].kind != elicitSingle || fields[2].question.Options[1].Label != "Medium" || fields[2].values[1] != "m" {
		t.Fatalf("enum field = %+v", fields[2])
	}
	if fields[3].kind != elicitInteger || fields[4].kind != elicitNumber {
		t.Fatalf("numeric kinds = %v %v", fields[3].kind, fields[4].kind)
	}
	if fields[5].kind != elicitMulti || !fields[5].question.MultiSelect {
		t.Fatalf("multi field = %+v", fields[5])
	}
}

func TestBuildElicitationResponse_AcceptDeclineConfirm(t *testing.T) {
	msg := loadDroidFixture(t, "elicitation_create")
	var p struct {
		RequestedSchema json.RawMessage `json:"requestedSchema"`
	}
	_ = json.Unmarshal(fixtureField(t, msg, "params"), &p)
	fields := parseElicitationForm(p.RequestedSchema)

	res := buildElicitationResponse(fields, core.PermissionResult{
		Behavior: "allow",
		UpdatedInput: map[string]any{"answers": map[string]any{
			"What is your favorite color?": "purple",
			"Should I proceed?":            "yes",
		}},
	})
	if res["action"] != "accept" {
		t.Fatalf("action = %v", res["action"])
	}
	content := res["content"].(map[string]any)
	if content["question_1"] != "other" || content["question_1_other"] != "purple" {
		t.Fatalf("free text must map to the other choice, got %v", content)
	}
	if content["question_2"] != "option_0" {
		t.Fatalf("label yes must map to const option_0, got %v", content["question_2"])
	}

	if got := buildElicitationResponse(fields, core.PermissionResult{Behavior: "deny"}); got["action"] != "decline" {
		t.Fatalf("deny action = %v, want decline", got["action"])
	}

	typed := parseElicitationForm(json.RawMessage(`{"properties":{
		"ok":{"type":"boolean","description":"OK?"},
		"n":{"type":"integer","description":"N?"},
		"x":{"type":"number","description":"X?"},
		"tags":{"type":"array","description":"Tags","items":{"enum":["a","b"]}}}}`))
	res = buildElicitationResponse(typed, core.PermissionResult{Behavior: "allow", UpdatedInput: map[string]any{
		"answers": map[string]any{"OK?": "No", "N?": "3", "X?": "1.5", "Tags": "a, b"},
	}})
	c := res["content"].(map[string]any)
	if c["ok"] != false || c["n"] != int64(3) || c["x"] != 1.5 {
		t.Fatalf("typed content = %#v", c)
	}
	if tags, _ := c["tags"].([]string); len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Fatalf("tags = %#v", c["tags"])
	}

	confirm := []elicitationField{confirmElicitationField("Proceed?")}
	if got := buildElicitationResponse(confirm, core.PermissionResult{Behavior: "allow", UpdatedInput: map[string]any{
		"answers": map[string]any{"Proceed?": "No"},
	}}); got["action"] != "decline" {
		t.Fatalf("confirm No action = %v, want decline", got["action"])
	}
}

// --- elicitation end-to-end through the session ---

func TestSession_ElicitationRoundTrip_DroidFixture(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	fs := startFakeServer(t, wResp, rReq, nil)

	req := loadDroidFixture(t, "elicitation_create")
	if _, err := fmt.Fprintf(wResp, "%s\n", req); err != nil {
		t.Fatal(err)
	}
	ev := nextEvent(t, s, core.EventPermissionRequest)
	if ev.ToolName != "AskUserQuestion" || len(ev.Questions) != 2 {
		t.Fatalf("event = %+v", ev)
	}
	if ev.RequestID != "0" {
		t.Fatalf("request id = %q, want 0 (droid numbers its requests from 0)", ev.RequestID)
	}

	err := s.RespondPermission(ev.RequestID, core.PermissionResult{
		Behavior: "allow",
		UpdatedInput: map[string]any{"answers": map[string]any{
			ev.Questions[0].Question: "green",
			ev.Questions[1].Question: "no",
		}},
	})
	if err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	resp := fs.waitFor(t, func(m rpcMsg) bool { return m.Method == "" && string(m.ID) == "0" })
	var out struct {
		Action  string         `json:"action"`
		Content map[string]any `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out.Action != "accept" || out.Content["question_1"] != "option_1" || out.Content["question_2"] != "option_1" {
		t.Fatalf("response = %+v", out)
	}
}

func TestSession_ElicitationCancelledOnCancelTurn(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	fs := startFakeServer(t, wResp, rReq, nil)

	_, _ = fmt.Fprint(wResp, `{"jsonrpc":"2.0","id":7,"method":"elicitation/create","params":{"mode":"form","message":"Pick","requestedSchema":{"type":"object","properties":{"q":{"type":"string","enum":["a","b"]}}}}}`+"\n")
	ev := nextEvent(t, s, core.EventPermissionRequest)

	if err := s.CancelTurn(); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	resp := fs.waitFor(t, func(m rpcMsg) bool { return m.Method == "" && string(m.ID) == "7" })
	if !strings.Contains(string(resp.Result), `"cancel"`) {
		t.Fatalf("pending elicitation must be answered with cancel, got %s", resp.Result)
	}
	if err := s.RespondPermission(ev.RequestID, core.PermissionResult{Behavior: "allow"}); err == nil {
		t.Fatal("a cancelled elicitation must not be answered twice")
	}
}

func TestSession_ElicitationUnsupportedModeDeclined(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	_ = s
	fs := startFakeServer(t, wResp, rReq, nil)
	_, _ = fmt.Fprint(wResp, `{"jsonrpc":"2.0","id":9,"method":"elicitation/create","params":{"mode":"url","url":"https://example.com","message":"Open"}}`+"\n")
	resp := fs.waitFor(t, func(m rpcMsg) bool { return m.Method == "" && string(m.ID) == "9" })
	if !strings.Contains(string(resp.Result), `"decline"`) {
		t.Fatalf("url-mode elicitation must be declined, got %s", resp.Result)
	}
}

func TestHandshake_DeclaresElicitationAndAbsorbsConfigOptions(t *testing.T) {
	a := &Agent{}
	s, wResp, rReq := newTestSession(t, a)
	newRes := fixtureField(t, loadDroidFixture(t, "session_new_response"), "result")
	fs := startFakeServer(t, wResp, rReq, func(m rpcMsg) string {
		switch m.Method {
		case "initialize":
			return `{"protocolVersion":1,"agentCapabilities":{"loadSession":true}}`
		case "session/new":
			return string(newRes)
		}
		return `{}`
	})
	s.acpSessID = ""
	if err := s.handshake("", ""); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	init := fs.waitFor(t, func(m rpcMsg) bool { return m.Method == "initialize" })
	var ip struct {
		ClientCapabilities struct {
			Elicitation struct {
				Form *struct{} `json:"form"`
			} `json:"elicitation"`
		} `json:"clientCapabilities"`
	}
	if err := json.Unmarshal(init.Params, &ip); err != nil || ip.ClientCapabilities.Elicitation.Form == nil {
		t.Fatalf("initialize must declare clientCapabilities.elicitation.form, params=%s", init.Params)
	}
	if got := s.GetModel(); got != "claude-sonnet-5-5" {
		t.Fatalf("session model = %q", got)
	}
	if got := a.GetReasoningEffort(); got != "high" {
		t.Fatalf("agent effort = %q, want high from configOptions", got)
	}
	if n := len(a.AvailableModels(context.Background())); n != 5 {
		t.Fatalf("AvailableModels = %d, want 5", n)
	}
}
