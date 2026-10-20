//go:build windows

package media

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ngbcoder/NovaBar/internal/winmd"
)

// windowsMediaMetadata is the metadata that defines the media session interfaces.
//
// It ships with the operating system, so it is the ground truth for the interface
// identifiers and the vtable order that this package hardcodes.
func windowsMediaMetadata(t *testing.T) *winmd.File {
	t.Helper()

	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}

	path := filepath.Join(root, "System32", "WinMetadata", "Windows.Media.winmd")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("Windows metadata not available at %s: %v", path, err)
	}

	f, err := winmd.Load(path)
	if err != nil {
		t.Fatalf("could not read %s: %v", path, err)
	}
	return f
}

// TestInterfaceIdentifiersMatchWindowsMetadata pins the identifiers this package uses
// to the ones the operating system publishes.
//
// An identifier that is wrong fails as E_NOINTERFACE, which reads plausibly enough
// to send the next hour down the wrong path. The same mistake as the wrong class
// name, one level down. So the identifiers are not written from documentation: they
// are checked against the metadata the operating system ships, and this test fails
// the moment they drift.
func TestInterfaceIdentifiersMatchWindowsMetadata(t *testing.T) {
	f := windowsMediaMetadata(t)

	cases := []struct {
		interfaceName string
		identifier    string
	}{
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionManagerStatics", "2050c4ee-11a0-57de-aed7-c97c70338245"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionManager", "cace8eac-e86e-504a-ab31-5ff8ff1bce49"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSession", "7148c835-9b14-5ae2-ab85-dc9b1c14e1a8"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionMediaProperties", "68856cf6-adb4-54b2-ac16-05837907acb6"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionPlaybackInfo", "94b4b6cf-e8ba-51ad-87a7-c10ade106127"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionPlaybackControls", "6501a3e6-bc7a-503a-bb1b-68f158f3fb03"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionTimelineProperties", "ede34136-6f25-588d-8ecf-ea5b6735aaa5"},
	}

	for _, tc := range cases {
		t.Run(tc.interfaceName, func(t *testing.T) {
			got, ok := f.InterfaceGUID(tc.interfaceName)
			if !ok {
				t.Fatalf("%s has no identifier in the metadata", tc.interfaceName)
			}
			if got.String() != "{"+tc.identifier+"}" {
				t.Errorf("%s = %s, want {%s}", tc.interfaceName, got, tc.identifier)
			}
		})
	}
}

// TestVtableSlotsMatchWindowsMetadata pins the vtable slots to the method order the
// metadata publishes.
//
// A slot that points at the wrong method does not fail: it returns something, or it
// faults. Reading the order from metadata and checking it here means the order is
// never a matter of memory.
func TestVtableSlotsMatchWindowsMetadata(t *testing.T) {
	f := windowsMediaMetadata(t)

	cases := []struct {
		interfaceName string
		slot          int
		method        string
	}{
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionManagerStatics", slotStaticsRequestAsync, "RequestAsync"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionManager", slotManagerGetCurrentSession, "GetCurrentSession"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionManager", slotManagerGetSessions, "GetSessions"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSession", slotSessionGetSourceAppUserModelID, "get_SourceAppUserModelId"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSession", slotSessionTryGetMediaProperties, "TryGetMediaPropertiesAsync"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSession", slotSessionGetTimelineProperties, "GetTimelineProperties"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSession", slotSessionGetPlaybackInfo, "GetPlaybackInfo"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSession", slotSessionTryPlay, "TryPlayAsync"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSession", slotSessionTryPause, "TryPauseAsync"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSession", slotSessionTryStop, "TryStopAsync"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSession", slotSessionTrySkipNext, "TrySkipNextAsync"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSession", slotSessionTrySkipPrevious, "TrySkipPreviousAsync"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSession", slotSessionTryTogglePlayPause, "TryTogglePlayPauseAsync"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionMediaProperties", slotMediaPropertiesGetTitle, "get_Title"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionMediaProperties", slotMediaPropertiesGetArtist, "get_Artist"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionMediaProperties", slotMediaPropertiesGetAlbumTitle, "get_AlbumTitle"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionTimelineProperties", slotTimelineGetEndTime, "get_EndTime"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionTimelineProperties", slotTimelineGetPosition, "get_Position"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionPlaybackInfo", slotPlaybackInfoGetControls, "get_Controls"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionPlaybackInfo", slotPlaybackInfoGetPlaybackStatus, "get_PlaybackStatus"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionPlaybackControls", slotControlsGetIsPlayEnabled, "get_IsPlayEnabled"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionPlaybackControls", slotControlsGetIsPauseEnabled, "get_IsPauseEnabled"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionPlaybackControls", slotControlsGetIsNextEnabled, "get_IsNextEnabled"},
		{"Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionPlaybackControls", slotControlsGetIsPreviousEnabled, "get_IsPreviousEnabled"},
	}

	for _, tc := range cases {
		t.Run(tc.interfaceName+"."+tc.method, func(t *testing.T) {
			def, ok := f.FindTypeDef(tc.interfaceName)
			if !ok {
				t.Fatalf("%s is not in the metadata", tc.interfaceName)
			}

			methods := f.Methods(def)

			// The ABI puts IUnknown in slots 0 to 2 and IInspectable in 3 to 5, so an
			// interface's own first method is slot 6.
			index := tc.slot - 6
			if index < 0 || index >= len(methods) {
				t.Fatalf("slot %d is outside %s, which has %d methods", tc.slot, tc.interfaceName, len(methods))
			}
			if methods[index].Name != tc.method {
				t.Errorf("slot %d of %s is %q, want %q", tc.slot, tc.interfaceName, methods[index].Name, tc.method)
			}
		})
	}
}

