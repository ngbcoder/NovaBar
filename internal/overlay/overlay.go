// Package overlay hosts transient, non-reserving surfaces such as the app launcher.
//
// An overlay is deliberately not an AppBar. An AppBar claims work area from every
// window on the display, which is correct for a permanent bar and wrong for something
// that appears for a few seconds and then vanishes: reserving space for it would visibly
// reflow the user's desktop every time they opened the launcher.
//
// An overlay therefore owns a plain topmost window that takes no work-area reservation
// and disappears from the shell's point of view the moment it is destroyed.
package overlay

import (
	"fmt"
	"log"

	"github.com/ngbcoder/NovaBar/internal/webview"
	"github.com/ngbcoder/NovaBar/internal/win"
	"golang.org/x/sys/windows"
)

// windowClassName is the shared class for overlay windows.
const windowClassName = "NovaBarOverlayClass"

// wndProc is the single shared window-procedure thunk for overlay windows.
//
// Module level so exactly one callback thunk exists, matching the rest of the project.
var wndProc = windows.NewCallback(func(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case win.WM_DESTROY:
		return 0
	}
	return win.DefWindowProc(hwnd, msg, wParam, lParam)
})

// Options configure an overlay.
type Options struct {
	// Widget is the widget directory to load.
	Widget string
	// Width and Height are the surface size in pixels.
	Width  int32
	Height int32
	// Anchor is a fraction of the monitor, 0.5 being centered.
	AnchorX float64
	AnchorY float64
	// OnClose fires when the overlay dismisses itself.
	OnClose func()
	// OnMessage receives strings posted from the page.
	OnMessage func(payload string)

	// Position places the window: "center" (the default) or "bottom".
	//
	// "bottom" sits above the taskbar and reserves no work area, which is how the
	// macOS-style dock floats. Unlike a real AppBar it does not shrink the desktop.
	Position string
	// SpanMonitor widens the window to the full monitor, so content inside it can center
	// itself while the window stays a single rectangle.
	SpanMonitor bool
	// BottomMargin is the gap between a bottom-positioned window and the screen edge.
	BottomMargin int32
	// Transparent composites the window over the desktop instead of painting a
	// background, which is what lets a rounded dock sit on the wallpaper.
	Transparent bool
	// Persistent keeps the window alive when the page posts the dismiss sentinel. The
	// dock is always present; it is not something the user opened.
	Persistent bool
}

// Overlay is a transient popup surface.
type Overlay struct {
	hwnd    windows.HWND
	surface *webview.Surface
	closed  bool
	// persistent marks a window that is always present rather than something the user
	// opened, so a page posting the dismiss sentinel does not close it.
	persistent bool
	// opts are the options the overlay was created with, kept so a resize can put
	// the window back where it belongs.
	opts Options
}

// Show creates and displays an overlay centered on the primary monitor.
//
// Must run on the pinned message-pump thread: window creation and the WebView2 controller
// both belong to that thread.
func Show(opts Options) (*Overlay, error) {
	if opts.Width <= 0 {
		opts.Width = 640
	}
	if opts.Height <= 0 {
		opts.Height = 420
	}
	if opts.AnchorX == 0 {
		opts.AnchorX = 0.5
	}
	if opts.AnchorY == 0 {
		opts.AnchorY = 0.4
	}
	if opts.Widget == "" {
		opts.Widget = "launcher"
	}

	// Work out the work area so the overlay centers over usable space rather than
	// possibly landing under a bar or the taskbar.
	screenW := int32(win.GetSystemMetrics(0))
	screenH := int32(win.GetSystemMetrics(1))
	if screenW <= 0 || screenH <= 0 {
		screenW, screenH = 1920, 1080
	}

	x := int32(float64(screenW-opts.Width) * opts.AnchorX)
	y := int32(float64(screenH-opts.Height) * opts.AnchorY)

	// A bottom-positioned window hugs the screen edge.
	//
	// Anchored to the screen, not the work area, deliberately. The dock reserves no space,
	// so the work area imposes no constraint on it. Following one puts the dock above the
	// strip Windows reserves for its own taskbar even when that taskbar is hidden: the
	// shell re-asserts that reservation on Windows 11 and this shell does not fight it for
	// a strip neither of them needs. A gap under the dock is worse than an unused
	// reservation.
	//
	// Horizontally it is centred rather than spanning the monitor. A window that spans
	// the monitor covers the whole strip along the bottom of the screen, and a window
	// that covers the screen is a window that takes the clicks: the user could not press
	// a button in an application along the bottom edge. The overlay is sized to the
	// content instead, so only the content is in the way.
	if opts.Position == "bottom" {
		if opts.SpanMonitor {
			opts.Width = screenW
		}
		x = (screenW - opts.Width) / 2
		y = screenH - opts.Height - opts.BottomMargin
	}

	hwnd, err := createOverlayWindow(x, y, opts.Width, opts.Height)
	if err != nil {
		return nil, err
	}

	o := &Overlay{hwnd: hwnd, persistent: opts.Persistent, opts: opts}

	bg := webview.Color{R: 20, G: 22, B: 26, A: 255}
	if opts.Transparent {
		bg.A = 0
	}
	surface, err := webview.Attach(hwnd, webview.Options{
		Transparent:     opts.Transparent,
		BackgroundColor: bg,
		OnMessage: func(payload string) {
			if payload == dismissMessage && !opts.Persistent {
				o.Close()
				return
			}
			if opts.OnMessage != nil {
				opts.OnMessage(payload)
			}
		},
	})
	if err != nil {
		win.DestroyWindow(hwnd)
		return nil, err
	}
	o.surface = surface

	surface.Resize(0, 0, opts.Width, opts.Height)
	surface.Show()

	log.Printf("[NovaBar Overlay] Shown %dx%d at %d,%d\n", opts.Width, opts.Height, x, y)
	return o, nil
}

