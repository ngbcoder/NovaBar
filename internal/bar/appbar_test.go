package bar

import (
	"testing"

	"github.com/ngbcoder/NovaBar/internal/win"
)

// TestOwnsSpace pins the lost-reservation check.
//
// Getting this wrong is not cosmetic: a bar that believes it still owns its strip
// while the work area has taken it back draws over the title bar of every maximized
// window, and a bar that believes it lost a strip it still owns re-registers with the
// shell every few seconds, making the desktop re-lay itself out.
func TestOwnsSpace(t *testing.T) {
	monitor := win.RECT{Left: 0, Top: 0, Right: 2560, Bottom: 1440}

	cases := []struct {
		name  string
		edge  uint32
		work  win.RECT
		owned bool
	}{
		{
			name:  "top bar holds the top strip",
			edge:  win.ABE_TOP,
			work:  win.RECT{Left: 0, Top: 40, Right: 2560, Bottom: 1440},
			owned: true,
		},
		{
			name:  "top bar lost the top strip",
			edge:  win.ABE_TOP,
			work:  win.RECT{Left: 0, Top: 0, Right: 2560, Bottom: 1440},
			owned: false,
		},
		{
			name:  "bottom bar holds the bottom strip",
			edge:  win.ABE_BOTTOM,
			work:  win.RECT{Left: 0, Top: 0, Right: 2560, Bottom: 1400},
			owned: true,
		},
		{
			name:  "bottom bar lost the bottom strip",
			edge:  win.ABE_BOTTOM,
			work:  win.RECT{Left: 0, Top: 0, Right: 2560, Bottom: 1440},
			owned: false,
		},
		{
			name:  "left bar holds the left strip",
			edge:  win.ABE_LEFT,
			work:  win.RECT{Left: 40, Top: 0, Right: 2560, Bottom: 1440},
			owned: true,
		},
		{
			name:  "right bar lost the right strip",
			edge:  win.ABE_RIGHT,
			work:  win.RECT{Left: 0, Top: 0, Right: 2560, Bottom: 1440},
			owned: false,
		},
	}

	for _, tc := range cases {
		ab := NewAppBarManager(monitor, tc.edge, 40, tc.name)
		ab.registered = true

		if got := ab.ownsSpace(tc.work); got != tc.owned {
			t.Errorf("%s: ownsSpace = %v, want %v (work %v)", tc.name, got, tc.owned, tc.work)
		}
	}
}

// TestOwnsSpaceIgnoresUnregisteredBar keeps the guard quiet for a bar that never got
// its space in the first place. There is nothing to reclaim, and re-registering one
// that the shell refused would just provoke another relayout every few seconds.
func TestOwnsSpaceIgnoresUnregisteredBar(t *testing.T) {
	ab := NewAppBarManager(win.RECT{Right: 2560, Bottom: 1440}, win.ABE_TOP, 40, "test")

	if !ab.ownsSpace(win.RECT{Right: 2560, Bottom: 1440}) {
		t.Error("an unregistered bar should not be treated as having lost its space")
	}
}

// TestClaimDueOnlyThrottlesAttempts covers the difference between "still fine" and
// "just repaired". A healthy check must not restart the cooldown, or the repair after a
// shell restart waits for the timer the last healthy check set.
func TestClaimDueOnlyThrottlesAttempts(t *testing.T) {
	ab := NewAppBarManager(win.RECT{Right: 2560, Bottom: 1440}, win.ABE_TOP, 40, "test")

	if !ab.claimDue() {
		t.Fatal("the first claim should be allowed immediately")
	}
	if ab.claimDue() {
		t.Error("a second claim inside the cooldown should be refused")
	}
}

// TestBarNeedsReclaimIgnoresTheLastGrant is the regression this guard exists for.
//
// A bar that registers while another process' bar still owns the edge is granted
// nothing. When that other bar disappears the strip returns to the work area, but the
// bar's own idea of its grant is still zero. Deciding by the grant would leave it
// skipped forever, drawing over a maximized window's title bar.
func TestBarNeedsReclaimIgnoresTheLastGrant(t *testing.T) {
	fullScreen := win.RECT{Right: 2560, Bottom: 1440}

	ab := NewAppBarManager(fullScreen, win.ABE_TOP, 40, "test")
	ab.registered = true
	ab.grantedRect = win.RECT{} // the shell granted nothing at registration

	if !barNeedsReclaim(ab, fullScreen) {
		t.Error("a registered bar whose strip is back in the work area must be reclaimed")
	}

	held := win.RECT{Top: 40, Right: 2560, Bottom: 1440}
	if barNeedsReclaim(ab, held) {
		t.Error("a bar whose strip is still reserved must be left alone")
	}

	ab.registered = false
	if barNeedsReclaim(ab, fullScreen) {
		t.Error("an unregistered bar has nothing to reclaim")
	}
}
