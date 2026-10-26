package bar

import (
	"testing"

	"github.com/ngbcoder/NovaBar/internal/win"
)

func TestPerMonitorIndependentTopBars(t *testing.T) {
	m := NewManager()

	// Two monitors side by side: Monitor 1 (0..2560), Monitor 2 (2560..5120)
	m.bars = []*AppBarManager{
		{
			edge:        win.ABE_TOP,
			size:        40,
			name:        "main",
			registered:  true,
			monitorRect: win.RECT{Left: 0, Top: 0, Right: 2560, Bottom: 1440},
		},
		{
			edge:        win.ABE_TOP,
			size:        40,
			name:        "main",
			registered:  true,
			monitorRect: win.RECT{Left: 2560, Top: 0, Right: 5120, Bottom: 1440},
		},
	}

	desktop := Rect{Left: 0, Top: 0, Right: 5120, Bottom: 1440}
	m.recomputeRectsLocked(desktop)

	if len(m.bars) != 2 {
		t.Fatalf("got %d bars, want 2", len(m.bars))
	}

	b1 := m.bars[0].requested
	b2 := m.bars[1].requested

	if b1.Left != 0 || b1.Right != 2560 {
		t.Errorf("monitor 1 top bar spans %d..%d, want 0..2560", b1.Left, b1.Right)
	}
	if b2.Left != 2560 || b2.Right != 5120 {
		t.Errorf("monitor 2 top bar spans %d..%d, want 2560..5120", b2.Left, b2.Right)
	}
	if b1.Height() != 40 || b2.Height() != 40 {
		t.Errorf("bar heights must be 40")
	}
}
