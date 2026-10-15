package bar

import (
	"fmt"
	"log"
	"sync"

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
		LpszClassName: classNamePtr,
	}

	_, _ = win.RegisterClassEx(&wcx)

	// Create borderless topmost layered window
	exStyle := uint32(win.WS_EX_TOOLWINDOW | win.WS_EX_NOACTIVATE | win.WS_EX_APPWINDOW)
	style := uint32(win.WS_POPUP | win.WS_VISIBLE)

	hwnd, err := win.CreateWindowEx(
		exStyle,
		classNamePtr,
		titlePtr,
		style,
		0, 0, 0, 0,
		0, 0, 0, nil,
	)
	if err != nil || hwnd == 0 {
		return fmt.Errorf("failed to create AppBar window: %w", err)
	}
	ab.hwnd = hwnd

	// Register AppBar with Windows
	var abd win.APPBARDATA
	abd.CbSize = uint32(win.RECT{}.Height()) // size of APPBARDATA
	abd.HWnd = ab.hwnd
	abd.UCallbackMessage = WM_APPBAR_NOTIFY

	win.SHAppBarMessage(win.ABM_NEW, &abd)
	ab.registered = true

	// Position and reserve screen work area
	ab.UpdatePosition()

	log.Printf("[NovaBar+ AppBar] Registered AppBar successfully (HWND: %v)\n", ab.hwnd)
	return nil
}

// UpdatePosition queries and sets the system work area reserved for NovaBar.
func (ab *AppBarManager) UpdatePosition() {
	if !ab.registered || ab.hwnd == 0 {
		return
	}

	screenW := int32(1920) // Default fallback
	var abd win.APPBARDATA
	abd.HWnd = ab.hwnd
	abd.UEdge = ab.edge

	abd.Rc.Left = 0
	abd.Rc.Right = screenW
	if ab.edge == win.ABE_TOP {
		abd.Rc.Top = 0
		abd.Rc.Bottom = ab.height
	} else {
		abd.Rc.Top = 1080 - ab.height
		abd.Rc.Bottom = 1080
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
