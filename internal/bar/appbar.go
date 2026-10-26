package bar

import (
	"fmt"
	"log"
	"sync"
	"time"
	"unsafe"

	"github.com/ngbcoder/NovaBar/internal/win"
	"golang.org/x/sys/windows"
)

const (
	WM_USER          = 0x0400
	WM_APPBAR_NOTIFY = WM_USER + 101
	AppBarClassName  = "NovaBarAppBarClass"
)

var appbarWndProc = windows.NewCallback(func(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_APPBAR_NOTIFY:
		return 0
	case win.WM_DESTROY:
		return 0
	}
	return win.DefWindowProc(hwnd, msg, wParam, lParam)
})

var registry = struct {
	mu    sync.Mutex
	hwnds map[windows.HWND]string
}{
	hwnds: make(map[windows.HWND]string),
}

func registerWithShell(hwnd windows.HWND, edge string) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	if _, exists := registry.hwnds[hwnd]; exists {
		return false
	}
	registry.hwnds[hwnd] = edge
	return true
}

func unregisterFromShell(hwnd windows.HWND) {
	registry.mu.Lock()
	delete(registry.hwnds, hwnd)
	registry.mu.Unlock()
}

func RegisteredEdges() map[windows.HWND]string {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	out := make(map[windows.HWND]string, len(registry.hwnds))
	for hwnd, edge := range registry.hwnds {
		out[hwnd] = edge
	}
	return out
}

type AppBarManager struct {
	mu         sync.Mutex
	hwnd       windows.HWND
	registered bool
	edge       uint32
	size       int32
	name       string
	bounds     win.RECT

	requested     win.RECT
	grantedRect   win.RECT
	grantedReason string

	lastClaim   time.Time
	monitorRect win.RECT
}

func NewAppBarManager(monitorRect win.RECT, edge uint32, size int32, name string) *AppBarManager {
	if name == "" {
		name = EdgeName(edge)
	}
	return &AppBarManager{
		monitorRect: monitorRect,
		edge:        edge,
		size:        size,
		name:        name,
	}
}

func (ab *AppBarManager) MonitorRect() win.RECT {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return ab.monitorRect
}

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

func (ab *AppBarManager) Name() string { return ab.name }

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
	hBrush, _, _ := procCreateSolidBrush.Call(0x001A1614)

	wcx := win.WNDCLASSEX{
		LpfnWndProc:   appbarWndProc,
		HbrBackground: windows.Handle(hBrush),
		LpszClassName: classNamePtr,
	}
	if _, err := win.RegisterClassEx(&wcx); err != nil {
		log.Printf("[NovaBar AppBar] RegisterClassEx reported: %v (continuing)\n", err)
	}

	exStyle := uint32(win.WS_EX_TOOLWINDOW | win.WS_EX_TOPMOST | win.WS_EX_NOACTIVATE)
	style := uint32(win.WS_POPUP | win.WS_VISIBLE)

	startW, startH := int32(1920), ab.size
	if ab.isVertical() {
		startW, startH = ab.size, int32(1080)
	}

	hwnd, err := win.CreateWindowEx(
		exStyle,
		classNamePtr,
		titlePtr,
		style,
		ab.monitorRect.Left, ab.monitorRect.Top, startW, startH,
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
		log.Printf("[NovaBar AppBar] %s bar HWND %v was already registered; skipping ABM_NEW\n", ab.name, ab.hwnd)
	} else {
		win.SHAppBarMessage(win.ABM_NEW, &abd)

		abd.LParam = win.ABS_ALWAYSONTOP
		win.SHAppBarMessage(win.ABM_SETSTATE, &abd)
		abd.LParam = 0
	}
	ab.registered = true

	ab.updatePositionLocked()

	win.ShowWindow(ab.hwnd, win.SW_SHOW)
	log.Printf("[NovaBar AppBar] Registered %s bar on monitor (%d,%d) (HWND: %v, size: %d)\n",
		ab.name, ab.monitorRect.Left, ab.monitorRect.Top, ab.hwnd, ab.size)
	return nil
}

func (ab *AppBarManager) isVertical() bool {
	return ab.edge == win.ABE_LEFT || ab.edge == win.ABE_RIGHT
}

