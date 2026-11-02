package droid

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

// systemReminderRe strips injected <system-reminder> blocks, which Droid
// stores inline in user messages but never shows to the user.
var systemReminderRe = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// hiddenIDPrefixes mark entries Droid synthesises for the model (context
// injections, todo nags, internal system notes, hook output).
var hiddenIDPrefixes = []string{"context-", "todo-", "__internal__", "hook-", "hook_"}

// findSessionFile locates <sessionsDir>/<project-slug>/<sessionID><suffix>.
func findSessionFile(sessionsDir, sessionID, suffix string) (string, error) {
	if sessionsDir == "" {
		return "", errors.New("droid: sessions directory unknown")
	}
	if sessionID == "" || strings.ContainsAny(sessionID, `/\*?[`) || strings.Contains(sessionID, "..") {
		return "", fmt.Errorf("droid: invalid session id %q", sessionID)
	}
	matches, err := filepath.Glob(filepath.Join(sessionsDir, "*", sessionID+suffix))
	if err != nil {
		return "", fmt.Errorf("droid: glob session file: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("droid: session not found: %s", sessionID)
	}
	if len(matches) > 1 {
		// The same id should not appear in two project folders; prefer the
		// most recently modified copy if it does.
		best, bestMod := matches[0], time.Time{}
		for _, m := range matches {
			if st, err := os.Stat(m); err == nil && st.ModTime().After(bestMod) {
				best, bestMod = m, st.ModTime()
			}
		}
		return best, nil
	}
	return matches[0], nil
}

func readSessionHistory(sessionsDir, sessionID string, limit int) ([]core.HistoryEntry, error) {
	path, err := findSessionFile(sessionsDir, sessionID, ".jsonl")
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("droid: open session history: %w", err)
	}
	defer func() { _ = f.Close() }()
	entries, err := parseSessionHistory(f)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

type sessionLine struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Timestamp  string `json:"timestamp"`
	Visibility string `json:"visibility"`
	Message    struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		Visibility string          `json:"visibility"`
	} `json:"message"`
}

// parseSessionHistory extracts the user-visible user/assistant text turns
// from a Droid session jsonl stream.
func parseSessionHistory(r io.Reader) ([]core.HistoryEntry, error) {
	// Lines can carry base64 images, so read whole lines without a cap.
	br := bufio.NewReader(r)
	var out []core.HistoryEntry
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if e, ok := parseHistoryLine(line); ok {
				out = append(out, e)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("droid: read session history: %w", err)
		}
	}
	return out, nil
}

func parseHistoryLine(line []byte) (core.HistoryEntry, bool) {
	var l sessionLine
	if json.Unmarshal(line, &l) != nil || l.Type != "message" {
		return core.HistoryEntry{}, false
	}
	role := l.Message.Role
	if role != "user" && role != "assistant" {
		return core.HistoryEntry{}, false
	}
	for _, v := range []string{l.Visibility, l.Message.Visibility} {
		if v == "llm_only" || v == "user_only" {
			return core.HistoryEntry{}, false
		}
	}
	for _, p := range hiddenIDPrefixes {
		if strings.HasPrefix(l.ID, p) {
			return core.HistoryEntry{}, false
		}
	}
	text := strings.TrimSpace(systemReminderRe.ReplaceAllString(contentText(l.Message.Content), ""))
	if text == "" {
		return core.HistoryEntry{}, false
	}
	ts, _ := time.Parse(time.RFC3339Nano, l.Timestamp)
	return core.HistoryEntry{Role: role, Content: text, Timestamp: ts}, true
}

// contentText joins the text blocks of a message; tool calls, tool
// results, thinking and images are skipped.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