// dismissMessage is what a widget posts to close its own overlay.
const dismissMessage = "__novabar_dismiss__"

// DismissMessage is the payload a widget posts to request its overlay close.
const DismissMessage = dismissMessage

// createOverlayWindow creates the popup window an overlay renders into.
func createOverlayWindow(x, y, width, height int32) (windows.HWND, error) {
	classPtr, err := windows.UTF16PtrFromString(windowClassName)
	if err != nil {
		return 0, err
	}
	titlePtr, _ := windows.UTF16PtrFromString("NovaBar Launcher")

	// Set background brush to 0 so Win32 doesn't paint a solid color block underneath.
	wcx := win.WNDCLASSEX{
		LpfnWndProc:   wndProc,
		HbrBackground: 0,
		LpszClassName: classPtr,
	}
	// Registering the same class twice reports ERROR_CLASS_ALREADY_EXISTS, which is
	// expected when a second overlay is shown after the first was destroyed.
	_, _ = win.RegisterClassEx(&wcx)

	// WS_EX_TOOLWINDOW keeps it out of the taskbar and Alt+Tab.
	// 0x00080000 is WS_EX_LAYERED, required to enable proper window transparency compositing.
	exStyle := uint32(win.WS_EX_TOOLWINDOW | win.WS_EX_TOPMOST | 0x00080000)
	style := uint32(win.WS_POPUP | win.WS_VISIBLE)

	return win.CreateWindowEx(
		exStyle,
		classPtr,
		titlePtr,
		style,
		x, y, width, height,
		0, 0, 0, nil,
	)
}

// Resize changes the overlay's size and puts it back where it belongs.
//
// A bottom-positioned overlay is re-centred on the way, because the whole point of
// resizing one is that its content is narrower than the screen.
//
// Must run on the message pump thread: it moves the window and resizes the WebView2
// surface, and both belong to the thread that created them.
func (o *Overlay) Resize(width, height int32) error {
	if o == nil || o.closed || o.hwnd == 0 {
		return fmt.Errorf("overlay is not open")
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("overlay size %dx%d is not usable", width, height)
	}

	screenW := int32(win.GetSystemMetrics(0))
	screenH := int32(win.GetSystemMetrics(1))
	if screenW <= 0 || screenH <= 0 {
		screenW, screenH = 1920, 1080
	}

	x, y := int32(0), int32(0)
	switch o.opts.Position {
	case "bottom":
		x = (screenW - width) / 2
		y = screenH - height - o.opts.BottomMargin
	default:
		x = int32(float64(screenW-width) * o.opts.AnchorX)
		y = int32(float64(screenH-height) * o.opts.AnchorY)
	}

	if err := win.SetWindowPos(o.hwnd, 0, x, y, width, height, win.SWP_NOZORDER|win.SWP_NOACTIVATE); err != nil {
		return fmt.Errorf("could not resize the overlay window: %w", err)
	}

	if o.surface != nil {
		o.surface.Resize(0, 0, width, height)
	}

	o.opts.Width = width
	o.opts.Height = height

	log.Printf("[NovaBar Overlay] Resized to %dx%d at %d,%d\n", width, height, x, y)
	return nil
}

// HWND returns the overlay's window handle.
func (o *Overlay) HWND() windows.HWND {
	if o == nil {
		return 0
	}
	return o.hwnd
}

// Surface returns the WebView2 surface, so the caller can push bootstrap data.
func (o *Overlay) Surface() *webview.Surface {
	if o == nil {
		return nil
	}
	return o.surface
}

// SetBootstrap injects the endpoint and permissions before the widget loads.
func (o *Overlay) SetBootstrap(js string) {
	if o == nil || o.surface == nil {
		return
	}
	o.surface.Init(js)
}

// Navigate loads the widget.
func (o *Overlay) Navigate(url string) {
	if o == nil || o.surface == nil {
		return
	}
	o.surface.Navigate(url)
}

// Close destroys the overlay. Safe to call more than once.
func (o *Overlay) Close() {
	if o == nil || o.closed {
		return
	}
	o.closed = true

	if o.surface != nil {
		o.surface.Destroy()
		o.surface = nil
	}
	if o.hwnd != 0 {
		win.DestroyWindow(o.hwnd)
		o.hwnd = 0
	}

	// The pixels of a topmost window that vanishes are not reliably replaced.
	win.RefreshDesktop()

	log.Println("[NovaBar Overlay] Closed")
}

// String describes the overlay for logging.
func (o *Overlay) String() string {
	if o == nil || o.hwnd == 0 {
		return "<closed overlay>"
	}
	return fmt.Sprintf("overlay(hwnd=%d)", o.hwnd)
}
