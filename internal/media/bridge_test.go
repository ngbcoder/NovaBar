package media

import (
	"strings"
	"testing"

	"github.com/go-ole/go-ole"
)

// TestNewBridgeNeverPanics is the load-bearing safety test.
//
// NewBridge calls into WinRT on a machine that may or may not have a media session
// stack. A crash here would take down the daemon's message-pump thread, which is the
// one thing the watchdog exists to protect against. Whatever happens, it must come back
// with a usable bridge and a reason.
func TestNewBridgeNeverPanics(t *testing.T) {
	b, err := NewBridge()
	if err != nil {
		t.Fatalf("NewBridge returned an error instead of a bridge: %v", err)
	}
	if b == nil {
		t.Fatal("NewBridge returned a nil bridge")
	}
	defer releaseBridge(b)

	// An available bridge needs a reason-free status; an unavailable one must explain
	// itself, or the shell shows a hidden media area with no clue why.
	if !b.Available() && strings.TrimSpace(b.Reason()) == "" {
		t.Error("unavailable bridge has an empty reason; the user cannot act on that")
	}

	// Logged unconditionally: which branch this machine takes is environment-dependent
	// and is the first thing to look at when the media area is missing.
	t.Logf("bridge available=%v reason=%q", b.Available(), b.Reason())

	// Current must be safe to call regardless of which bridge came back.
	session, err := b.Current()
	if err == nil && session.Source != "gsmtc" {
		t.Errorf("Current reported no error but source = %q; it must not fabricate a session", session.Source)
	}

	// Transport must refuse rather than pretend, unless there is a session to
	// control. On a machine with nothing playing, refusing is the correct answer.
	if err := b.Command(CommandPlay); err == nil && session.Source != "gsmtc" {
		t.Error("Command succeeded on a bridge that reports no session")
	}
}

// TestDescribeActivationError pins the distinctions that make the message useful.
//
// A single opaque "activation failed" hides whether this is fixable, so each known
// HRESULT has to produce its own wording.
func TestDescribeActivationError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "class unregistered",
			err:  ole.NewError(hresultClassNotRegistered),
			want: "unregistered",
		},
		{
			name: "no interface",
			err:  ole.NewError(hresultNoInterface),
			want: "does not expose",
		},
		{
			name: "denied",
			err:  ole.NewError(hresultDenied),
			want: "denied",
		},
		{
			name: "unknown falls back",
			err:  ole.NewError(0x80004005),
			want: "activation failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := describeActivationFailure(tc.err)
			if !strings.Contains(got, tc.want) {
				t.Errorf("describeActivationFailure() = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// TestHarmlessROError guards the S_FALSE filter.
//
// go-ole reports S_FALSE(1) from RoInitialize as an error, and left unfiltered it makes
// the bridge claim WinRT failed on a thread that is already initialized. This is exactly
// what a naive `if err != nil` check does wrong.
func TestHarmlessROError(t *testing.T) {
	if !isHarmlessROError(ole.NewError(hresultSFalse)) {
		t.Error("S_FALSE must be treated as harmless")
	}
	if isHarmlessROError(ole.NewError(hresultClassNotRegistered)) {
		t.Error("a real failure must not be filtered as harmless")
	}
	if isHarmlessROError(nil) {
		t.Error("nil must not be treated as an S_FALSE failure")
	}
}

// TestControllerRefusesWithoutBridge checks the contract the UI depends on: with no
// bridge the controller reports no session and refuses transport, rather than showing
// a permanently Stopped track that looks like a real paused player.
func TestControllerRefusesWithoutBridge(t *testing.T) {
	c := NewController()

	if c.Attached() && !c.bridge.Available() {
		t.Error("Controller reports attached with a bridge that cannot read a session")
	}

	session := c.GetCurrentSession()
	if session.Source != "none" && session.Source != "gsmtc" {
		t.Errorf("unexpected source %q", session.Source)
	}
	if session.Source == "none" && session.Playback != StateUnknown {
		t.Errorf("unavailable controller reported playback = %q; must be unknown", session.Playback)
	}

	// Refreshing only fails when there is no session to report. On a machine with
	// music playing it is expected to succeed, so this cannot assert either way
	// without knowing the environment.
	if !c.Attached() {
		if err := c.Refresh(); err == nil {
			t.Error("Refresh succeeded with no media session")
		}
	}
}

// releaseBridge releases a bridge if it holds one.
func releaseBridge(b Bridge) {
	if r, ok := b.(interface{ Release() }); ok {
		r.Release()
	}
}
