package rpc

import (
	"testing"
)

// TestAudioPermissionsAreSplit pins the read/control separation.
//
// Seeing which applications are playing is harmless. Changing one of their volumes is
// audible to everyone at the desk. Collapsing the two would give every widget that draws a
// volume indicator the ability to blast an application's audio, which is precisely the
// kind of quiet widening least privilege exists to prevent.
func TestAudioPermissionsAreSplit(t *testing.T) {
	want := map[string]string{
		MethodVolumeList: PermAudioRead,
		MethodVolumeSet:  PermAudioControl,
		MethodVolumeMute: PermAudioControl,
	}
	for method, required := range want {
		got, ok := methodPermissions[method]
		if !ok {
			t.Errorf("%s is not mapped, so no widget may call it at all", method)
			continue
		}
		if got != required {
			t.Errorf("%s requires %q, want %q", method, got, required)
		}
	}

	readOnly := &Client{permissions: map[string]bool{PermAudioRead: true}}
	if err := readOnly.hasPermission(MethodVolumeList); err != nil {
		t.Errorf("audio.read cannot call volume.list: %v", err)
	}
	for _, method := range []string{MethodVolumeSet, MethodVolumeMute} {
		if err := readOnly.hasPermission(method); err == nil {
			t.Errorf("%s is reachable with audio.read alone", method)
		}
	}

	// audio.read must not drag in unrelated observation either.
	for _, method := range []string{MethodWindowsGet, MethodLauncherSearch, MethodPinsList} {
		if err := readOnly.hasPermission(method); err == nil {
			t.Errorf("audio.read also grants %s; permissions are not independent", method)
		}
	}
}

// TestAudioTopicIsGated: audio state should only be pushed to widgets allowed to read it.
func TestAudioTopicIsGated(t *testing.T) {
	noPerms := &Client{permissions: map[string]bool{}}
	if noPerms.hasTopic(TopicAudioUpdated) {
		t.Error("a widget with no permissions can subscribe to audio.updated")
	}

	windowsOnly := &Client{permissions: map[string]bool{PermWindowsRead: true}}
	if windowsOnly.hasTopic(TopicAudioUpdated) {
		t.Error("windows.read grants audio.updated; the topic is not scoped to audio")
	}

	audioRead := &Client{permissions: map[string]bool{PermAudioRead: true}}
	if !audioRead.hasTopic(TopicAudioUpdated) {
		t.Error("audio.read cannot subscribe to audio.updated")
	}

	// Being able to change volume must not imply being told about other applications.
	controlOnly := &Client{permissions: map[string]bool{PermAudioControl: true}}
	if controlOnly.hasTopic(TopicAudioUpdated) {
		t.Error("audio.control alone grants observation of audio.updated")
	}
}

// TestAudioPermissionsAreDeclorable: a permission missing from AllPermissions is rejected
// at manifest validation, making it unusable however it is mapped.
func TestAudioPermissionsAreDeclorable(t *testing.T) {
	for _, p := range []string{PermAudioRead, PermAudioControl} {
		if !ValidPermission(p) {
			t.Errorf("%q is not in AllPermissions, so a manifest declaring it is rejected", p)
		}
	}
}

// TestVolumeListReachableByAudioReadOnly is the positive half of the split, stated
// separately so the negative assertions in TestAudioPermissionsAreSplit cannot pass
// merely because the methods were left unmapped.
func TestVolumeListReachableByAudioReadOnly(t *testing.T) {
	c := &Client{permissions: map[string]bool{PermAudioRead: true}}
	if err := c.hasPermission(MethodVolumeList); err != nil {
		t.Fatalf("volume.list should be reachable with audio.read: %v", err)
	}
}
