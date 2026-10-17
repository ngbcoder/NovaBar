package tiling

// FrameCorrection describes the adjustment needed so a window's *visible* frame lands
// exactly on a target rectangle.
//
// Windows reports two rectangles per window:
//
//   - GetWindowRect: the window rectangle, which includes the invisible resize border
//     that DWM draws outside the visible edge.
//   - GetExtendedFrameBounds (DWMWA_EXTENDED_FRAME_BOUNDS): the visible frame.
//
// When you ask SetWindowPos to place the window rectangle at T, the visible frame ends
// up at T shifted by (extendedFrame - windowRect). This type carries that shift.
//
// Pure and OS-independent so it can be unit tested on any platform.
type FrameCorrection struct {
	OffsetX int
	OffsetY int
	// InsetX and InsetY are how much smaller the visible frame is than the window
	// rect on each edge, always >= 0.
	InsetX int
	InsetY int
}

// RectPair is the pair of rectangles describing one window's frame geometry.
type RectPair struct {
	// Window is GetWindowRect output.
	Window Rect
	// ExtendedFrame is DWMWA_EXTENDED_FRAME_BOUNDS output.
	ExtendedFrame Rect
}

// FromWinRects converts a native window/extended-frame pair into the pure form used
// for correction math.
func FromWinRects(windowLeft, windowTop, windowWidth, windowHeight int,
	frameLeft, frameTop, frameWidth, frameHeight int) RectPair {
	return RectPair{
		Window: Rect{
			X:      windowLeft,
			Y:      windowTop,
			Width:  windowWidth,
			Height: windowHeight,
		},
		ExtendedFrame: Rect{
			X:      frameLeft,
			Y:      frameTop,
			Width:  frameWidth,
			Height: frameHeight,
		},
	}
}

// ComputeFrameCorrection returns the correction that maps a target *visible* rectangle
// onto the window rectangle that SetWindowPos must receive.
//
// The earlier implementation computed `extendedFrame.Left - 0` because the window rect
// was left unpopulated, which subtracted each window's absolute screen position and
// scattered the layout. The window rect must now be supplied.
//
// Returns the zero correction when either rectangle is degenerate (zero or negative
// extent), which is the signal that DWM has not yet computed frame bounds for a window
// that was just created.
func ComputeFrameCorrection(p RectPair) FrameCorrection {
	if p.Window.Width <= 0 || p.Window.Height <= 0 ||
		p.ExtendedFrame.Width <= 0 || p.ExtendedFrame.Height <= 0 {
		return FrameCorrection{}
	}

	// Positive offset means the visible frame sits to the right of / below the window
	// rect origin, i.e. there is an invisible border on the left/top.
	offsetX := p.ExtendedFrame.X - p.Window.X
	offsetY := p.ExtendedFrame.Y - p.Window.Y

	// Inset is how much of the window rect the visible frame does not cover.
	insetX := p.Window.Width - p.ExtendedFrame.Width
	insetY := p.Window.Height - p.ExtendedFrame.Height

	// Degenerate DWM data can report an extended frame larger than the window rect.
	// Trust the window rect in that case and skip compensation rather than
	// inflating the window.
	if insetX < 0 {
		insetX = 0
	}
	if insetY < 0 {
		insetY = 0
	}

	return FrameCorrection{OffsetX: offsetX, OffsetY: offsetY, InsetX: insetX, InsetY: insetY}
}

// ApplyToTarget returns the rectangle to hand to SetWindowPos so that the window's
// visible frame lands on target.
//
// target is the desired position of the *visible* frame.
func (c FrameCorrection) ApplyToTarget(target Rect) Rect {
	return Rect{
		X:      target.X - c.OffsetX,
		Y:      target.Y - c.OffsetY,
		Width:  target.Width + c.InsetX,
		Height: target.Height + c.InsetY,
	}
}
