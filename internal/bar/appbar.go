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

// AppBarManager handles registration, size reservation, and unregistration with the
// Windows shell.
type AppBarManager struct {
	mu         sync.Mutex
	hwnd       windows.HWND
	registered bool
	edge       uint32 // win.ABE_TOP or win.ABE_BOTTOM
	height     int32
	bounds     win.RECT
}

// NewAppBarManager creates an instance of AppBarManager.
func NewAppBarManager(edge uint32, height int32) *AppBarManager {
	return &AppBarManager{
		edge:   edge,
		height: height,
	}
}

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

	// Borderless topmost tool window. WS_EX_TOOLWINDOW keeps it out of the taskbar
	// and Alt+Tab, which matters because NovaBar replaces the taskbar.
	exStyle := uint32(win.WS_EX_TOOLWINDOW | win.WS_EX_TOPMOST)
	style := uint32(win.WS_POPUP | win.WS_VISIBLE)

	hwnd, err := win.CreateWindowEx(
		exStyle,
		classNamePtr,
		titlePtr,
		style,
		0, 0, 1920, ab.height,
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

	win.SHAppBarMessage(win.ABM_NEW, &abd)
	ab.registered = true

	ab.updatePositionLocked()

	win.ShowWindow(ab.hwnd, win.SW_SHOW)
	log.Printf("[NovaBar AppBar] Registered AppBar (HWND: %v, edge: %d, height: %d)\n", ab.hwnd, ab.edge, ab.height)
	return nil
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
	if ab.edge == win.ABE_TOP {
		abd.Rc.Top = 0
		abd.Rc.Bottom = ab.height
	} else {
		abd.Rc.Top = screenH - ab.height
		abd.Rc.Bottom = screenH
	}

	// Ask the shell to shrink the work area around our rectangle, then commit it.
	win.SHAppBarMessage(win.ABM_QUERYPOS, &abd)
	win.SHAppBarMessage(win.ABM_SETPOS, &abd)

	_ = win.SetWindowPos(
		ab.hwnd,
		win.HWND_TOPMOST,
		abd.Rc.Left,
		abd.Rc.Top,
		abd.Rc.Width(),
		abd.Rc.Height(),
		win.SWP_NOACTIVATE|win.SWP_SHOWWINDOW,
	)
	ab.bounds = abd.Rc
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
	win.DestroyWindow(ab.hwnd)
	ab.registered = false
	ab.hwnd = 0
	ab.bounds = win.RECT{}
	log.Println("[NovaBar AppBar] Unregistered AppBar and released the reserved work area.")
}

// HWND returns the AppBar window handle, or 0 when not registered.
func (ab *AppBarManager) HWND() windows.HWND {
	return ab.hwnd
}

// Edge returns the reserved screen edge (win.ABE_TOP or win.ABE_BOTTOM).
func (ab *AppBarManager) Edge() uint32 {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return ab.edge
}

// Bounds returns the reserved rectangle.
func (ab *AppBarManager) Bounds() win.RECT {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return ab.bounds
}

// Registered reports whether the AppBar is currently registered with the shell.
func (ab *AppBarManager) Registered() bool {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return ab.registered
}