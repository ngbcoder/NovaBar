package rpc

import (
	"testing"
)

// TestPinPermissionsAreSplitLeastPrivilege pins the deliberate split for dock pinning.
//
// Reading the pin list needs only launcher.use, because a widget that can already start
// programs has no need for a further grant to see what is pinned. Changing the list is
// gated behind pins.manage, because it writes to disk and outlives the session.
//
// If these two collapse into one permission, every launcher-usable widget silently
// gains write access to the user's persistent configuration, which is exactly the kind
// of quiet widening least privilege exists to prevent.
func TestPinPermissionsAreSplitLeastPrivilege(t *testing.T) {
	want := map[string]string{
		MethodPinsList:   PermLauncherUse,
		MethodPinsAdd:    PermPinsManage,
		MethodPinsRemove: PermPinsManage,
		MethodPinsToggle: PermPinsManage,
		MethodPinsMove:   PermPinsManage,
	}

	for method, required := range want {
		got, ok := methodPermissions[method]
		if !ok {
			t.Errorf("%s is not mapped to a permission, so it is not widget-callable at all", method)
			continue
		}
		if got != required {
			t.Errorf("%s requires %q, want %q", method, got, required)
		}
	}

	// The mutation methods must not be reachable by holding only launcher.use.
	launcherOnly := &Client{permissions: map[string]bool{PermLauncherUse: true}}
	for _, method := range []string{MethodPinsAdd, MethodPinsRemove, MethodPinsToggle, MethodPinsMove} {
		if err := launcherOnly.hasPermission(method); err == nil {
			t.Errorf("%s is reachable with launcher.use alone; it writes to disk", method)
		}
	}

	// And reading must be reachable by a launcher-usable widget, which is what lets the
	// dock show pins without a second grant.
	if err := launcherOnly.hasPermission(MethodPinsList); err != nil {
		t.Errorf("pins.list is not reachable with launcher.use: %v", err)
	}

	// A widget holding only pins.manage must not thereby gain read access to anything
	// else it was not granted.
	pinsOnly := &Client{permissions: map[string]bool{PermPinsManage: true}}
	if err := pinsOnly.hasPermission(MethodLauncherSearch); err == nil {
		t.Error("pins.manage also grants launcher.search; permissions are not independent")
	}
	if err := pinsOnly.hasPermission(MethodWindowsGet); err == nil {
		t.Error("pins.manage also grants windows.read; permissions are not independent")
	}
}

// TestPermPinsManageIsDeclorable checks the new permission can actually be requested. A
// permission missing from AllPermissions is rejected at manifest validation, which would
// make pins.manage unusable no matter that it is mapped.
func TestPermPinsManageIsDeclorable(t *testing.T) {
	if !ValidPermission(PermPinsManage) {
		t.Errorf("%q is not in AllPermissions, so a manifest declaring it is rejected", PermPinsManage)
	}

	found := false
	for _, p := range AllPermissions {
		if p == PermPinsManage {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("%q missing from AllPermissions", PermPinsManage)
	}
}

// TestPinsTopicIsGated covers the push side: a widget that cannot launch should not be
// told when the pin list changes.
func TestPinsTopicIsGated(t *testing.T) {
	noPerms := &Client{permissions: map[string]bool{}}
	if noPerms.hasTopic(TopicPinsChanged) {
		t.Error("a widget with no permissions can subscribe to pins.changed")
	}

	launcherOnly := &Client{permissions: map[string]bool{PermLauncherUse: true}}
	if !launcherOnly.hasTopic(TopicPinsChanged) {
		t.Error("a launcher-usable widget cannot subscribe to pins.changed")
	}

	// pins.manage alone is deliberately not enough to observe pins: managing is not
	// observing.
	pinsOnly := &Client{permissions: map[string]bool{PermPinsManage: true}}
	if pinsOnly.hasTopic(TopicPinsChanged) {
		t.Error("pins.manage alone grants observation of pins.changed")
	}
}
