package rpc

import "testing"

// TestWindowFocusIsSeparateFromControl pins the split between bringing a window forward
// and acting destructively on it.
//
// The launcher focuses an already-running app rather than launching a second copy. While
// focus lived behind windows.control, that meant the launcher had to be granted the
// ability to close windows, and neither shipped widget needed it.
//
// Bundling them back would let any widget that legitimately focuses a window also close
// one the user never targeted, which is the failure this split exists to prevent.
func TestWindowFocusIsSeparateFromControl(t *testing.T) {
	// Focus must require the narrow permission.
	if got := methodPermissions[MethodWindowFocus]; got != PermWindowsFocus {
		t.Errorf("%s requires %q, want %q", MethodWindowFocus, got, PermWindowsFocus)
	}

	// Move and close stay on the destructive permission.
	for _, method := range []string{MethodWindowMove, MethodWindowClose} {
		if got := methodPermissions[method]; got != PermWindowsControl {
			t.Errorf("%s requires %q, want %q", method, got, PermWindowsControl)
		}
	}

	// Focus alone must not reach the destructive methods.
	focusOnly := &Client{permissions: map[string]bool{PermWindowsFocus: true}}
	if err := focusOnly.hasPermission(MethodWindowFocus); err != nil {
		t.Errorf("windows.focus cannot call window.focus: %v", err)
	}
	for _, method := range []string{MethodWindowMove, MethodWindowClose} {
		if err := focusOnly.hasPermission(method); err == nil {
			t.Errorf("%s is reachable with windows.focus alone", method)
		}
	}

	// Control alone must not reach focus either: the permissions are independent, not
	// nested. Nesting would make "granted control" mean three things at once.
	controlOnly := &Client{permissions: map[string]bool{PermWindowsControl: true}}
	if err := controlOnly.hasPermission(MethodWindowFocus); err == nil {
		t.Error("windows.control also grants window.focus; the permissions are meant to be independent")
	}
	if err := controlOnly.hasPermission(MethodWindowClose); err != nil {
		t.Errorf("windows.control cannot call window.close: %v", err)
	}
}

// TestPermWindowsFocusIsDeclorable: a permission absent from AllPermissions is rejected
// at manifest validation, which would make it unusable however it is mapped.
func TestPermWindowsFocusIsDeclorable(t *testing.T) {
	if !ValidPermission(PermWindowsFocus) {
		t.Errorf("%q is not in AllPermissions, so a manifest declaring it is rejected", PermWindowsFocus)
	}
}

// TestPermissionsAreDistinct guards against two constants sharing a string, which would
// silently merge two grants into one.
func TestPermissionsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, p := range AllPermissions {
		if prev, dup := seen[p]; dup {
			t.Errorf("AllPermissions lists %q twice (as %s and %s)", p, prev, p)
		}
		seen[p] = p
	}
	if len(seen) != len(AllPermissions) {
		t.Errorf("AllPermissions has %d entries but only %d distinct", len(AllPermissions), len(seen))
	}
}
