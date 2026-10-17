package bar

import (
	"fmt"
	"log"
	"sync"
	"unsafe"

	"github.com/ngbcoder/NovaBar/internal/win"
	"golang.org/x/sys/windows"
)

const (
	WM_USER          = 0x0400
	WM_APPBAR_NOTIFY = WM_USER + 101
	AppBarClassName  = "NovaBarAppBarClass"
)

// appbarWndProc is the single shared window-procedure thunk for the AppBar class.
//
// It is a package-level var so exactly one callback thunk exists. The previous
// implementation allocated a new thunk inside CreateAndRegister, which leaks one per
// call.
var appbarWndProc = windows.NewCallback(func(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_APPBAR_NOTIFY:
		// Shell notifications (ABN_POSCHANGED etc.) arrive here. Position updates are
		// driven explicitly by the daemon rather than reactively, so nothing to do.
		return 0
	case win.WM_DESTROY:
		return 0
	}
	return win.DefWindowProc(hwnd, msg, wParam, lParam)
})

// registry tracks every HWND registered with the shell as an AppBar.
//
// ABM_NEW is not reference counted: registering the same window twice leaves the shell
// in a state that ABM_REMOVE does not fully unwind. The per-instance flag in
// AppBarManager catches the obvious case, but nothing stopped two managers from
// registering bars on the same edge. Config validation now forbids duplicate edges, and
// this registry is the second line of defence.
var registry = struct {
	mu    sync.Mutex
	hwnds map[windows.HWND]string // HWND -> edge name, for diagnostics
}{
	hwnds: make(map[windows.HWND]string),
}

// registerWithShell registers an HWND unless it is already registered.
//
// Returns true if this call performed the registration.
func registerWithShell(hwnd windows.HWND, edge string) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	if _, exists := registry.hwnds[hwnd]; exists {
		return false
	}
	registry.hwnds[hwnd] = edge
	return true
}

// unregisterFromShell forgets an HWND.
func unregisterFromShell(hwnd windows.HWND) {
	registry.mu.Lock()
	delete(registry.hwnds, hwnd)
	registry.mu.Unlock()
}

// RegisteredEdges returns a snapshot of registered bars for status output.
func RegisteredEdges() map[windows.HWND]string {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	out := make(map[windows.HWND]string, len(registry.hwnds))
	for hwnd, edge := range registry.hwnds {
		out[hwnd] = edge
	}
	return out
}

// AppBarManager handles registration, size reservation, and unregistration with the
// Windows shell for a single screen edge.
type AppBarManager struct {
	mu         sync.Mutex
	hwnd       windows.HWND
	registered bool
	edge       uint32 // win.ABE_TOP, ABE_BOTTOM, ABE_LEFT or ABE_RIGHT
	size       int32  // reserved thickness along the edge's normal axis
	name       string
	bounds     win.RECT

	// requested is the rectangle this bar occupies on screen, computed by us.
	// grantedRect is what the shell actually credited against the work area, which
	// may be nothing at all: Windows refuses to let an AppBar overlap the real
	// taskbar, so a bottom bar registered while the taskbar is visible gets no credit.
	//
	// Both are kept because they answer different questions. "Where is the bar drawn"
	// is requested; "did the shell shrink the desktop for us" is grantedRect.
	requested     win.RECT
	grantedRect   win.RECT
	grantedReason string
}

// NewAppBarManager creates an instance of AppBarManager.
func NewAppBarManager(edge uint32, size int32, name string) *AppBarManager {
	if name == "" {
		name = EdgeName(edge)
	}
	return &AppBarManager{
		edge: edge,
		size: size,
		name: name,
	}
}

// EdgeName returns a readable name for an ABE_* edge constant.
func EdgeName(edge uint32) string {
	switch edge {
	case win.ABE_TOP:
		return "top"
	case win.ABE_BOTTOM:
		return "bottom"
	case win.ABE_LEFT:
		return "left"
	case win.ABE_RIGHT:
		return "right"
	default:
		return "unknown"
	}
}

// Name returns the bar's label.
func (ab *AppBarManager) Name() string { return ab.name }

