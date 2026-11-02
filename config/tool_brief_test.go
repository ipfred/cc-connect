package config

import "testing"

func TestEffectiveToolBriefLen(t *testing.T) {
	global, project := 20, 40
	if got := EffectiveToolBriefLen(&Config{}, &ProjectConfig{}); got != 0 {
		t.Fatalf("default = %d, want 0", got)
	}
	cfg := &Config{Display: DisplayConfig{ToolBriefLen: &global}}
	if got := EffectiveToolBriefLen(cfg, &ProjectConfig{}); got != 20 {
		t.Fatalf("global = %d, want 20", got)
	}
	if got := EffectiveToolBriefLen(cfg, &ProjectConfig{Display: &DisplayConfig{ToolBriefLen: &project}}); got != 40 {
		t.Fatalf("project override = %d, want 40", got)
	}
}
