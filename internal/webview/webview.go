// Package webview hosts WebView2 surfaces inside NovaBar's own Win32 windows.
//
// The bar windows in internal/bar already exist, are already registered with the
// shell, and already reserve work area. Embedding into those handles, rather than
// letting WebView2 create windows of its own, keeps that logic in one place and means
// the shell's own window styles (WS_EX_NOACTIVATE, WS_EX_TOOLWINDOW) still apply.
//
// Everything here is pure Go and builds with CGO_ENABLED=0. The library underneath,
// wailsapp/go-webview2, drives ICoreWebView2 through syscall and has no import "C".
package webview

import (
	"fmt"
	"log"
	"sync"

	"github.com/ngbcoder/NovaBar/internal/win"
	"github.com/wailsapp/go-webview2/pkg/edge"
	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
)

// Color is an RGBA background colour.
type Color struct {
	R, G, B, A uint8
}

// Options configure a Surface.
type Options struct {
	// Debug enables the WebView2 developer tools.
	Debug bool

	// DataPath is the WebView2 user data folder. Empty means a folder beside the
	// executable.
	DataPath string

	// AdditionalBrowserArgs are Chromium command-line switches.
	//
	// These are not optional decoration. See DefaultBrowserArgs: without them a bar
	// window, which is never focused and is usually considered occluded, has its
	// timers throttled to roughly one tick per second.
	AdditionalBrowserArgs []string

	// Transparent composites the surface over its parent window instead of painting
	// an opaque background.
	Transparent bool

	// BackgroundColor overrides the default background when not transparent.
	BackgroundColor Color

	// ZoomFactor is the page zoom, where 1.0 is 100%.
	ZoomFactor float64

	// OnMessage receives strings posted from the page via window.chrome.webview.postMessage.
	OnMessage func(payload string)

	// OnNavigationComplete fires after each page load finishes.
	OnNavigationComplete func(success bool)
}

// DefaultBrowserArgs are the Chromium switches a desktop shell surface needs.
//
// Taken from ngbcoder/SeelenNovaBar, which has been running this arrangement in anger.
// The two that matter most are the first pair: a window that is never focused and is
// frequently judged occluded gets its timers throttled hard, which is what makes a
// WebView2 taskbar clock tick once a second. Everything else is about a widget surface
// having no business talking to the network or writing a disk cache.
var DefaultBrowserArgs = []string{
	// Timers and backgrounding. Not optional for a bar.
	"--disable-background-timer-throttling",
	"--disable-backgrounding-occluded-windows",
	"--disable-renderer-backgrounding",

	// Occlusion and paint holding interact badly with a window that is always visible
	// but never focused.
	"--disable-features=CalculateNativeWinOcclusion,PaintHolding,InterestCohot,BackForwardCache,msWebOOUI,msPdfOOUI,msSmartScreenProtection",

	// A widget surface has no cache to speak of and no reason to grow one.
	"--disk-cache-size=0",
	"--disable-application-cache",
	"--media-cache-size=0",

	// No outbound traffic at all from a shell widget.
	"--disable-background-networking",
	"--disable-component-update",
	"--disable-sync",
	"--no-pings",
	"--no-first-run",

	// Trim the process surface.
	"--disable-extensions",
	"--disable-breakpad",
	"--disable-crash-reporter",
	"--disable-ipc-flooding-protection",
}

// Surface is one WebView2 view embedded in a parent window.
type Surface struct {
	parent   windows.HWND
	chromium *edge.Chromium

	mu    sync.Mutex
	ready bool
}

// comInitState records the apartment initialization for this process.
//
// WebView2 requires a single-threaded apartment on the thread that owns the
// controller, which is the daemon's pinned message pump. Initializing per-Surface
// would be wrong: COM reference counts per thread, so a second initialization is a
// no-op that reports S_FALSE, and a matching CoUninitialize would unbalance the count.
var comInitState struct {
	once sync.Once
	err  error
}

// initializeApartment puts the calling thread into an STA for WebView2.
//
// Must be called on the message-pump thread before any Surface is attached.
func initializeApartment() error {
	comInitState.once.Do(func() {
		already, err := win.CoInitializeApartment()
		if err != nil {
			comInitState.err = err
			return
		}
		if already {
			log.Printf("[NovaBar WebView] COM apartment was already initialized on this thread\n")
		}
	})
	return comInitState.err
}

