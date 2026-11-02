package acp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

// elicitationToolName routes elicitation/create requests through the
// engine's existing AskUserQuestion card flow.
const elicitationToolName = "AskUserQuestion"

// elicitationOtherConst is the choice value servers (e.g. Droid) use to
// signal "free-text answer in the sibling <prop>_other field".
const elicitationOtherConst = "other"

type elicitationKind int

const (
	elicitText elicitationKind = iota
	elicitNumber
	elicitInteger
	elicitBool
	elicitSingle
	elicitMulti
	// elicitConfirm is synthesised for schemas without properties: the
	// user's yes/no decides between accept and decline.
	elicitConfirm
)

// elicitationField maps one property of an elicitation form schema onto
// one AskUserQuestion entry.
type elicitationField struct {
	prop      string
	otherProp string // sibling free-text property backing the "other" choice
	kind      elicitationKind
	values    []string // choice values, aligned with question.Options
	question  core.UserQuestion
}

type elicitationState struct {
	rpcID  json.RawMessage
	fields []elicitationField
}

type elicitConstOption struct {
	Const any    `json:"const"`
	Title string `json:"title"`
}

type elicitChoiceSchema struct {
	Enum      []any               `json:"enum"`
	EnumNames []string            `json:"enumNames"`
	OneOf     []elicitConstOption `json:"oneOf"`
	AnyOf     []elicitConstOption `json:"anyOf"`
}

