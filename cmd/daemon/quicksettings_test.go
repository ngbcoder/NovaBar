package daemon

import (
	"testing"

	"github.com/ngbcoder/NovaBar/internal/bar"
	"github.com/ngbcoder/NovaBar/internal/win"
)

// TestQuickSettingsPlacement pins the panel beside every bar edge. The island is
// clickable on whichever bar renders it, so the panel must not always assume a top bar.
func TestQuickSettingsPlacement(t *testing.T) {
	cases := []struct {
		name string
		edge string
		bar  bar.Rect
		want win.RECT
	}{
		{
			name: "top",
			edge: "top",
			bar:  bar.Rect{Left: 0, Top: 0, Right: 1920, Bottom: 40},
			want: win.RECT{Left: 800, Top: 48, Right: 1120, Bottom: 268},
		},
		{
			name: "bottom",
			edge: "bottom",
			bar:  bar.Rect{Left: 0, Top: 1040, Right: 1920, Bottom: 1080},
			want: win.RECT{Left: 800, Top: 812, Right: 1120, Bottom: 1032},
		},
		{
			name: "left",
			edge: "left",
			bar:  bar.Rect{Left: 0, Top: 0, Right: 64, Bottom: 1080},
			want: win.RECT{Left: 72, Top: 430, Right: 392, Bottom: 650},
		},
		{
			name: "right",
			edge: "right",
			bar:  bar.Rect{Left: 1856, Top: 0, Right: 1920, Bottom: 1080},
			want: win.RECT{Left: 1528, Top: 430, Right: 1848, Bottom: 650},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quickSettingsPlacement(tc.edge, tc.bar, quickSettingsWidth, quickSettingsHeight); got != tc.want {
				t.Errorf("quickSettingsPlacement(%q) = %+v, want %+v", tc.edge, got, tc.want)
			}
		})
	}
}

// TestQuickSettingsPlacementGrowsFromBarEdge pins that a larger panel keeps its edge on
// the bar: downward for a top bar, upward for a bottom bar. Otherwise the queue would
// push the panel away from the island or off the screen.
func TestQuickSettingsPlacementGrowsFromBarEdge(t *testing.T) {
	topBar := bar.Rect{Left: 0, Top: 0, Right: 1920, Bottom: 40}
	bottomBar := bar.Rect{Left: 0, Top: 1040, Right: 1920, Bottom: 1080}

	if got := quickSettingsPlacement("top", topBar, quickSettingsWidth, 420); got.Top != 48 {
		t.Errorf("top bar panel moved to y=%d, want 48", got.Top)
	}
	if got := quickSettingsPlacement("bottom", bottomBar, quickSettingsWidth, 420); got.Bottom != 1032 {
		t.Errorf("bottom bar panel moved to bottom=%d, want 1032", got.Bottom)
	}
}

// TestQuickSettingsPlacementWidensAroundCenter pins that the queue column opens to the
// right without sliding the settings away from the island: the wider panel stays centred
// on the bar it belongs to.
func TestQuickSettingsPlacementWidensAroundCenter(t *testing.T) {
	barRect := bar.Rect{Left: 0, Top: 0, Right: 1920, Bottom: 40}

	got := quickSettingsPlacement("top", barRect, 576, quickSettingsHeight)
	want := win.RECT{Left: 672, Top: 48, Right: 1248, Bottom: 268}
	if got != want {
		t.Errorf("widened panel = %+v, want %+v", got, want)
	}
}

// TestQuickSettingsClampSize keeps a misreported size from making an unusable window.
func TestQuickSettingsClampSize(t *testing.T) {
	if got := quickSettingsClampWidth(10); got != quickSettingsWidth {
		t.Errorf("clamped tiny width = %d, want %d", got, quickSettingsWidth)
	}
	if got := quickSettingsClampWidth(100000); got != quickSettingsMaxWidth {
		t.Errorf("clamped huge width = %d, want %d", got, quickSettingsMaxWidth)
	}
	if got := quickSettingsClampWidth(576); got != 576 {
		t.Errorf("clamped 576 = %d, want 576", got)
	}
	if got := quickSettingsClampHeight(10); got != quickSettingsHeight {
		t.Errorf("clamped tiny height = %d, want %d", got, quickSettingsHeight)
	}
	if got := quickSettingsClampHeight(100000); got != quickSettingsMaxHeight {
		t.Errorf("clamped huge height = %d, want %d", got, quickSettingsMaxHeight)
	}
	if got := quickSettingsClampHeight(360); got != 360 {
		t.Errorf("clamped 360 = %d, want 360", got)
	}
}

// TestQuickSettingsOpensAtTheLastSize covers the jump the panel used to make: the
// window was created at the floor size, the page then reported its content size, and
// the window grew a frame later. Starting the next open at the remembered size means
// only the very first open after the daemon starts can be resized.
func TestQuickSettingsOpensAtTheLastSize(t *testing.T) {
	d := &daemon{}

	w, h := d.quickSettingsOpenSize()
	if w != quickSettingsWidth || h != quickSettingsHeight {
		t.Errorf("first open = %dx%d, want the floor %dx%d", w, h, quickSettingsWidth, quickSettingsHeight)
	}

	d.noteQuickSettingsSize(576, 240)
	if w, h := d.quickSettingsOpenSize(); w != 576 || h != 240 {
		t.Errorf("next open = %dx%d, want the last reported 576x240", w, h)
	}
}
