package tiling

import (
	"testing"

	"github.com/ngbcoder/NovaBar/internal/config"
)

func TestRuleSetProcessMatch(t *testing.T) {
	rs := NewRuleSet([]config.RuleConfig{
		{Process: "Taskmgr.exe", Action: "float"},
	})

	got := rs.Evaluate(WindowAttributes{ProcName: "taskmgr", Title: "Task Manager"})
	if got.Action != ActionFloat {
		t.Errorf("Action = %q, want %q", got.Action, ActionFloat)
	}

	// A non-matching window defaults to tiling.
	got = rs.Evaluate(WindowAttributes{ProcName: "notepad"})
	if got.Action != ActionTile {
		t.Errorf("Action = %q, want %q for an unmatched window", got.Action, ActionTile)
	}
}

// TestRuleSetProcessSuffixInsensitive covers the ".exe" spelled and bare spellings
// both matching, since config files in the wild use both.
func TestRuleSetProcessSuffixInsensitive(t *testing.T) {
	rs := NewRuleSet([]config.RuleConfig{{Process: "notepad.exe", Action: "float"}})

	for _, spelling := range []string{"notepad", "notepad.exe", "Notepad.EXE"} {
		if got := rs.Evaluate(WindowAttributes{ProcName: spelling}); got.Action != ActionFloat {
			t.Errorf("ProcName %q: Action = %q, want %q", spelling, got.Action, ActionFloat)
		}
	}
}

func TestRuleSetClassMatch(t *testing.T) {
	rs := NewRuleSet([]config.RuleConfig{
		{Class: "OperationStatusWindow", Action: "float"},
	})

	if got := rs.Evaluate(WindowAttributes{ProcName: "explorer", Class: "OperationStatusWindow"}); got.Action != ActionFloat {
		t.Errorf("Action = %q, want %q", got.Action, ActionFloat)
	}
	// Case-insensitive.
	if got := rs.Evaluate(WindowAttributes{ProcName: "explorer", Class: "operationstatuswindow"}); got.Action != ActionFloat {
		t.Errorf("Action = %q, want %q (class match should ignore case)", got.Action, ActionFloat)
	}
}

func TestRuleSetTitleRegex(t *testing.T) {
	rs := NewRuleSet([]config.RuleConfig{
		{Title: `^Picture-in-Picture`, Action: "float"},
	})

	if got := rs.Evaluate(WindowAttributes{ProcName: "chrome", Title: "Picture-in-Picture - Meeting"}); got.Action != ActionFloat {
		t.Errorf("Action = %q, want %q", got.Action, ActionFloat)
	}
	// The regex is anchored, so a trailing occurrence must NOT match and the window
	// falls through to tiling.
	if got := rs.Evaluate(WindowAttributes{ProcName: "chrome", Title: "Meeting - Picture-in-Picture"}); got.Action == ActionFloat {
		t.Error("an anchored regex must not match a trailing occurrence")
	}
}

// TestRuleSetFirstMatchWins covers ordering, which is what lets a user put a specific
// rule above a broad one.
func TestRuleSetFirstMatchWins(t *testing.T) {
	rs := NewRuleSet([]config.RuleConfig{
		{Process: "chrome.exe", Title: "Picture-in-Picture", Action: "ignore"},
		{Process: "chrome.exe", Action: "float"},
	})

	got := rs.Evaluate(WindowAttributes{ProcName: "chrome", Title: "Picture-in-Picture"})
	if got.Action != ActionIgnore {
		t.Errorf("Action = %q, want %q (first matching rule wins)", got.Action, ActionIgnore)
	}

	got = rs.Evaluate(WindowAttributes{ProcName: "chrome", Title: "Docs"})
	if got.Action != ActionFloat {
		t.Errorf("Action = %q, want %q", got.Action, ActionFloat)
	}
}

// TestRuleSetBadRegexIsSkipped covers the resilience requirement: one malformed pattern
// must not disable every other rule.
func TestRuleSetBadRegexIsSkipped(t *testing.T) {
	rs := NewRuleSet([]config.RuleConfig{
		{Title: `([unclosed`, Action: "float"},
		{Process: "notepad.exe", Action: "float"},
	})

	if got := rs.Evaluate(WindowAttributes{ProcName: "notepad"}); got.Action != ActionFloat {
		t.Errorf("a valid rule after a malformed one must still apply, got %q", got.Action)
	}
}

// TestRuleSetEmptyDefaultIsTile documents that an empty action means tile, not an
// accidental float.
func TestRuleSetEmptyDefaultIsTile(t *testing.T) {
	rs := NewRuleSet([]config.RuleConfig{{Process: "notepad.exe"}})

	if got := rs.Evaluate(WindowAttributes{ProcName: "notepad"}); got.Action != ActionTile {
		t.Errorf("Action = %q, want %q", got.Action, ActionTile)
	}
}

func TestIsDefaultFloating(t *testing.T) {
	// These must float even with an empty rule list, so a broken config can never
	// leave Task Manager tiled and unusable.
	for _, name := range []string{"taskmgr", "taskmgr.exe", "Taskmgr.EXE", "regedit", "mmc"} {
		if !IsDefaultFloating(name) {
			t.Errorf("IsDefaultFloating(%q) = false, want true", name)
		}
	}

	for _, name := range []string{"notepad", "chrome", ""} {
		if IsDefaultFloating(name) {
			t.Errorf("IsDefaultFloating(%q) = true, want false", name)
		}
	}
}
