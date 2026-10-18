// Package volume reads and controls per-application audio volume.
//
// # Why Core Audio rather than GSMTC
//
// GSMTC would report what is playing and let a bar drive transport, but it needs WinRT:
// IAsyncOperation, activation factories and a property bag, hand-marshalled. Core Audio's
// ISimpleAudioVolume gives per-application volume directly, is classic COM, and go-wca
// already wraps every vtable needed.
//
// Volume is also the more useful thing for a desktop bar. It is visible at a glance and
// adjustable without switching to the application.
//
// # Scope
//
// Only the default render endpoint is used. Multi-output setups where the "current"
// endpoint differs from the system default are out of scope, and this package does not
// pretend to handle them.
//
// # Failure behaviour
//
// Nothing here fabricates. If a process cannot be named, the session reports its pid
// rather than an invented label. If the audio stack is unavailable, Available is false
// with a reason, and the shell omits the volume UI instead of showing controls that do
// nothing.
package volume

import (
	"fmt"
	"log"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"
)

// shareModeShared is AUDCLNT_SHARE_MODE_SHARED. go-wca does not export it.
const shareModeShared = 0x1

// SessionState mirrors AudioSessionState, trimmed to what the shell acts on.
type SessionState string

const (
	StateInactive SessionState = "inactive"
	StateActive   SessionState = "active"
	StateExpired  SessionState = "expired"
)

// Session is one application's audio session.
type Session struct {
	// PID owns the session. Zero means the system sounds session, which has no process.
	PID uint32 `json:"pid"`
	// ProcName is the owning executable, e.g. "Spotify.exe". Empty when the process
	// cannot be opened, which happens for elevated and protected processes.
	ProcName string `json:"proc_name"`
	// DisplayName is the session's own name, which Windows usually fills in with the
	// app's display name even when the process cannot be inspected.
	DisplayName string `json:"display_name"`
	// Volume is the session level from 0 to 1.
	Volume float32 `json:"volume"`
	// Muted is the session's mute state, which is independent of Volume.
	Muted bool `json:"muted"`
	// State is the session's activity.
	State SessionState `json:"state"`
	// SystemSounds marks the pseudo-session Windows uses for system beeps. It has no
	// owning process and is not something a user wants to see or adjust.
	SystemSounds bool `json:"system_sounds"`
}

// Label is what the UI shows. Prefers the display name, falls back to the executable,
// then to the pid, and never invents a name.
func (s Session) Label() string {
	switch {
	case s.DisplayName != "":
		return s.DisplayName
	case s.ProcName != "":
		return s.ProcName
	default:
		return fmt.Sprintf("PID %d", s.PID)
	}
}

// ErrNoSession means no audio session matched the request.
var ErrNoSession = fmt.Errorf("no audio session for that process")

// ErrNoEndpoint means there is no usable default render endpoint.
var ErrNoEndpoint = fmt.Errorf("no default audio output device")

// Controller owns the Core Audio COM objects.
//
// COM apartments are per-OS-thread and a goroutine can migrate between threads, so the
// apartment is pinned to one locked thread and every call is funnelled to it. Initialising
// COM on the calling goroutine and then calling a COM method would be a race: the call
// could land on a thread that was never initialised.
type Controller struct {
	job chan func()

	closeOnce sync.Once
	closed    chan struct{}

	mu      sync.RWMutex
	ready   bool
	reason  string
	devOnce sync.Once
}

// New starts the COM thread.
//
// Returns a Controller even when audio is unavailable, so callers have one shape to deal
// with; check Available.
func New() *Controller {
	c := &Controller{
		job:    make(chan func()),
		closed: make(chan struct{}),
	}

	go c.run()

	// Wait briefly for the thread to come up so Available is meaningful immediately
	// rather than racy for the first caller.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		settled := c.ready
		c.mu.RUnlock()
		if settled {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	return c
}

// run is the pinned COM thread.
func (c *Controller) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		c.setUnavailable(fmt.Sprintf("COM could not be initialized for audio: %v", err))
		// Keep draining so callers are not blocked forever on a dead thread.
		for {
			select {
			case <-c.closed:
				return
			case f := <-c.job:
				f()
			}
		}
	}
	defer ole.CoUninitialize()

	c.mu.Lock()
	c.ready = true
	c.mu.Unlock()

	for {
		select {
		case <-c.closed:
			return
		case f := <-c.job:
			f()
		}
	}
}

func (c *Controller) setUnavailable(reason string) {
	c.mu.Lock()
	c.ready = true
	c.reason = reason
	c.mu.Unlock()
}

