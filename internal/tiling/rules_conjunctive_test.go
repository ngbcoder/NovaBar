package tiling

import (
	"testing"

	"github.com/ngbcoder/NovaBar/internal/config"
)

// TestRuleSetFieldsAreConjunctive pins the AND semantics of multi-field rules.
//
// The default config relies on this: the explorer rule carries both a process and a
// class, and must match only explorer's OperationStatusWindow. Under OR semantics it
// would float every Explorer window on the desktop.
func TestRuleSetFieldsAreConjunctive(t *testing.T) {
	rs := NewRuleSet([]config.RuleConfig{
		{Process: "explorer.exe", Class: "OperationStatusWindow", Action: "float"},
	})

	// Both fields match.
	if got := rs.Evaluate(WindowAttributes{
		ProcName: "explorer",
		Class:    "OperationStatusWindow",
	}); got.Action != ActionFloat {
		t.Errorf("Action = %q, want %q when both fields match", got.Action, ActionFloat)
	}

	// Process matches, class does not: must NOT float.
	if got := rs.Evaluate(WindowAttributes{
		ProcName: "explorer",
		Class:    "CabinetWClass",
	}); got.Action != ActionTile {
		t.Errorf("Action = %q, want %q: a partial match must not apply the rule", got.Action, ActionTile)
	}

	// Class matches, process does not: must NOT float.
	if got := rs.Evaluate(WindowAttributes{
		ProcName: "notepad",
		Class:    "OperationStatusWindow",
	}); got.Action != ActionTile {
		t.Errorf("Action = %q, want %q: a partial match must not apply the rule", got.Action, ActionTile)
	}
}

// TestRuleSetTitleFieldIsConjunctive covers the same for process + title.
func TestRuleSetTitleFieldIsConjunctive(t *testing.T) {
	rs := NewRuleSet([]config.RuleConfig{
		{Process: "chrome.exe", Title: "Picture-in-Picture", Action: "float"},
	})

	// Process matches, title does not.
	if got := rs.Evaluate(WindowAttributes{ProcName: "chrome", Title: "Docs"}); got.Action != ActionTile {
		t.Errorf("Action = %q, want %q: title must also match", got.Action, ActionTile)
	}

	// Title matches, process does not.
	if got := rs.Evaluate(WindowAttributes{ProcName: "firefox", Title: "Picture-in-Picture"}); got.Action != ActionTile {
		t.Errorf("Action = %q, want %q: process must also match", got.Action, ActionTile)
	}
}
