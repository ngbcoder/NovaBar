//go:build windows

package media

import (
	"errors"
	"sync"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/ngbcoder/NovaBar/internal/winrt"
)

// sessionManagerClass is the WinRT runtime class name for the GSMTC session manager.
//
// The namespace is Windows.Media.Control. An earlier version of this file used
// Windows.GlobalSystemMediaTransportControls, which is not a real namespace, and drew
// the conclusion from the resulting REGDB_E_CLASSNOTREG that the machine had no media stack.
//
// A wrong activatable class name is indistinguishable from an absent one at the API
// boundary: both are REGDB_E_CLASSNOTREG. That mistake produced a confident, wrong
// diagnosis and a pile of investigation into the environment, the app identity and the
// activation contract. Verify the class name before concluding anything about the
// environment.
const sessionManagerClass = "Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager"

// iidUnknown is requested when the typed interface cannot be asked for directly.
//
// Asking the factory for IID_IUnknown is the one acquisition step verified to work: it
// returns a valid object for this class. It is also the fallback for the case where
// the runtime hands back an activation factory instead of the statics object.
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

// RO_INIT_MULTITHREADED. The media bridge runs on its own thread, so a
// multi-threaded apartment lets the runtime deliver completions without waiting for
// the caller's thread.
const roInitMultithreaded = 1

// bridgeStartupTimeout bounds how long the bridge waits for its thread to come up.
const bridgeStartupTimeout = 10 * time.Second

// gsmtcBridge reads media sessions from the Windows media session manager.
//
// It owns a thread of its own, and every WinRT call happens on that thread. The reason
// is the apartment: WinRT initialises per thread, and an object created in one
// apartment cannot be used from another that has not been initialised. The daemon
// serves media over RPC, on whichever goroutine the request arrived on, so sharing
// one thread is the only way to keep the calls legal.
type gsmtcBridge struct {
	jobs chan func()
	quit chan struct{}
	done chan struct{}

	closeOnce sync.Once

	mu     sync.RWMutex
	reader *gsmtcReader
	reason string
}

// Available reports whether session data can be served.
//
// It is false while the manager has not been reached, so the shell hides the media
// area rather than showing an empty one that looks like a bug.
func (b *gsmtcBridge) Available() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.reader != nil
}

// Reason explains why an attached manager still yields no data.
//
// An empty reason means there is nothing wrong.
func (b *gsmtcBridge) Reason() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.reason
}

// Current reports the session that is playing now.
//
// No session is a normal state, not a failure: it is what a machine with nothing
// playing reports.
func (b *gsmtcBridge) Current() (MediaSession, error) {
	var session MediaSession

	err, ran := b.run(func(r *gsmtcReader) error {
		var readErr error
		session, readErr = r.currentSession()
		return readErr
	})
	if !ran {
		return MediaSession{Playback: StateUnknown, Source: "none"}, errNoSession
	}
	if err != nil {
		if errors.Is(err, errNoCurrentSession) {
			return MediaSession{Playback: StateUnknown, Source: "none"}, errNoSession
		}
		return MediaSession{Playback: StateUnknown, Source: "none"}, err
	}

	return session, nil
}

// Command applies a transport command to the current session.
func (b *gsmtcBridge) Command(cmd TransportCommand) error {
	var err error
	_, ran := b.run(func(r *gsmtcReader) error {
		err = r.command(cmd)
		return err
	})
	if !ran {
		return errNoBridge
	}
	return err
}

// run executes a job on the bridge's thread.
//
// The second result reports whether the job ran at all, which is false when the
// bridge has been shut down or its thread could not be started.
func (b *gsmtcBridge) run(job func(*gsmtcReader) error) (error, bool) {
	type outcome struct {
		err error
	}

	done := make(chan outcome, 1)

	select {
	case b.jobs <- func() {
		r, err := b.acquire()
		if err != nil {
			done <- outcome{err}
			return
		}
		done <- outcome{job(r)}
	}:
	case <-b.quit:
		return nil, false
	}

	select {
	case result := <-done:
		return result.err, true
	case <-b.quit:
		return nil, false
	}
}