// Available reports whether the audio stack is usable.
//
// Nil-safe: the daemon can legitimately hold no controller, and a nil dereference here
// would panic inside an RPC handler, which drops the widget's connection rather than
// returning an error the widget can show.
func (c *Controller) Available() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.reason == ""
}

// Reason explains why audio is unavailable, or is empty when it is available.
func (c *Controller) Reason() string {
	if c == nil {
		return "the audio subsystem is not running"
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.reason
}

// Close stops the COM thread. Safe to call more than once.
func (c *Controller) Close() {
	c.closeOnce.Do(func() { close(c.closed) })
}

// do runs fn on the COM thread and waits for it.
func (c *Controller) do(fn func() error) error {
	if c == nil {
		return fmt.Errorf("audio is unavailable: %s", c.Reason())
	}
	if !c.Available() {
		return fmt.Errorf("audio is unavailable: %s", c.Reason())
	}

	done := make(chan error, 1)
	select {
	case c.job <- func() { done <- fn() }:
	case <-c.closed:
		return fmt.Errorf("audio controller is closed")
	}

	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		// A wedged COM call must not hang the daemon's RPC thread forever.
		return fmt.Errorf("audio call timed out")
	}
}

// List returns the audio sessions worth showing.
//
// The system-sounds session is excluded: it has no owning process and is not something a
// user adjusts. A session whose process cannot be opened is still returned, with its pid,
// because hiding it would understate what is playing.
func (c *Controller) List() ([]Session, error) {
	var out []Session
	err := c.do(func() error {
		ctl, err := openSessionControl()
		if err != nil {
			return err
		}

		var count int
		if err := ctl.enumerator.GetCount(&count); err != nil {
			return err
		}

		for i := 0; i < count; i++ {
			var session *wca.IAudioSessionControl
			if err := ctl.enumerator.GetSession(i, &session); err != nil {
				continue // one bad session must not hide the rest
			}

			s := Session{}

			var state uint32
			if err := session.GetState(&state); err == nil {
				switch state {
				case 0:
					s.State = StateInactive
				case 1:
					s.State = StateActive
				default:
					s.State = StateExpired
				}
			}

			// Windows' own pseudo-session for system beeps.
			if ctl2, err := asControl2(session); err == nil && ctl2 != nil {
				if err := ctl2.IsSystemSoundsSession(); err == nil {
					s.SystemSounds = true
				}
			}
			if s.SystemSounds {
				continue
			}

			if err := session.GetDisplayName(&s.DisplayName); err == nil {
				// Windows fills this with the app's display name even when the process
				// itself cannot be opened, so it is the better label when available.
				s.DisplayName = strings.TrimSpace(s.DisplayName)
			}

			if ctl2, err := asControl2(session); err == nil && ctl2 != nil {
				if err := ctl2.GetProcessId(&s.PID); err == nil && s.PID != 0 {
					s.ProcName = exeName(s.PID)
				}
			}

			if sav, err := asSimpleVolume(session); err == nil && sav != nil {
				var level float32
				if err := sav.GetMasterVolume(&level); err == nil {
					s.Volume = clampLevel(level)
				}
				var muted bool
				if err := sav.GetMute(&muted); err == nil {
					s.Muted = muted
				}
			}

			// A session with no identifiable owner and no name is noise.
			if s.PID == 0 && s.DisplayName == "" {
				continue
			}
			out = append(out, s)
		}
		return nil
	})

	return out, err
}

// SetVolume sets one application's session level. Level is clamped to 0..1.
//
// Windows refuses this for some app types, most notably Store apps on newer builds. That
// refusal is reported, not hidden.
func (c *Controller) SetVolume(pid uint32, level float32) error {
	return c.applyToSession(pid, func(sav *wca.ISimpleAudioVolume) error {
		// A GUID event context tags the change so notifications can be correlated;
		// nil is accepted and means "no correlation".
		return sav.SetMasterVolume(clampLevel(level), nil)
	})
}

// SetMute mutes or unmutes one application's session.
func (c *Controller) SetMute(pid uint32, muted bool) error {
	return c.applyToSession(pid, func(sav *wca.ISimpleAudioVolume) error {
		return sav.SetMute(muted, nil)
	})
}

