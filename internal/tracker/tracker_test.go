package tracker

import (
	"testing"

	"github.com/ngbcoder/NovaBar/internal/config"
	"github.com/ngbcoder/NovaBar/internal/tiling"
)

func TestTrackerSetRules(t *testing.T) {
	trk := NewWindowTracker()
	rules := []config.RuleConfig{
		{Process: "Raycast.exe", Action: "ignore"},
		{Process: "TextInputHost.exe", Action: "ignore"},
	}

	trk.SetRules(rules)

	if trk.rules == nil {
		t.Fatal("expected rules to be set on tracker")
	}

	dec := trk.rules.Evaluate(tiling.WindowAttributes{
		ProcName: "Raycast",
	})
	if dec.Action != tiling.ActionIgnore {
		t.Errorf("got action %q, want %q", dec.Action, tiling.ActionIgnore)
	}

	dec2 := trk.rules.Evaluate(tiling.WindowAttributes{
		ProcName: "notepad",
	})
	if dec2.Action == tiling.ActionIgnore {
		t.Errorf("notepad should not be ignored")
	}
}