// CreateAndRegister creates a borderless AppBar window and registers it using
// SHAppBarMessage.
//
// Runs on the pinned message-pump thread: window creation and AppBar registration
// must happen there so the shell's callbacks land on the thread that owns the window.
func (ab *AppBarManager) CreateAndRegister() error {
	ab.mu.Lock()
	defer ab.mu.Unlock()

	if ab.registered {
		return nil
	}

	classNamePtr, _ := windows.UTF16PtrFromString(AppBarClassName)
	titlePtr, _ := windows.UTF16PtrFromString("NovaBar AppBar")

	modGdi32 := windows.NewLazySystemDLL("gdi32.dll")
	procCreateSolidBrush := modGdi32.NewProc("CreateSolidBrush")
	// Dark background RGB(20, 22, 26) = 0x001A1614
	hBrush, _, _ := procCreateSolidBrush.Call(0x001A1614)

	// Register window class. ERROR_CLASS_ALREADY_EXISTS is expected on a second
	// registration within one process and is not an error.
	wcx := win.WNDCLASSEX{
		LpfnWndProc:   appbarWndProc,
		HbrBackground: windows.Handle(hBrush),
		LpszClassName: classNamePtr,
	}
	if _, err := win.RegisterClassEx(&wcx); err != nil {
		log.Printf("[NovaBar AppBar] RegisterClassEx reported: %v (continuing)\n", err)
	}

	// Borderless topmost tool window.
	//
	// WS_EX_NOACTIVATE is the important one: without it, clicking the bar pulls focus
	// away from whatever the user is actually working in. Seelen builds its bar windows
	// with the equivalent of exactly this (Tauri's focusable(false)).
	exStyle := uint32(win.WS_EX_TOOLWINDOW | win.WS_EX_TOPMOST | win.WS_EX_NOACTIVATE)
	style := uint32(win.WS_POPUP | win.WS_VISIBLE)

	// Create at a sane starting size for the edge; the real geometry is set by
	// updatePositionLocked once the screen metrics are known.
	startW, startH := int32(1920), ab.size
	if ab.isVertical() {
		startW, startH = ab.size, int32(1080)
	}

	hwnd, err := win.CreateWindowEx(
		exStyle,
		classNamePtr,
		titlePtr,
		style,
		0, 0, startW, startH,
		0, 0, 0, nil,
	)
	if err != nil || hwnd == 0 {
		return fmt.Errorf("failed to create AppBar window: %w", err)
	}
	ab.hwnd = hwnd

	var abd win.APPBARDATA
	abd.CbSize = uint32(unsafe.Sizeof(abd))
	abd.HWnd = ab.hwnd
	abd.UCallbackMessage = WM_APPBAR_NOTIFY

	if !registerWithShell(ab.hwnd, ab.name) {
		// Already registered: do not send ABM_NEW again, since the shell does not
		// reference count it and a second ABM_REMOVE would leave the work area shrunk.
		log.Printf("[NovaBar AppBar] %s bar HWND %v was already registered; skipping ABM_NEW\n", ab.name, ab.hwnd)
	} else {
		win.SHAppBarMessage(win.ABM_NEW, &abd)

		// Keep the bar above other always-on-top windows.
		abd.LParam = win.ABS_ALWAYSONTOP
		win.SHAppBarMessage(win.ABM_SETSTATE, &abd)
		abd.LParam = 0
	}
	ab.registered = true

	ab.updatePositionLocked()

	win.ShowWindow(ab.hwnd, win.SW_SHOW)
	log.Printf("[NovaBar AppBar] Registered %s bar (HWND: %v, size: %d)\n", ab.name, ab.hwnd, ab.size)
	return nil
}

// isVertical reports whether the bar occupies a left or right edge.
func (ab *AppBarManager) isVertical() bool {
	return ab.edge == win.ABE_LEFT || ab.edge == win.ABE_RIGHT
}

// UpdatePosition re-reserves the screen work area and repositions the window.
func (ab *AppBarManager) UpdatePosition() {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	ab.updatePositionLocked()
}

