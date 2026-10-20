package win

import "testing"

// TestVirtualScreenContainsThePrimaryDisplay pins the virtual screen metrics.
//
// The system metrics for the virtual screen are four numbers in a row, and reading
// the wrong pair of them is silent: the desktop comes back as a sliver two pixels
// wide, and everything positioned against it lands in the corner.
//
// The check that catches it is arithmetic rather than a constant: the virtual screen
// has to contain the primary display, and it has to be at least as large as it.
func TestVirtualScreenContainsThePrimaryDisplay(t *testing.T) {
	virtual := VirtualScreen()
	primary, err := PrimaryMonitor()
	if err != nil {
		t.Skipf("no primary monitor: %v", err)
	}

	if virtual.Width() <= 0 || virtual.Height() <= 0 {
		t.Fatalf("the virtual screen is %dx%d, which is not a screen", virtual.Width(), virtual.Height())
	}

	if virtual.Width() < primary.Width() || virtual.Height() < primary.Height() {
		t.Errorf("the virtual screen is %dx%d, smaller than the primary display's %dx%d",
			virtual.Width(), virtual.Height(), primary.Width(), primary.Height())
	}

	// The primary display has to sit inside the virtual screen.
	if primary.Left < virtual.Left || primary.Top < virtual.Top ||
		primary.Right > virtual.Right || primary.Bottom > virtual.Bottom {
		t.Errorf("the primary display %d,%d..%d,%d is not inside the virtual screen %d,%d..%d,%d",
			primary.Left, primary.Top, primary.Right, primary.Bottom,
			virtual.Left, virtual.Top, virtual.Right, virtual.Bottom)
	}
}

// TestPrimaryMonitorIsOnThePrimary checks that the primary display is resolved
// rather than whatever display happens to be first.
func TestPrimaryMonitorIsOnThePrimary(t *testing.T) {
	primary, err := PrimaryMonitor()
	if err != nil {
		t.Skipf("no primary monitor: %v", err)
	}

	screenW := GetSystemMetrics(0)
	screenH := GetSystemMetrics(1)
	if primary.Width() != screenW || primary.Height() != screenH {
		t.Errorf("the primary display is %dx%d, the screen metrics say %dx%d",
			primary.Width(), primary.Height(), screenW, screenH)
	}
}