// applyToSession runs fn against the session owned by pid.
func (c *Controller) applyToSession(pid uint32, fn func(*wca.ISimpleAudioVolume) error) error {
	return c.do(func() error {
		ctl, err := openSessionControl()
		if err != nil {
			return err
		}

		var count int
		if err := ctl.enumerator.GetCount(&count); err != nil {
			return err
		}

		for i := 0; i < count; i++ {
			var session *wca.IAudioSessionControl
			if err := ctl.enumerator.GetSession(i, &session); err != nil {
				continue
			}
			ctl2, err := asControl2(session)
			if err != nil || ctl2 == nil {
				continue
			}
			var owner uint32
			if err := ctl2.GetProcessId(&owner); err != nil || owner != pid {
				continue
			}
			sav, err := asSimpleVolume(session)
			if err != nil || sav == nil {
				return fmt.Errorf("that session does not expose a volume control")
			}
			return fn(sav)
		}
		return ErrNoSession
	})
}

// sessionControl bundles what is needed to walk sessions on the COM thread.
//
// The COM objects are intentionally not cached between calls. They are only valid for
// their enumerator's lifetime, and holding them invites use-after-release; enumerating is
// cheap enough that correctness wins.
type sessionControl struct {
	device     *wca.IMMDevice
	manager    *wca.IAudioSessionManager2
	enumerator *wca.IAudioSessionEnumerator
}

// openSessionControl opens the default render endpoint and its session enumerator.
func openSessionControl() (*sessionControl, error) {
	var devEnum *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0,
		wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &devEnum); err != nil {
		return nil, fmt.Errorf("cannot reach the audio device enumerator: %w", err)
	}

	const (
		eRender  = 0
		eConsole = 0
	)
	var dev *wca.IMMDevice
	if err := devEnum.GetDefaultAudioEndpoint(eRender, eConsole, &dev); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoEndpoint, err)
	}

	var mgr *wca.IAudioSessionManager2
	if err := dev.Activate(wca.IID_IAudioSessionManager2, shareModeShared, nil, &mgr); err != nil {
		return nil, fmt.Errorf("cannot open the audio session manager: %w", err)
	}

	var enumerator *wca.IAudioSessionEnumerator
	if err := mgr.GetSessionEnumerator(&enumerator); err != nil {
		return nil, fmt.Errorf("cannot enumerate audio sessions: %w", err)
	}

	return &sessionControl{device: dev, manager: mgr, enumerator: enumerator}, nil
}

// asControl2 casts a session control to IAudioSessionControl2.
//
// go-wca wraps the vtables but exports no QueryInterface helper, so slot 0 is called
// directly. One primitive, and the object is validated by checking the returned pointer.
func asControl2(session *wca.IAudioSessionControl) (*wca.IAudioSessionControl2, error) {
	var out *wca.IAudioSessionControl2
	if err := queryInterface(session, wca.IID_IAudioSessionControl2, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("session does not implement IAudioSessionControl2")
	}
	return out, nil
}

// asSimpleVolume casts a session control to ISimpleAudioVolume.
func asSimpleVolume(session *wca.IAudioSessionControl) (*wca.ISimpleAudioVolume, error) {
	var out *wca.ISimpleAudioVolume
	if err := queryInterface(session, wca.IID_ISimpleAudioVolume, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("session does not implement ISimpleAudioVolume")
	}
	return out, nil
}

// queryInterface performs a COM QueryInterface.
//
// out must be a pointer to a pointer to the target struct type. reflect turns that into a
// raw out-parameter, which is the approach go-wca uses internally.
func queryInterface(self *wca.IAudioSessionControl, iid *ole.GUID, out interface{}) error {
	pout := reflect.ValueOf(out).Elem().Addr().Pointer()

	hr, _, _ := syscall.Syscall(
		self.VTable().QueryInterface,
		3,
		uintptr(unsafe.Pointer(self)),
		uintptr(unsafe.Pointer(iid)),
		pout)
	runtime.KeepAlive(out)

	if hr != 0 {
		return ole.NewError(hr)
	}
	return nil
}

// clampLevel keeps a level inside 0..1.
func clampLevel(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// exeName returns the executable name for pid.
//
// Fails for elevated and protected processes, which is why Session falls back to the
// session's own display name and finally to the pid. Inventing a name would be worse than
// admitting we do not have one.
func exeName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}

	path := windows.UTF16ToString(buf[:n])
	if i := strings.LastIndexByte(path, '\\'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// LogUnavailable reports an unavailable controller once at startup, so a missing audio
// device is visible in the log rather than silently absent from the dock.
func (c *Controller) LogUnavailable() {
	if reason := c.Reason(); reason != "" {
		log.Printf("[NovaBar Volume] audio unavailable: %s\n", reason)
	}
}