func (ab *AppBarManager) updatePositionLocked() {
	if !ab.registered || ab.hwnd == 0 {
		return
	}

	screenW := win.GetSystemMetrics(0) // SM_CXSCREEN
	screenH := win.GetSystemMetrics(1) // SM_CYSCREEN
	if screenW <= 0 || screenH <= 0 {
		log.Printf("[NovaBar AppBar] GetSystemMetrics returned %dx%d; leaving reservation unchanged\n", screenW, screenH)
		return
	}

	var abd win.APPBARDATA
	abd.CbSize = uint32(unsafe.Sizeof(abd))
	abd.HWnd = ab.hwnd
	abd.UEdge = ab.edge

	abd.Rc.Left = 0
	abd.Rc.Right = screenW
	abd.Rc.Top = 0
	abd.Rc.Bottom = screenH

	switch ab.edge {
	case win.ABE_TOP:
		abd.Rc.Bottom = ab.size
	case win.ABE_BOTTOM:
		abd.Rc.Top = screenH - ab.size
	case win.ABE_LEFT:
		abd.Rc.Right = ab.size
	case win.ABE_RIGHT:
		abd.Rc.Left = screenW - ab.size
	}

	// Ask the shell to shrink the work area around our rectangle, then commit it.
	win.SHAppBarMessage(win.ABM_QUERYPOS, &abd)
	win.SHAppBarMessage(win.ABM_SETPOS, &abd)

	// The shell is allowed to shrink the proposal to fit, and it does not always do so
	// cleanly: it has been observed returning an inverted rectangle (Top greater than
	// Bottom) when the edge is already taken by the native taskbar.
	//
	// The shell also refuses outright to let an AppBar overlap the real taskbar, which
	// is the normal case for a bottom bar while the taskbar is still shown.
	granted := abd.Rc
	reason := ""

	switch {
	case ab.rectIsInverted(granted):
		reason = "the shell returned an inverted rectangle"
	case granted.Width() == 0 || granted.Height() == 0:
		reason = "the shell granted no space"
	}

	if reason != "" {
		log.Printf("[NovaBar AppBar] %s bar: %s (shell returned %dx%d at %d,%d)\n",
			ab.name, reason, granted.Width(), granted.Height(), granted.Left, granted.Top)
	}

	// Position the window ourselves either way. The shell's reservation and the window's
	// own geometry are separate concerns: if we fall back to the shell's refusal we get
	// a zero-size window and an invisible bar, when the truth is that the bar simply
	// could not claim work-area credit. Drawing the strip and letting the engine
	// compensate is more useful and more honest.
	//
	// The rect is normally supplied by Manager.recomputeRects, which insets each bar by
	// the thickness of the others so adjacent edges do not both claim the corner. Only
	// fall back to the bare edge calculation when running standalone.
	if !ab.rectIsValid(ab.requested) {
		ab.requested = ab.edgeInset(screenW, screenH, ab.size)
	}
	ab.grantedRect = granted
	ab.grantedReason = reason

	window := ab.requested
	if ab.rectIsValid(window) {
		_ = win.SetWindowPos(
			ab.hwnd,
			win.HWND_TOPMOST,
			window.Left,
			window.Top,
			window.Width(),
			window.Height(),
			win.SWP_NOACTIVATE|win.SWP_SHOWWINDOW,
		)
	}
}

// SetRequestedRect sets the rectangle this bar occupies, computed by the Manager.
func (ab *AppBarManager) SetRequestedRect(rect win.RECT) {
	ab.mu.Lock()
	ab.requested = rect
	hwnd := ab.hwnd
	ab.mu.Unlock()

	if hwnd == 0 || !ab.rectIsValid(rect) {
		return
	}
	_ = win.SetWindowPos(
		hwnd,
		win.HWND_TOPMOST,
		rect.Left,
		rect.Top,
		rect.Width(),
		rect.Height(),
		win.SWP_NOACTIVATE|win.SWP_SHOWWINDOW,
	)
}

// rectIsValid reports whether a rectangle has positive extent on both axes.
func (ab *AppBarManager) rectIsValid(r win.RECT) bool {
	return r.Width() > 0 && r.Height() > 0
}

