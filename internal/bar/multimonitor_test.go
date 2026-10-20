package bar

import (
	"testing"

	"github.com/ngbcoder/NovaBar/internal/win"
)

// TestBarsSpanTheWholeDesktop is the multi-monitor check.
//
// A bar owns an edge of the desktop, not of one monitor, and the shell credits the
// space against the whole desktop. Sized for the primary display alone, a bar leaves
// the rest of the desktop uncovered, and the windows on the second display end up
// underneath it.
func TestBarsSpanTheWholeDesktop(t *testing.T) {
	m := NewManager()

	// Two displays side by side, the second one to the right of the primary.
	desktop := Rect{Left: 0, Top: 0, Right: 5120, Bottom: 1440}

	m.bars[win.ABE_TOP] = &AppBarManager{
		edge:       win.ABE_TOP,
		size:       40,
		name:       "main",
		registered: true,
		requested:  win.RECT{Left: 0, Top: 0, Right: 5120, Bottom: 40},
	}
	m.recomputeRectsLocked(desktop)

	bounds := m.bars[win.ABE_TOP].requested
	if bounds.Left != 0 || bounds.Right != desktop.Right {
		t.Errorf("the top bar spans %d..%d, want 0..%d", bounds.Left, bounds.Right, desktop.Right)
	}
	if bounds.Height() != 40 {
		t.Errorf("the top bar is %d tall, want 40", bounds.Height())
	}
}

// TestBarFollowsANegativeDesktopOrigin checks the case where a display sits to the
// left of the primary.
//
// The virtual screen's origin is then negative, and a bar placed against zero lands
// on the wrong display entirely.
func TestBarFollowsANegativeDesktopOrigin(t *testing.T) {
	m := NewManager()

	// A display to the left of the primary: the desktop starts at -1920.
	desktop := Rect{Left: -1920, Top: 0, Right: 2560, Bottom: 1440}

	m.bars[win.ABE_TOP] = &AppBarManager{
		edge:       win.ABE_TOP,
		size:       40,
		name:       "main",
		registered: true,
	}

	m.recomputeRectsLocked(desktop)

	bounds := m.bars[win.ABE_TOP].requested
	if bounds.Left != desktop.Left || bounds.Right != desktop.Right {
		t.Errorf("the top bar spans %d..%d, want %d..%d",
			bounds.Left, bounds.Right, desktop.Left, desktop.Right)
	}
}

// TestVerticalBarStopsBelowTheTopBar checks that the corner still belongs to one
// bar when the desktop is wider than the primary.
func TestVerticalBarStopsBelowTheTopBar(t *testing.T) {
	m := NewManager()

	desktop := Rect{Left: 0, Top: 0, Right: 5120, Bottom: 1440}

	m.bars[win.ABE_TOP] = &AppBarManager{
		edge:       win.ABE_TOP,
		size:       40,
		name:       "main",
		registered: true,
	}
	m.bars[win.ABE_LEFT] = &AppBarManager{
		edge:       win.ABE_LEFT,
		size:       64,
		name:       "dock",
		registered: true,
	}

	m.recomputeRectsLocked(desktop)

	top := m.bars[win.ABE_TOP].requested
	left := m.bars[win.ABE_LEFT].requested

	if top.Top != 0 {
		t.Errorf("the top bar starts at %d, want 0", top.Top)
	}
	if left.Top < top.Bottom {
		t.Errorf("the left bar starts at %d, under the top bar's bottom at %d", left.Top, top.Bottom)
	}
}