type elicitSchemaProp struct {
	elicitChoiceSchema
	Type        string              `json:"type"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Items       *elicitChoiceSchema `json:"items"`
}

// choices returns the (value, label) pairs a property offers, and whether
// the property is a multi-select array.
func (p elicitSchemaProp) choices() (values, labels []string, multi bool) {
	src := &p.elicitChoiceSchema
	if strings.EqualFold(p.Type, "array") && p.Items != nil {
		src = p.Items
		multi = true
	}
	opts := src.OneOf
	if len(opts) == 0 {
		opts = src.AnyOf
	}
	for _, o := range opts {
		if o.Const == nil {
			continue
		}
		v := fmt.Sprint(o.Const)
		label := strings.TrimSpace(o.Title)
		if label == "" {
			label = v
		}
		values = append(values, v)
		labels = append(labels, label)
	}
	if len(values) == 0 {
		for i, e := range src.Enum {
			v := fmt.Sprint(e)
			label := v
			if i < len(src.EnumNames) && strings.TrimSpace(src.EnumNames[i]) != "" {
				label = strings.TrimSpace(src.EnumNames[i])
			}
			values = append(values, v)
			labels = append(labels, label)
		}
	}
	return values, labels, multi
}

// orderedObjectKeys returns the keys of a JSON object in document order.
// Go maps lose ordering, but question order matters for the IM cards.
func orderedObjectKeys(raw json.RawMessage) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return keys
		}
		key, ok := tok.(string)
		if !ok {
			return keys
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return keys
		}
		keys = append(keys, key)
	}
	return keys
}

// parseElicitationForm converts an elicitation requestedSchema into
// ordered question fields.
func parseElicitationForm(schema json.RawMessage) []elicitationField {
	if len(schema) == 0 {
		return nil
	}
	var s struct {
		Properties json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(schema, &s) != nil || len(s.Properties) == 0 {
		return nil
	}
	var props map[string]elicitSchemaProp
	if json.Unmarshal(s.Properties, &props) != nil {
		return nil
	}
	keys := orderedObjectKeys(s.Properties)

	otherOf := make(map[string]string) // choice prop -> its free-text sibling
	folded := make(map[string]bool)
	for _, key := range keys {
		values, _, _ := props[key].choices()
		if !containsString(values, elicitationOtherConst) {
			continue
		}
		sibling := key + "_" + elicitationOtherConst
		sp, ok := props[sibling]
		if !ok {
			continue
		}
		if sv, _, _ := sp.choices(); len(sv) > 0 {
			continue
		}
		otherOf[key] = sibling
		folded[sibling] = true
	}

	seen := make(map[string]bool)
	var fields []elicitationField
	for _, key := range keys {
		if folded[key] {
			continue
		}
		p := props[key]
		text := strings.TrimSpace(p.Description)
		if text == "" {
			text = strings.TrimSpace(p.Title)
		}
		if text == "" {
			text = key
		}
		if seen[text] {
			text = text + " (" + key + ")"
		}
		seen[text] = true

		f := elicitationField{
			prop:      key,
			otherProp: otherOf[key],
			question: core.UserQuestion{
				Question: text,
				Header:   strings.TrimSpace(p.Title),
			},
		}
		values, labels, multi := p.choices()
		switch {
		case len(values) > 0:
			f.kind = elicitSingle
			if multi {
				f.kind = elicitMulti
				f.question.MultiSelect = true
			}
			for i, v := range values {
				if f.otherProp != "" && v == elicitationOtherConst {
					continue
				}
				f.values = append(f.values, v)
				f.question.Options = append(f.question.Options, core.UserQuestionOption{Label: labels[i]})
			}
		case strings.EqualFold(p.Type, "boolean"):
			f.kind = elicitBool
			f.values = []string{"true", "false"}
			f.question.Options = []core.UserQuestionOption{{Label: "Yes"}, {Label: "No"}}
		case strings.EqualFold(p.Type, "integer"):
			f.kind = elicitInteger
		case strings.EqualFold(p.Type, "number"):
			f.kind = elicitNumber
		default:
			f.kind = elicitText
		}
		fields = append(fields, f)
	}
	return fields
}

func confirmElicitationField(message string) elicitationField {
	q := strings.TrimSpace(message)
	if q == "" {
		q = "Continue?"
	}
	return elicitationField{
		kind:   elicitConfirm,
		values: []string{"true", "false"},
		question: core.UserQuestion{
			Question: q,
			Options:  []core.UserQuestionOption{{Label: "Yes"}, {Label: "No"}},
		},
	}
}

func elicitationQuestions(fields []elicitationField) []core.UserQuestion {
	out := make([]core.UserQuestion, len(fields))
	for i, f := range fields {
		out[i] = f.question
	}
	return out
}

// parseBoolAnswer accepts the button labels plus common typed replies.
func parseBoolAnswer(answer string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "yes", "y", "true", "ok", "是", "是的", "好", "对", "确认", "はい":
		return true, true
	case "no", "n", "false", "否", "不", "不是", "取消", "いいえ":
		return false, true
	}
	return false, false
}

// matchChoice resolves an answer (button label or raw value) to a choice value.
func (f elicitationField) matchChoice(answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	for i, opt := range f.question.Options {
		if strings.EqualFold(opt.Label, answer) {
			return f.values[i], true
		}
	}
	for _, v := range f.values {
		if strings.EqualFold(v, answer) {
			return v, true
		}
	}
	return "", false
}

// fill writes the typed value(s) for this field's answer into content.
func (f elicitationField) fill(answer string, content map[string]any) {
	answer = strings.TrimSpace(answer)
	if answer == "" || f.prop == "" {
		return
	}
	switch f.kind {
	case elicitSingle:
		if v, ok := f.matchChoice(answer); ok {
			content[f.prop] = v
			return
		}
		if f.otherProp != "" {
			content[f.prop] = elicitationOtherConst
			content[f.otherProp] = answer
			return
		}
		content[f.prop] = answer
	case elicitMulti:
		var picked, others []string
		for _, part := range strings.Split(answer, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if v, ok := f.matchChoice(part); ok {
				picked = append(picked, v)
			} else {
				others = append(others, part)
			}
		}
		if len(others) > 0 {
			if f.otherProp != "" {
				picked = append(picked, elicitationOtherConst)
				content[f.otherProp] = strings.Join(others, ", ")
			} else {
				picked = append(picked, others...)
			}
		}
		if len(picked) > 0 {
			content[f.prop] = picked
		}
	case elicitBool:
		if b, ok := parseBoolAnswer(answer); ok {
			content[f.prop] = b
		}
	case elicitInteger:
		if n, err := strconv.ParseInt(answer, 10, 64); err == nil {
			content[f.prop] = n
		} else if x, err := strconv.ParseFloat(answer, 64); err == nil && x == float64(int64(x)) {
			content[f.prop] = int64(x)
		} else {
			content[f.prop] = answer
		}
	case elicitNumber:
		if x, err := strconv.ParseFloat(answer, 64); err == nil {
			content[f.prop] = x
		} else {
			content[f.prop] = answer
		}
	default:
		content[f.prop] = answer
	}
}

// answersFromUpdatedInput extracts the engine's AskUserQuestion answers
// map (question text -> answer).
func answersFromUpdatedInput(input map[string]any) map[string]string {
	out := make(map[string]string)
	switch a := input["answers"].(type) {
	case map[string]any:
		for k, v := range a {
			out[k] = fmt.Sprint(v)
		}
	case map[string]string:
		for k, v := range a {
			out[k] = v
		}
	}
	return out
}

// buildElicitationResponse turns a PermissionResult for an elicitation
// into the ACP elicitation/create result payload.
func buildElicitationResponse(fields []elicitationField, result core.PermissionResult) map[string]any {
	if !strings.EqualFold(result.Behavior, "allow") {
		return map[string]any{"action": "decline"}
	}
	answers := answersFromUpdatedInput(result.UpdatedInput)
	if len(fields) == 1 && fields[0].kind == elicitConfirm {
		if yes, ok := parseBoolAnswer(answers[fields[0].question.Question]); ok && !yes {
			return map[string]any{"action": "decline"}
		}
		return map[string]any{"action": "accept", "content": map[string]any{}}
	}
	content := make(map[string]any)
	for _, f := range fields {
		f.fill(answers[f.question.Question], content)
	}
	return map[string]any{"action": "accept", "content": content}
}

// handleElicitation surfaces an ACP elicitation/create request as an
// AskUserQuestion permission event.
func (s *acpSession) handleElicitation(id json.RawMessage, params json.RawMessage) {
	var p struct {
		Mode            string          `json:"mode"`
		Message         string          `json:"message"`
		RequestedSchema json.RawMessage `json:"requestedSchema"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		_ = s.tr.respondError(id, -32602, "invalid params")
		return
	}
	if p.Mode != "" && !strings.EqualFold(p.Mode, "form") {
		slog.Info("acp: declining unsupported elicitation mode", "mode", p.Mode)
		if err := s.tr.respondSuccess(id, map[string]any{"action": "decline"}); err != nil {
			slog.Warn("acp: respond elicitation decline failed", "error", err)
		}
		return
	}
	fields := parseElicitationForm(p.RequestedSchema)
	if len(fields) == 0 {
		fields = []elicitationField{confirmElicitationField(p.Message)}
	}
	reqKey := jsonIDKey(id)
	s.permMu.Lock()
	if s.elicitByID == nil {
		s.elicitByID = make(map[string]elicitationState)
	}
	s.elicitByID[reqKey] = elicitationState{rpcID: id, fields: fields}
	s.permMu.Unlock()

	questions := elicitationQuestions(fields)
	slog.Info("acp: elicitation request", "request_id", reqKey, "questions", len(questions))
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.emit(core.Event{
			Type:         core.EventPermissionRequest,
			RequestID:    reqKey,
			ToolName:     elicitationToolName,
			ToolInput:    p.Message,
			ToolInputRaw: map[string]any{"message": p.Message},
			Questions:    questions,
			SessionID:    s.currentACPSessionID(),
		})
	}()
}

func (s *acpSession) takeElicitation(requestID string) (elicitationState, bool) {
	s.permMu.Lock()
	defer s.permMu.Unlock()
	st, ok := s.elicitByID[requestID]
	if ok {
		delete(s.elicitByID, requestID)
	}
	return st, ok
}

// cancelPendingElicitations answers every outstanding elicitation with
// action "cancel" so the agent does not wait on a question nobody will
// answer (turn cancelled or session closing).
func (s *acpSession) cancelPendingElicitations() {
	s.permMu.Lock()
	pending := s.elicitByID
	s.elicitByID = nil
	s.permMu.Unlock()
	for key, st := range pending {
		if err := s.tr.respondSuccess(st.rpcID, map[string]any{"action": "cancel"}); err != nil {
			slog.Debug("acp: cancel elicitation failed", "request_id", key, "error", err)
		}
	}
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