func (ab *AppBarManager) UpdatePosition() {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	ab.updatePositionLocked()
}

func (ab *AppBarManager) updatePositionLocked() {
	if !ab.registered || ab.hwnd == 0 {
		return
	}

	screenW := ab.monitorRect.Width()
	screenH := ab.monitorRect.Height()
	if screenW <= 0 || screenH <= 0 {
		return
	}

	var abd win.APPBARDATA
	abd.CbSize = uint32(unsafe.Sizeof(abd))
	abd.HWnd = ab.hwnd
	abd.UEdge = ab.edge

	abd.Rc.Left = ab.monitorRect.Left
	abd.Rc.Right = ab.monitorRect.Right
	abd.Rc.Top = ab.monitorRect.Top
	abd.Rc.Bottom = ab.monitorRect.Bottom

	switch ab.edge {
	case win.ABE_TOP:
		abd.Rc.Bottom = ab.monitorRect.Top + ab.size
	case win.ABE_BOTTOM:
		abd.Rc.Top = ab.monitorRect.Bottom - ab.size
	case win.ABE_LEFT:
		abd.Rc.Right = ab.monitorRect.Left + ab.size
	case win.ABE_RIGHT:
		abd.Rc.Left = ab.monitorRect.Right - ab.size
	}

	win.SHAppBarMessage(win.ABM_QUERYPOS, &abd)
	win.SHAppBarMessage(win.ABM_SETPOS, &abd)

	granted := abd.Rc
	reason := ""

	switch {
	case ab.rectIsInverted(granted):
		reason = "the shell returned an inverted rectangle"
	case granted.Width() == 0 || granted.Height() == 0:
		reason = "the shell granted no space"
	}

	if !ab.rectIsValid(ab.requested) {
		ab.requested = ab.edgeInsetForMonitor(ab.monitorRect, ab.size)
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

func (ab *AppBarManager) edgeInsetForMonitor(mon win.RECT, thickness int32) win.RECT {
	r := mon
	if thickness < 0 {
		thickness = 0
	}
	switch ab.edge {
	case win.ABE_TOP:
		r.Bottom = mon.Top + thickness
	case win.ABE_BOTTOM:
		r.Top = mon.Bottom - thickness
	case win.ABE_LEFT:
		r.Right = mon.Left + thickness
	case win.ABE_RIGHT:
		r.Left = mon.Right - thickness
	}
	return r
}

func (ab *AppBarManager) claimDue() bool {
	ab.mu.Lock()
	defer ab.mu.Unlock()

	if time.Since(ab.lastClaim) < claimCooldown {
		return false
	}
	ab.lastClaim = time.Now()
	return true
}

func (ab *AppBarManager) noteClaimed() {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	ab.lastClaim = time.Now()
}

const claimCooldown = 30 * time.Second

func (ab *AppBarManager) SetScope(scope string) {}

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

func (ab *AppBarManager) rectIsValid(r win.RECT) bool {
	return r.Width() > 0 && r.Height() > 0
}

func (ab *AppBarManager) rectIsInverted(r win.RECT) bool {
	return r.Width() < 0 || r.Height() < 0
}

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

	win.RefreshDesktop()

	log.Printf("[NovaBar AppBar] Unregistered %s bar on monitor (%d,%d)\n",
		ab.name, ab.monitorRect.Left, ab.monitorRect.Top)
}

func (ab *AppBarManager) HWND() windows.HWND {
	return ab.hwnd
}

func (ab *AppBarManager) Size() int32 {
	return ab.size
}

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

func (ab *AppBarManager) GrantedReason() string {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	if !ab.registered {
		return "not registered"
	}
	return ab.grantedReason
}

func (ab *AppBarManager) Bounds() win.RECT {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return ab.requested
}

func (ab *AppBarManager) SetSize(size int32) {
	ab.mu.Lock()
	ab.size = size
	ab.updatePositionLocked()
	ab.mu.Unlock()
}

func (ab *AppBarManager) Edge() uint32 {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return ab.edge
}

func (ab *AppBarManager) Registered() bool {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return ab.registered
}
