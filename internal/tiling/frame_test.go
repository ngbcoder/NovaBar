package tiling

import "testing"

// TestComputeFrameCorrectionRegression guards the bug that shipped in the M2 engine:
// the window rect was declared but never populated, so the correction subtracted each
// window's absolute screen position and scattered the layout.
//
// The fixture models a maximized-window case: a window at screen origin with an 8px
// invisible left/top border and a 1px right/bottom shadow.
func TestComputeFrameCorrectionRegression(t *testing.T) {
	pair := FromWinRects(
		// GetWindowRect
		100, 100, 820, 580,
		// GetExtendedFrameBounds
		108, 108, 804, 564,
	)

	c := ComputeFrameCorrection(pair)

	if c.OffsetX != 8 {
		t.Errorf("OffsetX = %d, want 8 (invisible left border)", c.OffsetX)
	}
	if c.OffsetY != 8 {
		t.Errorf("OffsetY = %d, want 8 (invisible top border)", c.OffsetY)
	}
	if c.InsetX != 16 {
		t.Errorf("InsetX = %d, want 16 (8 left + 8 right)", c.InsetX)
	}
	if c.InsetY != 16 {
		t.Errorf("InsetY = %d, want 16 (8 top + 8 bottom)", c.InsetY)
	}
}

// TestCorrectionApplyToTarget verifies the correction actually cancels out: a target
// applied through the correction must land its visible frame back on the target.
//
// This is the property that matters. The regression above produced an OffsetX equal to
// the window's screen X, which made this round-trip fail for any window not at the
// screen origin.
func TestCorrectionApplyToTarget(t *testing.T) {
	// A window far from the origin is the case that broke: the old code used the
	// absolute Left value as the offset.
	windowLeft, windowTop, windowW, windowH := 1840, 300, 800, 600
	frameLeft, frameTop, frameW, frameH := 1848, 308, 784, 584

	c := ComputeFrameCorrection(FromWinRects(
		windowLeft, windowTop, windowW, windowH,
		frameLeft, frameTop, frameW, frameH,
	))

	targets := []Rect{
		{X: 0, Y: 0, Width: 1920, Height: 1040},
		{X: 960, Y: 0, Width: 960, Height: 1040},
		{X: 1920, Y: 0, Width: 1280, Height: 1024},
		{X: 100, Y: 100, Width: 500, Height: 400},
	}

	for _, target := range targets {
		applied := c.ApplyToTarget(target)

		// Simulate what Windows does: the visible frame is the window rect shifted by
		// the same offset, and sized window minus the insets.
		visibleX := applied.X + c.OffsetX
		visibleY := applied.Y + c.OffsetY
		visibleW := applied.Width - c.InsetX
		visibleH := applied.Height - c.InsetY

		if visibleX != target.X || visibleY != target.Y {
			t.Errorf("target %+v: visible frame landed at (%d,%d), want (%d,%d)",
				target, visibleX, visibleY, target.X, target.Y)
		}
		if visibleW != target.Width || visibleH != target.Height {
			t.Errorf("target %+v: visible frame sized %dx%d, want %dx%d",
				target, visibleW, visibleH, target.Width, target.Height)
		}
	}
}

// TestComputeFrameCorrectionDegenerate covers the cases DWM hands us for windows it has
// not yet computed bounds for. Skipping compensation is the right answer: a zero
// correction moves the window rect exactly onto the target.
func TestComputeFrameCorrectionDegenerate(t *testing.T) {
	cases := []struct {
		name string
		pair RectPair
	}{
		{"zero window rect", RectPair{Window: Rect{}, ExtendedFrame: Rect{X: 8, Y: 8, Width: 100, Height: 100}}},
		{"zero frame rect", RectPair{Window: Rect{X: 5, Y: 5, Width: 100, Height: 100}, ExtendedFrame: Rect{}}},
		{"negative extents", RectPair{Window: Rect{Width: -10, Height: -10}, ExtendedFrame: Rect{Width: 5, Height: 5}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeFrameCorrection(tc.pair)
			if got != (FrameCorrection{}) {
				t.Errorf("expected zero correction, got %+v", got)
			}
		})
	}
}

// TestComputeFrameCorrectionOversizedFrame covers DWM reporting an extended frame
// larger than the window rect. Trusting it would inflate the window, so insets clamp
// to zero.
func TestComputeFrameCorrectionOversizedFrame(t *testing.T) {
	c := ComputeFrameCorrection(FromWinRects(
		0, 0, 100, 100,
		-5, -5, 110, 110,
	))

	if c.InsetX != 0 {
		t.Errorf("InsetX = %d, want 0 for an oversized frame", c.InsetX)
	}
	if c.InsetY != 0 {
		t.Errorf("InsetY = %d, want 0 for an oversized frame", c.InsetY)
	}
	if c.OffsetX != -5 {
		t.Errorf("OffsetX = %d, want -5", c.OffsetX)
	}
}

// TestBorderlessWindowNeedsNoCorrection documents that a borderless window (such as one
// of our own WebView2 surfaces) correctly yields no correction.
func TestBorderlessWindowNeedsNoCorrection(t *testing.T) {
	c := ComputeFrameCorrection(FromWinRects(0, 0, 800, 600, 0, 0, 800, 600))
	if c != (FrameCorrection{}) {
		t.Errorf("expected zero correction for a borderless window, got %+v", c)
	}

	applied := c.ApplyToTarget(Rect{X: 100, Y: 100, Width: 400, Height: 300})
	if applied != (Rect{X: 100, Y: 100, Width: 400, Height: 300}) {
		t.Errorf("zero correction must pass the target through unchanged, got %+v", applied)
	}
}