// TestAsyncSlotsMatchWindowsMetadata checks the two asynchronous interfaces, which
// live in a different metadata file.
func TestAsyncSlotsMatchWindowsMetadata(t *testing.T) {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	path := filepath.Join(root, "System32", "WinMetadata", "Windows.Foundation.winmd")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("Windows metadata not available at %s: %v", path, err)
	}

	f, err := winmd.Load(path)
	if err != nil {
		t.Fatalf("could not read %s: %v", path, err)
	}

	cases := []struct {
		interfaceName string
		slot          int
		method        string
	}{
		{"Windows.Foundation.IAsyncOperation`1", slotAsyncGetResults, "GetResults"},
		{"Windows.Foundation.IAsyncInfo", slotAsyncInfoGetStatus, "get_Status"},
	}

	for _, tc := range cases {
		t.Run(tc.interfaceName+"."+tc.method, func(t *testing.T) {
			def, ok := f.FindTypeDef(tc.interfaceName)
			if !ok {
				t.Fatalf("%s is not in the metadata", tc.interfaceName)
			}

			methods := f.Methods(def)
			index := tc.slot - 6
			if index < 0 || index >= len(methods) {
				t.Fatalf("slot %d is outside %s, which has %d methods", tc.slot, tc.interfaceName, len(methods))
			}
			if methods[index].Name != tc.method {
				t.Errorf("slot %d of %s is %q, want %q", tc.slot, tc.interfaceName, methods[index].Name, tc.method)
			}
		})
	}
}

// TestPlaybackStatusValuesMatchWindowsMetadata pins the playback status values.
//
// The shell maps them onto its own states, so a wrong value shows the wrong icon.
func TestPlaybackStatusValuesMatchWindowsMetadata(t *testing.T) {
	f := windowsMediaMetadata(t)

	def, ok := f.FindTypeDef("Windows.Media.Control.GlobalSystemMediaTransportControlsSessionPlaybackStatus")
	if !ok {
		t.Fatal("the playback status enum is not in the metadata")
	}

	want := map[string]int64{
		"Closed":   playbackStatusClosed,
		"Opened":   playbackStatusOpened,
		"Changing": playbackStatusChanging,
		"Stopped":  playbackStatusStopped,
		"Playing":  playbackStatusPlaying,
		"Paused":   playbackStatusPaused,
	}

	got := map[string]int64{}
	for _, c := range f.Constants(def) {
		value, ok := c.ConstantValue()
		if !ok {
			continue
		}
		for _, field := range f.Fields(def) {
			if field.Row == int(c.ParentIndex) {
				got[field.Name] = value
			}
		}
	}

	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %d in the metadata, want %d", name, got[name], value)
		}
	}
}
