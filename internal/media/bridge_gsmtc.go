//go:build windows

package media

import (
	"errors"
	"fmt"

	"github.com/go-ole/go-ole"
)

// sessionManagerClass is the WinRT runtime class name for the GSMTC session manager.
//
// The namespace is Windows.Media.Control. An earlier version of this file used
// Windows.GlobalSystemMediaTransportControls, which is not a real namespace, and drew the
// conclusion from the resulting REGDB_E_CLASSNOTREG that the machine had no media stack.
//
// A wrong activatable class name is indistinguishable from an absent one at the API
// boundary: both are REGDB_E_CLASSNOTREG. That mistake produced a confident, wrong
// diagnosis and a pile of investigation into the environment, the app identity and the
// activation contract. Verify the class name before concluding anything about the
// environment.
const sessionManagerClass = "Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager"

// iidUnknown is requested rather than the typed session-manager interface.
//
// Asking the factory for IID_IUnknown is the one acquisition step verified to work: it
// returns a valid object for this class. The typed IID was never confirmed and is not
// needed yet, because nothing below can read the object.
//
// Verifying identifiers against the live object, rather than from memory, is the lesson
// from the class-name mistake. See docs/gsmtc-m5-findings.md.
var iidUnknown = ole.NewGUID("00000000-0000-0000-c000-000000000046")

// HRESULTs worth distinguishing. A single opaque "activation failed" hides the one fact
// that decides whether this is fixable, so the two common cases get named.
const (
	hresultClassNotRegistered = 0x80040154 // REGDB_E_CLASSNOTREG
	hresultNoInterface        = 0x800401F0 // E_NOINTERFACE
	hresultDenied             = 0x80070005 // E_ACCESSDENIED

	// hresultSFalse is success-already-done. It is not a failure.
	hresultSFalse = 0x00000001
)

// RO_INIT_MULTITHREADED. The daemon calls this from its pinned pump thread, so a
// multi-threaded apartment avoids stalling the message loop behind a WinRT call.
const roInitMultithreaded = 1

// gsmtcBridge holds an activated GSMTC session manager.
//
// Only activation is implemented. Reading session state needs IPropertySet/IMapView
// property access over the WinRT ABI, which is untestable here (see the note in
// NewBridge) so it is deliberately not written: shipping untested marshalling that can
// terminate the process would be worse than reporting the session as unreadable.
type gsmtcBridge struct {
	manager *ole.IInspectable
}

// Available reports whether session data can be served.
//
// False even though the manager is attached, because nothing can read it yet. Reporting
// true here would show an empty media area and look like a bug rather than unfinished
// work.
func (b *gsmtcBridge) Available() bool { return false }

// Reason explains why an attached manager still yields no data.
func (b *gsmtcBridge) Reason() string {
	return "GSMTC is attached, but session property reads are not implemented yet; " +
		"the media area stays hidden until they are"
}

// Current reports no session.
//
// The read path needs IPropertySet/IMapView property access over the WinRT ABI. It is
// not written, because it cannot be exercised on a machine where activation is refused
// and untested vtable marshalling can terminate the process outright. Returning an error
// is the honest outcome.
func (b *gsmtcBridge) Current() (MediaSession, error) {
	return MediaSession{Playback: StateUnknown, Source: "none"}, errNoSession
}

// Command refuses for the same reason Current returns nothing: with no session state
// there is no playback control to reach, and guessing risks acting on the wrong one.
func (b *gsmtcBridge) Command(TransportCommand) error { return errNoBridge }

// Release drops the COM reference. The daemon does not currently need to detach, but a
// COM object held for the process lifetime should still be releasable.
func (b *gsmtcBridge) Release() {
	if b != nil && b.manager != nil {
		b.manager.Release()
		b.manager = nil
	}
}

// NewBridge returns the platform media bridge.
//
// Activation is attempted for real. Depending on the machine it either attaches, or
// returns an unavailableBridge carrying a classified reason, so the shell can say what
// is actually wrong instead of a blanket "not supported".
func NewBridge() (Bridge, error) {
	// RoInitialize, not CoInitializeEx. Setting up a COM apartment is not the same
	// thing as initializing WinRT, and skipping this was what made an earlier
	// hand-rolled attempt fail with ERROR_INVALID_PARAMETER on every class including
	// trivially registered ones.
	//
	// S_FALSE means the thread was already initialized, which is harmless. go-ole
	// reports it as an error, so it is filtered explicitly rather than by string.
	if err := ole.RoInitialize(roInitMultithreaded); err != nil && !isHarmlessROError(err) {
		return &unavailableBridge{
			reason: "WinRT could not be initialized on this thread: " + err.Error(),
		}, nil
	}

	manager, err := ole.RoGetActivationFactory(sessionManagerClass, iidUnknown)
	if err != nil {
		return &unavailableBridge{
			reason: "the Windows GSMTC session manager is not available: " + classifyActivationError(err),
		}, nil
	}

	return &gsmtcBridge{manager: manager}, nil
}

// isHarmlessROError reports whether a RoInitialize failure is benign.
func isHarmlessROError(err error) bool {
	return hresultOf(err) == hresultSFalse
}

// hresultOf extracts an HRESULT from a go-ole error, or 0 if it is not one.
func hresultOf(err error) uint32 {
	var oleErr *ole.OleError
	if errors.As(err, &oleErr) {
		return uint32(oleErr.Code())
	}
	return 0
}

// classifyActivationError turns an activation HRESULT into an actionable sentence.
func classifyActivationError(err error) string {
	switch hresultOf(err) {
	case hresultClassNotRegistered:
		return "the OS reports the class as unregistered (REGDB_E_CLASSNOTREG), " +
			"so this Windows build has no usable media session stack"
	case hresultNoInterface:
		return "the class exists but does not expose the session manager interface"
	case hresultDenied:
		return "access was denied by the OS"
	}
	return fmt.Sprintf("activation failed (%v)", err)
}