// acquire returns the reader, activating the manager if it is not up yet.
//
// It runs on the bridge's thread, which is the only thread that may touch the
// reader's WinRT objects.
func (b *gsmtcBridge) acquire() (*gsmtcReader, error) {
	b.mu.Lock()
	reader := b.reader
	b.mu.Unlock()

	if reader != nil {
		return reader, nil
	}

	reader, err := newGSMTCReader()
	if err != nil {
		b.mu.Lock()
		b.reason = describeActivationFailure(err)
		b.mu.Unlock()
		return nil, err
	}

	b.mu.Lock()
	b.reader = reader
	b.reason = ""
	b.mu.Unlock()
	return reader, nil
}

// Release shuts the bridge down and releases the WinRT objects it holds.
func (b *gsmtcBridge) Release() {
	b.closeOnce.Do(func() {
		close(b.quit)
	})
	<-b.done
}

// NewBridge returns the platform media bridge.
//
// Activation is attempted for real. Depending on the machine it either attaches, or
// returns an unavailableBridge carrying a classified reason, so the shell can say what
// is actually wrong instead of a blanket "not supported".
func NewBridge() (Bridge, error) {
	b := &gsmtcBridge{
		jobs: make(chan func()),
		quit: make(chan struct{}),
		done: make(chan struct{}),
	}

	ready := make(chan error, 1)
	go b.serve(ready)

	select {
	case err := <-ready:
		if err != nil {
			b.Release()
			return &unavailableBridge{
				reason: "the Windows GSMTC session manager is not available: " + describeActivationFailure(err),
			}, nil
		}
	case <-time.After(bridgeStartupTimeout):
		b.Release()
		return &unavailableBridge{
			reason: "the Windows GSMTC session manager did not respond in time",
		}, nil
	}

	return b, nil
}

// serve runs the bridge's thread.
//
// It initializes WinRT on this thread and then does nothing but run jobs, so that
// every call into the runtime happens in the apartment that was set up for it.
func (b *gsmtcBridge) serve(ready chan<- error) {
	defer close(b.done)

	// RoInitialize, not CoInitializeEx. Setting up a COM apartment is not the same
	// thing as initializing WinRT, and skipping this was what made an earlier
	// hand-rolled attempt fail with ERROR_INVALID_PARAMETER on every class including
	// trivially registered ones.
	//
	// S_FALSE means the thread was already initialized, which is harmless. go-ole
	// reports it as an error, so it is filtered explicitly rather than by string.
	if err := ole.RoInitialize(roInitMultithreaded); err != nil && !isHarmlessROError(err) {
		ready <- errors.New("WinRT could not be initialized on this thread: " + err.Error())
		return
	}
	defer winrt.RoUninitialize()

	// Attach eagerly so that availability is known at startup rather than on the
	// first request, and so the reason reaches the log.
	if _, err := b.acquire(); err != nil {
		ready <- err
		// The thread stays up: the media stack may become reachable later, and a
		// retry costs one activation.
	} else {
		ready <- nil
	}

	for {
		select {
		case job := <-b.jobs:
			job()
		case <-b.quit:
			b.releaseReader()
			return
		}
	}
}

// releaseReader drops the WinRT objects the bridge holds.
func (b *gsmtcBridge) releaseReader() {
	b.mu.Lock()
	reader := b.reader
	b.reader = nil
	b.mu.Unlock()

	if reader != nil {
		reader.manager.Release()
	}
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

// describeActivationFailure turns a failure into an actionable sentence.
func describeActivationFailure(err error) string {
	switch hresultOf(err) {
	case hresultClassNotRegistered:
		return "the OS reports the class as unregistered (REGDB_E_CLASSNOTREG), " +
			"so this Windows build has no usable media session stack"
	case hresultNoInterface:
		return "the class exists but does not expose the session manager interface"
	case hresultDenied:
		return "access was denied by the OS"
	}

	var hr winrt.HRESULT
	if errors.As(err, &hr) {
		return describeHresult(hr)
	}
	return "activation failed (" + err.Error() + ")"
}

// describeHresult renders a result code from the runtime in words.
func describeHresult(hr winrt.HRESULT) string {
	switch hr {
	case winrt.ENoInterface:
		return "the class exists but does not expose the session manager interface"
	case winrt.EUnexpected:
		return "the media session manager returned something unusable"
	case winrt.EChangedState:
		return "the media session manager is no longer in a state that can answer"
	}
	return hr.Error()
}