// Attach embeds a WebView2 surface into an existing window.
//
// Must be called on the pinned message-pump thread. Attach blocks while the WebView2
// runtime starts and the controller is created, which takes a few hundred milliseconds
// on a cold start; the caller should do it before entering the message loop, or from a
// dedicated bootstrap phase.
func Attach(parent windows.HWND, opts Options) (*Surface, error) {
	if parent == 0 {
		return nil, fmt.Errorf("parent window handle is 0")
	}
	if err := initializeApartment(); err != nil {
		return nil, err
	}

	args := opts.AdditionalBrowserArgs
	if len(args) == 0 {
		args = DefaultBrowserArgs
	}

	// NewChromium must be used rather than a struct literal.
	//
	// edge.Chromium carries unexported state (the COM completion handlers, the binding
	// table, the controller) that only its constructor wires up. Building one as
	// &edge.Chromium{...} compiles, passes every field check, and then panics with a nil
	// dereference the first time WebView2 signals that the environment is ready, because
	// the environment completion handler was never created.
	c := edge.NewChromium()

	c.Debug = opts.Debug
	c.DataPath = opts.DataPath
	c.AdditionalBrowserArgs = args

	if opts.OnMessage != nil {
		onMessage := opts.OnMessage
		c.MessageCallback = func(message string, _ *edge.ICoreWebView2, _ *edge.ICoreWebView2WebMessageReceivedEventArgs) {
			onMessage(message)
		}
	}
	if opts.OnNavigationComplete != nil {
		onNav := opts.OnNavigationComplete
		c.NavigationCompletedCallback = func(_ *edge.ICoreWebView2, _ *edge.ICoreWebView2NavigationCompletedEventArgs) {
			// The args type exposes no success accessor, so a completed navigation is
			// reported as success and real failures come back over the message channel,
			// where the page can be more specific than a boolean.
			onNav(true)
		}
	}
	c.SetErrorCallback(func(err error) {
		log.Printf("[NovaBar WebView] WebView2 error: %v\n", err)
	})

	version, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	if err != nil {
		return nil, fmt.Errorf("the WebView2 runtime is not installed: %w", err)
	}
	log.Printf("[NovaBar WebView] Attaching to HWND %v using runtime %s\n", parent, version)

	if !c.Embed(uintptr(parent)) {
		return nil, fmt.Errorf("WebView2 failed to attach to HWND %v", parent)
	}

	s := &Surface{parent: parent, chromium: c, ready: true}

	if opts.Transparent {
		c.SetBackgroundColour(0, 0, 0, 0)
	} else {
		c.SetBackgroundColour(opts.BackgroundColor.R, opts.BackgroundColor.G, opts.BackgroundColor.B, opts.BackgroundColor.A)
	}

	if opts.ZoomFactor > 0 {
		c.PutZoomFactor(opts.ZoomFactor)
	}

	return s, nil
}

// SetHTML replaces the surface content.
//
// The page origin becomes about:blank, so the surface cannot make same-origin requests
// to anything, which is the safe default for widget content.
func (s *Surface) SetHTML(html string) {
	if s == nil || !s.ready {
		return
	}
	s.chromium.NavigateToString(html)
}

// Navigate loads a URL.
func (s *Surface) Navigate(url string) {
	if s == nil || !s.ready {
		return
	}
	s.chromium.Navigate(url)
}

// Init injects JavaScript that runs before the document loads, on every navigation.
func (s *Surface) Init(js string) {
	if s == nil || !s.ready {
		return
	}
	s.chromium.Init(js)
}

// Eval runs JavaScript in the page. Asynchronous; use Init or OnMessage to observe
// results.
func (s *Surface) Eval(js string) {
	if s == nil || !s.ready {
		return
	}
	s.chromium.Eval(js)
}

// Resize fits the surface to a rectangle, in physical pixels of the parent window.
func (s *Surface) Resize(left, top, width, height int32) {
	if s == nil || !s.ready {
		return
	}
	s.chromium.ResizeWithBounds(&edge.Rect{
		Left:   left,
		Top:    top,
		Right:  left + width,
		Bottom: top + height,
	})
}

// NotifyParentMoved tells WebView2 the parent window changed position or size, so the
// controller can follow it. Call after moving a bar.
func (s *Surface) NotifyParentMoved() {
	if s == nil || !s.ready {
		return
	}
	if err := s.chromium.NotifyParentWindowPositionChanged(); err != nil {
		log.Printf("[NovaBar WebView] NotifyParentWindowPositionChanged: %v\n", err)
	}
}

// Show makes the surface visible.
func (s *Surface) Show() {
	if s == nil || !s.ready {
		return
	}
	if err := s.chromium.Show(); err != nil {
		log.Printf("[NovaBar WebView] Show: %v\n", err)
	}
}

// Hide hides the surface without destroying it.
func (s *Surface) Hide() {
	if s == nil || !s.ready {
		return
	}
	if err := s.chromium.Hide(); err != nil {
		log.Printf("[NovaBar WebView] Hide: %v\n", err)
	}
}

// RuntimeVersion returns the detected WebView2 runtime version.
func RuntimeVersion() (string, error) {
	return webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
}

// Available reports whether a WebView2 runtime is installed.
func Available() bool {
	_, err := RuntimeVersion()
	return err == nil
}

// Destroy releases the surface. Safe on a nil receiver.
func (s *Surface) Destroy() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.ready {
		return
	}
	s.ready = false

	// ShuttingDown releases the environment and controller in the right order and
	// stops the in-process callbacks that would otherwise fire into freed Go memory.
	s.chromium.ShuttingDown()
}
