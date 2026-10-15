package bar

import (
	"fmt"
	"log"
	"sync"
	"unsafe"

	"github.com/novabar-shell/novabar/internal/win"
	"golang.org/x/sys/windows"
)

const (
	WM_USER         = 0x0400
	WM_APPBAR_NOTIFY = WM_USER + 101
	AppBarClassName = "NovaBarAppBarClass"
)

// AppBarManager handles registration, size reservation, and unregistration with the Windows shell.
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

// CreateAndRegister creates a borderless AppBar window and registers it using SHAppBarMessage.
func (ab *AppBarManager) CreateAndRegister() error {
	ab.mu.Lock()
	defer ab.mu.Unlock()

	if ab.registered {
		return nil
	}

	classNamePtr, _ := windows.UTF16PtrFromString(AppBarClassName)
	titlePtr, _ := windows.UTF16PtrFromString("NovaBar+ AppBar")

	modGdi32 := windows.NewLazySystemDLL("gdi32.dll")
	procCreateSolidBrush := modGdi32.NewProc("CreateSolidBrush")
	// Dark background RGB(20, 22, 26) = 0x001A1614
	hBrush, _, _ := procCreateSolidBrush.Call(0x001A1614)

	// Register window class
	wcx := win.WNDCLASSEX{
		LpfnWndProc: windows.NewCallback(func(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
			switch msg {
			case WM_APPBAR_NOTIFY:
				// Handle shell notifications (ABN_POSCHANGED, etc.)
				return 0
			}
			return win.DefWindowProc(hwnd, msg, wParam, lParam)
		}),
		HbrBackground: windows.Handle(hBrush),
		LpszClassName: classNamePtr,
	}

	_, _ = win.RegisterClassEx(&wcx)

	// Create borderless topmost layered window
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

	// Register AppBar with Windows
	var abd win.APPBARDATA
	abd.CbSize = uint32(unsafe.Sizeof(abd))
	abd.HWnd = ab.hwnd
	abd.UCallbackMessage = WM_APPBAR_NOTIFY

	win.SHAppBarMessage(win.ABM_NEW, &abd)
	ab.registered = true

	// Position and reserve screen work area
	ab.UpdatePosition()

	win.ShowWindow(ab.hwnd, win.SW_SHOW)
	log.Printf("[NovaBar+ AppBar] Registered AppBar successfully (HWND: %v)\n", ab.hwnd)
	return nil
}

// UpdatePosition queries and sets the system work area reserved for NovaBar.
func (ab *AppBarManager) UpdatePosition() {
	if !ab.registered || ab.hwnd == 0 {
		return
	}

	screenW := win.GetSystemMetrics(0) // SM_CXSCREEN
	screenH := win.GetSystemMetrics(1) // SM_CYSCREEN
	if screenW <= 0 {
		screenW = 1920
	}
	if screenH <= 0 {
		screenH = 1080
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

	// Request work area reduction from Windows shell
	win.SHAppBarMessage(win.ABM_QUERYPOS, &abd)
	win.SHAppBarMessage(win.ABM_SETPOS, &abd)

	// Reposition the physical window to the reserved rectangle
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

// Unregister cleanly removes the AppBar and restores full screen work area.
func (ab *AppBarManager) Unregister() {
	ab.mu.Lock()
	defer ab.mu.Unlock()

	if !ab.registered || ab.hwnd == 0 {
		return
	}

	var abd win.APPBARDATA
	abd.HWnd = ab.hwnd
	win.SHAppBarMessage(win.ABM_REMOVE, &abd)
	win.DestroyWindow(ab.hwnd)
	ab.registered = false
	ab.hwnd = 0
	log.Println("[NovaBar+ AppBar] Unregistered AppBar and restored desktop work area.")
}

// HWND returns the AppBar window handle.
func (ab *AppBarManager) HWND() windows.HWND {
	return ab.hwnd
}