// rectIsInverted reports whether a rectangle has negative extent on either axis.
func (ab *AppBarManager) rectIsInverted(r win.RECT) bool {
	return r.Width() < 0 || r.Height() < 0
}

// edgeInset builds a rectangle of the given thickness hugging this bar's edge of a
// screenW x screenH display.
func (ab *AppBarManager) edgeInset(screenW, screenH, thickness int32) win.RECT {
	var r win.RECT
	r.Left = 0
	r.Right = screenW
	r.Top = 0
	r.Bottom = screenH

	if thickness < 0 {
		thickness = 0
	}

	switch ab.edge {
	case win.ABE_TOP:
		r.Bottom = thickness
	case win.ABE_BOTTOM:
		r.Top = screenH - thickness
	case win.ABE_LEFT:
		r.Right = thickness
	case win.ABE_RIGHT:
		r.Left = screenW - thickness
	}
	return r
}

// Unregister removes the AppBar and restores the full screen work area.
func (ab *AppBarManager) Unregister() {
	ab.mu.Lock()
	defer ab.mu.Unlock()

	if !ab.registered || ab.hwnd == 0 {
		return
	}

	var abd win.APPBARDATA
	abd.CbSize = uint32(unsafe.Sizeof(abd))
	abd.HWnd = ab.hwnd
	win.SHAppBarMessage(win.ABM_REMOVE, &abd)
	unregisterFromShell(ab.hwnd)
	win.DestroyWindow(ab.hwnd)
	ab.registered = false
	ab.hwnd = 0
	ab.bounds = win.RECT{}

	// The window is gone but its pixels may not be. Repaint so the user is not left
	// looking at a bar that has already been removed.
	win.RefreshDesktop()

	log.Printf("[NovaBar AppBar] Unregistered %s bar and released its reserved work area.\n", ab.name)
}

// HWND returns the AppBar window handle, or 0 when not registered.
func (ab *AppBarManager) HWND() windows.HWND {
	return ab.hwnd
}

// Size returns the reserved thickness.
func (ab *AppBarManager) Size() int32 {
	return ab.size
}

// Granted returns the thickness the shell actually reserved against the work area, or
// 0 if it reserved none.
//
// Never negative: the shell has been observed returning an inverted rectangle when the
// edge is taken, whose raw Height() is negative but which means "no space".
//
// This is deliberately not the same as Size. A bar can be drawn and still have been
// granted no work-area credit, and confusing the two is how a bottom bar ends up
// invisible while the status output claims everything is fine.
func (ab *AppBarManager) Granted() int32 {
	ab.mu.Lock()
	defer ab.mu.Unlock()

	if !ab.registered {
		return 0
	}

	var thickness int32
	switch ab.edge {
	case win.ABE_TOP, win.ABE_BOTTOM:
		thickness = ab.grantedRect.Height()
	case win.ABE_LEFT, win.ABE_RIGHT:
		thickness = ab.grantedRect.Width()
	default:
		return 0
	}

	if thickness < 0 {
		return 0
	}
	return thickness
}

// GrantedReason explains why Granted is less than Size, or "" when the shell honoured
// the request in full.
func (ab *AppBarManager) GrantedReason() string {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	if !ab.registered {
		return "not registered"
	}
	return ab.grantedReason
}

// Bounds returns the rectangle the bar occupies, as computed by us rather than as
// credited by the shell.
func (ab *AppBarManager) Bounds() win.RECT {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return ab.requested
}

// SetSize changes the reserved thickness and re-applies the reservation.
func (ab *AppBarManager) SetSize(size int32) {
	ab.mu.Lock()
	ab.size = size
	ab.updatePositionLocked()
	ab.mu.Unlock()
}

// Edge returns the reserved screen edge (win.ABE_TOP and friends).
func (ab *AppBarManager) Edge() uint32 {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return ab.edge
}

// Registered reports whether the AppBar is currently registered with the shell.
func (ab *AppBarManager) Registered() bool {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return ab.registered
}
