package tiling

import "testing"

// TestWorkAreaForMonitorUsesTheWindowsOwnDisplay is the multi-monitor check for
// floating windows.
//
// A floating window the user dragged to a second screen belongs to that screen. It
// used to be moved back to the primary, which put it somewhere the user had not
// put it and could not see it.
func TestWorkAreaForMonitorUsesTheWindowsOwnDisplay(t *testing.T) {
	monitors := []MonitorInfo{
		{
			HMonitor: 0x100,
			Primary:  true,
			WorkArea: Rect{X: 0, Y: 0, Width: 2560, Height: 1440},
		},
		{
			HMonitor: 0x200,
			WorkArea: Rect{X: 2560, Y: 0, Width: 2560, Height: 1440},
		},
	}

	second := workAreaForMonitor(0x200, monitors)
	if second.X != 2560 {
		t.Errorf("the second display's work area starts at %d, want 2560", second.X)
	}

	primary := workAreaForMonitor(0x100, monitors)
	if primary.X != 0 {
		t.Errorf("the primary display's work area starts at %d, want 0", primary.X)
	}
}

// TestWorkAreaForMonitorFallsBackToThePrimary checks the case where the display
// cannot be resolved.
func TestWorkAreaForMonitorFallsBackToThePrimary(t *testing.T) {
	monitors := []MonitorInfo{
		{HMonitor: 0x100, Primary: true, WorkArea: Rect{X: 0, Y: 0, Width: 2560, Height: 1440}},
		{HMonitor: 0x200, WorkArea: Rect{X: 2560, Y: 0, Width: 2560, Height: 1440}},
	}

	got := workAreaForMonitor(0x999, monitors)
	if got.X != 0 {
		t.Errorf("an unknown display fell back to %d, want the primary at 0", got.X)
	}
}
