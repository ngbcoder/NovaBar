package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ngbcoder/NovaBar/internal/bar"
	"github.com/ngbcoder/NovaBar/internal/config"
	"github.com/ngbcoder/NovaBar/internal/hotkey"
	"github.com/ngbcoder/NovaBar/internal/launcher"
	"github.com/ngbcoder/NovaBar/internal/media"
	"github.com/ngbcoder/NovaBar/internal/onboarding"
	"github.com/ngbcoder/NovaBar/internal/overlay"
	"github.com/ngbcoder/NovaBar/internal/pins"
	"github.com/ngbcoder/NovaBar/internal/rpc"
	"github.com/ngbcoder/NovaBar/internal/tiling"
	"github.com/ngbcoder/NovaBar/internal/tracker"
	"github.com/ngbcoder/NovaBar/internal/volume"
	"github.com/ngbcoder/NovaBar/internal/watchdog"
	"github.com/ngbcoder/NovaBar/internal/webview"
	"github.com/ngbcoder/NovaBar/internal/win"
	"github.com/ngbcoder/NovaBar/internal/workspace"
	"golang.org/x/sys/windows"
)

// eventDebounce is how long the event pump waits after the last window event before
// reconciling. Windows emits bursts: one window opening can produce show, namemode
// change and foreground events.
const eventDebounce = 50 * time.Millisecond

// statsPollInterval is the pub/sub heartbeat period for system.stats.
const statsPollInterval = 2 * time.Second

// mediaPollInterval is how often the media session is re-read.
//
// The media stack does publish change events, but subscribing to one means a callback
// crossing a thread boundary, and that is where the crashes in this area came from.
// Polling cannot fault.
const mediaPollInterval = 2 * time.Second

// taskbarGuardInterval is how often the taskbar is checked for the shell having
// brought it back.
//
// Half a second is well inside the time the shell takes to restore the window, so
// the taskbar is never visibly there. The check is a window walk, and half a second
// is frequent enough to be unnoticeable and rare enough to cost nothing.
const taskbarGuardInterval = 500 * time.Millisecond

// daemon owns the long-lived components and their shutdown ordering.
type daemon struct {
	cfg     config.Config
	watcher *config.Watcher

	taskbars      *win.TaskbarState
	bars          *bar.Manager
	surfaces      map[string]*webview.Surface // keyed by bar name
	widgetSecrets map[string]string           // keyed by bar name
	widgets       string                      // resolved widget root

	// hideTaskbar is whether the native taskbar should be hidden.
	//
	// It is kept apart from the config because the guard reads it on a timer, and
	// the config is written from the config watcher and the settings surface.
	hideTaskbar atomic.Bool

	// quitting is set once the daemon is on its way out, and stops the guard from
	// touching the taskbar while the desktop is being restored.
	quitting atomic.Bool

	// guardMu is held while the guard is hiding the taskbar, so the teardown can
	// wait for an action in flight rather than racing it.
	guardMu sync.Mutex

	// launcher is the transient launcher overlay, nil when not showing. Unlike the bars
	// it reserves no work area, because reserving space for something that appears for
	// a few seconds would reflow the whole desktop every time it opened.
	launcher       *overlay.Overlay
	launcherSecret string

	// settings is the settings overlay, nil when closed. Separate from the launcher so the
	// two can be open independently, and each revokes its own credential.
	settings       *overlay.Overlay
	settingsSecret string

	// dock is the floating macOS-style dock. An overlay rather than an AppBar, because
	// it reserves no work area.
	dock       *overlay.Overlay
	dockSecret string

	// zones is the zone picker waiting for the user to pick a cell, if any.
	zones *zonePicker
	// zoneOverlay is the picker surface, and zoneSecret its credential.
	zoneOverlay *overlay.Overlay
	zoneSecret  string

	trk      *tracker.WindowTracker
	engine   *tiling.Engine
	spaces   *workspace.Manager
	indexer  *launcher.Indexer
	media    *media.Controller
	pinStore *pins.Store
	audio    *volume.Controller
	rpcs     *rpc.Server
	hotkeys  *hotkey.Manager
	dog      *watchdog.Watchdog

	// quit is signalled by the signal handler, which hands off to the pump thread.
	// Win32 requires the quit to originate on the pump thread, so the signal handler
	// cannot tear anything down directly.
	quitOnce sync.Once

	// pumpThread is the OS thread id running the message loop. Captured as early as
	// possible so a signal arriving during startup still has somewhere to post.
	pumpThread uint32

	// tiling guards against re-entrant tiling: moving windows generates the events
	// that trigger tiling.
	tilingMu  sync.Mutex
	tilingNow bool

	// pendingOnboarding is set during setup and consumed once the pump is running.
	pendingOnboarding bool

	// pumpTasks carries work that has to run on the message pump thread.
	//
	// WebView2 is bound to the thread that created it, so a bar resize has to
	// happen there. The config watcher and the settings surface both run on other
	// goroutines, so they hand the work over rather than doing it themselves.
	pumpTasks chan func()
}

// Run starts the daemon and pumps messages until quit.
func Run() {
	// The message pump must own its thread for the life of the process. Hooks, hotkeys
	// and the AppBar all deliver to this thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	log.SetFlags(log.Ltime)
	log.Println("[NovaBar] Starting daemon")

	d := &daemon{
		taskbars:      win.NewTaskbarState(),
		bars:          bar.NewManager(),
		surfaces:      make(map[string]*webview.Surface, 4),
		widgetSecrets: make(map[string]string, 4),
		trk:           tracker.NewWindowTracker(),
		spaces:        workspace.NewManager(),
		pumpTasks:     make(chan func(), 8),
	}
	// Capture the pump thread before anything can signal us, so an early Ctrl+C still
	// reaches the message loop.
	d.pumpThread = windows.GetCurrentThreadId()

	// Windows 11 only. Logged rather than refused: an unsupported build may still work,
	// and hard-failing a shell would be worse than the risk. The check exists so a
	// Windows 10 user gets a clear explanation instead of mysterious failures.
	if version, err := win.GetOsVersion(); err != nil {
		log.Printf("[NovaBar] Could not read the Windows version: %v\n", err)
	} else if reason := version.UnsupportedReason(); reason != "" {
		log.Printf("[NovaBar] WARNING: %s\n", reason)
	}

	if err := d.setup(); err != nil {
		log.Printf("[NovaBar] Fatal during setup: %v", err)
		d.teardown()
		return
	}

	d.registerHotkeys()
	d.installSignalHandler()
	go d.eventPump()
	go d.statsPump()
	go d.configPump()
	go d.mediaPump()
	go d.taskbarGuard()
	go d.barGuard()

	log.Println("[NovaBar] Running message pump. Use 'shellctl --rescue' or the rescue hotkey to exit.")

	// Only now, with the watchdog, IPC control channel and rescue hotkey all live, is
	// it safe to put a modal in front of the user.
	if d.pendingOnboarding {
		d.pendingOnboarding = false
		go func() {
			// Brief pause so the pump is definitely servicing messages before a modal
			// takes over the thread's input queue.
			time.Sleep(500 * time.Millisecond)
			cfg := d.cfg
			onboarding.ShowWelcomeNotice(&cfg)
			d.cfg = cfg
		}()
	}

	d.pump()
	d.teardown()
}

// Ordering is load-bearing. The watchdog, the IPC control channel and the hotkeys all
// come up before anything that can block or interfere with the desktop, because they
// are the user's way out. Onboarding in particular used to run first and could hold
// the process for as long as the modal went unanswered, leaving no rescue path at all
// during exactly the window the user is most likely to need one.
func (d *daemon) setup() error {
	cfg, err := config.Load()
	if err != nil {
		// Load already fell back to an inert config; say so loudly rather than
		// appearing to start normally.
		log.Printf("[NovaBar] CONFIG ERROR: %v", err)
		log.Printf("[NovaBar] Running with all layout features disabled until %s is fixed\n", d.configPath())
	}
	d.cfg = cfg

	firstRun := cfg.General.FirstRun
	if firstRun {
		// Clear the flag in memory now so nothing else acts on it; the modal itself
		// runs later, once the pump is alive.
		d.cfg.General.FirstRun = false
	}

	// The watchdog goes up before anything that can strand the user. From here on, a
	// crash cannot leave the desktop without a taskbar.
	dog, err := watchdog.Start()
	if err != nil {
		log.Printf("[NovaBar] Warning: watchdog not started: %v\n", err)
		log.Printf("[NovaBar] Recovery then depends on 'shellctl --rescue' and the rescue hotkey\n")
	} else {
		d.dog = dog
	}

	// Hide the native taskbars only when configured to. The default is off precisely
	// so a fresh install cannot strand a user with no way back.
	//
	// This runs before applyBars deliberately: hiding the taskbar has to land first,
	// because TaskbarState.Hide forces the shell to recompute the work area, and a
	// bottom bar asking for space the shell still believes the taskbar owns is refused
	// with nothing granted.
	if d.cfg.General.HideTaskbar {
		if err := d.taskbars.Hide(); err != nil {
			log.Printf("[NovaBar] Warning hiding taskbars: %v\n", err)
		} else {
			log.Println("[NovaBar] Native taskbars hidden (watchdog is watching this process)")
		}
		d.hideTaskbar.Store(true)
	} else {
		// Make sure a previous crashed run did not leave them hidden.
		win.ForceRestoreTaskbar()
		d.hideTaskbar.Store(false)
	}

	d.applyBars(d.cfg)

	// Indexing the Start Menu and PATH takes a few hundred milliseconds; do it off
	// the pump thread so the first frame is not delayed.
	d.indexer = launcher.NewIndexer()
	go func() {
		if d.indexer != nil {
			d.indexer.Reindex()
		}
	}()

	d.media = media.NewController()
	if !d.media.Attached() {
		reason := d.media.Reason()
		if reason == "" {
			reason = "no Windows media session manager available"
		}
		log.Printf("[NovaBar] Media: %s; transport controls unavailable\n", reason)
	}

	// Per-application volume. The controller pins its own COM thread, so it is safe to
	// call from any RPC goroutine. Unavailable audio is not fatal: the dock simply omits
	// the volume affordances rather than showing controls that do nothing.
	d.audio = volume.New()
	defer d.audio.Close()
	d.audio.LogUnavailable()
	if d.audio.Available() {
		logAudioStartup(d.audio)
		// Stop the poller before the controller, so it cannot issue a COM call into a
		// closed apartment. Defers run last-in-first-out.
		audioStop := make(chan struct{})
		defer close(audioStop)
		go d.pollAudio(audioStop)
	}

	// Pinned applications. A failure here is not fatal and not even worth much: the
	// store opens empty when the file is missing or unreadable, so the dock still shows
	// running windows. That degradation is deliberate, not a fallback.
	if pinPath, err := pins.GetPath(); err != nil {
		log.Printf("[NovaBar] Pins: no location available, dock will show running windows only: %v\n", err)
	} else if store, err := pins.Open(pinPath); err != nil {
		log.Printf("[NovaBar] Pins: cannot open %s, dock will show running windows only: %v\n", pinPath, err)
	} else {
		d.pinStore = store
		if store.Degraded != "" {
			log.Printf("[NovaBar] Pins: %s\n", store.Degraded)
		}
		log.Printf("[NovaBar] Pins: %d pinned from %s\n", len(store.List()), pinPath)
	}

	d.engine = tiling.NewEngine(d.cfg)

	d.rpcs = rpc.NewServer()
	d.registerRPCMethods()
	d.rpcs.SetControlHandler(d.handleControl)

	// Serve widget assets from the same listener, before starting it.
	if root, err := webview.FindWidgetRoot(); err != nil {
		log.Printf("[NovaBar] No widgets directory: %v\n", err)
		log.Printf("[NovaBar] Bars will reserve their space but render nothing\n")
	} else {
		d.widgets = root
		d.rpcs.SetAssetHandler(webview.WidgetAssetPrefix, webview.WidgetHandler(root))
		log.Printf("[NovaBar] Widgets loaded from %s (%v)\n", root, webview.ListWidgets(root))
	}

	// The SDK is served too, so a widget can import it without a build step. It lives
	// outside the widget tree because it is shared by every widget.
	if sdkRoot, err := webview.FindSDKRoot(); err != nil {
		log.Printf("[NovaBar] Widget SDK not served: %v\n", err)
	} else {
		d.rpcs.SetAssetHandler("/sdk", webview.WidgetHandler(sdkRoot))
	}

	// The floating dock needs the widget root, the RPC server and the widget SDK, all of
	// which only exist by now.
	if err := d.rpcs.Start(); err != nil {
		return fmt.Errorf("could not start IPC server: %w", err)
	}

	// After Start, because the dock's page is served by the listener it navigates to. An
	// earlier placement opened the window first and the page silently failed to load,
	// leaving a dock-shaped hole on the desktop.
	d.showDock()

	// The bars already exist from the applyBars call above, but the surfaces could not
	// attach then: the widget root is only resolved after the bars, and a widget needs
	// the IPC port and session token to bootstrap itself. Attach them now that both
	// exist.
	d.syncSurfaces(d.cfg)

	// Initial reconcile: adopt existing windows, put them on workspace 1, tile.
	d.trk.Refresh()
	d.trk.LogSummary()
	d.spaces.AssignAll(d.trk.GetWindowHandles(), 1)
	log.Printf("[NovaBar] %d monitor(s) detected\n", len(d.engine.GetMonitors()))
	if d.cfg.Tiling.Enabled {
		d.retile()
	}
	d.publishWindows()

	w, err := config.NewWatcher(d.cfg)
	if err != nil {
		log.Printf("[NovaBar] Warning: config live-reload unavailable: %v\n", err)
		return nil
	}
	d.watcher = w

	// Signal that the first-run modal is due. It is deferred until the pump is running
	// so the watchdog, IPC control channel and rescue hotkey are all live before the
	// user is asked to acknowledge anything.
	d.pendingOnboarding = firstRun

	return nil
}

// applyBars reconciles the live AppBars against the configured set.
//
// Enables and disables bars from config.toml without a restart, which is the point of
// holding several [[bars]] entries: turning a second bar on is a config edit.
func (d *daemon) applyBars(cfg config.Config) {
	specs := make([]bar.Spec, 0, len(cfg.Bars))

	for _, bc := range cfg.Bars {
		if !bc.Enabled {
			continue
		}
		// The dock is no longer an AppBar. A config written before that change may still
		// carry a [[bars]] entry using the dock widget, and creating it as a bar would put
		// a vertical strip back on a screen edge next to the floating dock the user now
		// has. Skip it and say so once, rather than leaving them to wonder why there are
		// two.
		if bc.Widget == dockWidgetName {
			log.Printf("[NovaBar] Bar %q uses the %q widget, which is now the floating dock; "+
				"remove that [[bars]] entry and use [dock] in config.toml instead\n",
				bc.Name, dockWidgetName)
			continue
		}
		edge, ok := bar.EdgeForName(bc.Edge)
		if !ok {
			log.Printf("[NovaBar] Ignoring bar with unknown edge %q\n", bc.Edge)
			continue
		}
		specs = append(specs, bar.Spec{
			Edge: edge,
			Size: int32(bc.Size),
			Name: bc.Name,
		})
	}

	for _, err := range d.bars.Apply(specs) {
		log.Printf("[NovaBar] %v\n", err)
	}

	d.warnAboutCollapsedBars()

	// How far the bars reach is a setting, and it changes the rectangles, so it is
	// applied before they are recomputed.
	d.bars.SetScope(cfg.General.BarScope)

	// Recompute every bar's rectangle before attaching surfaces, so adjacent edges do
	// not both claim the corner and a left dock starts below the top bar rather than
	// underneath it.
	d.bars.RecomputeRects()

	d.syncSurfaces(cfg)

	log.Printf("[NovaBar] %d bar(s) active\n", d.bars.Count())
}

// syncSurfaces attaches or releases a WebView2 surface for each bar.
//
// A surface lives inside the bar's own window, so the bar's work-area reservation and
// window styles keep applying and WebView2 creates no windows of its own.
//
// Must run on the message pump thread: WebView2 requires a single-threaded apartment,
// and the controller is bound to the thread that created it.
func (d *daemon) syncSurfaces(cfg config.Config) {
	if d.widgets == "" {
		return
	}
	if !webview.Available() {
		log.Printf("[NovaBar] WebView2 runtime unavailable; bars will render nothing\n")
		return
	}

	live := make(map[string]bool, len(cfg.Bars))

	for _, bc := range cfg.Bars {
		if !bc.Enabled {
			continue
		}

		edge, ok := bar.EdgeForName(bc.Edge)
		if !ok {
			continue
		}
		ab := d.bars.Get(edge)
		if ab == nil || !ab.Registered() {
			continue
		}

		name := bc.Name
		if name == "" {
			name = EdgeNameFor(edge)
		}
		live[name] = true

		existing, has := d.surfaces[name]
		if has {
			// Same widget on the same bar: refit the surface to the bar's current
			// rectangle and tell WebView2 the parent moved, which is what a bar being
			// resized or repositioned requires.
			d.fitSurface(existing, ab)
			continue
		}

		surface, err := d.attachSurface(ab, bc, name)
		if err != nil {
			log.Printf("[NovaBar] Could not attach a widget to the %s bar: %v\n", name, err)
			continue
		}
		d.surfaces[name] = surface
	}

	// Release surfaces whose bar is gone, revoking each credential with it.
	for name, surface := range d.surfaces {
		if live[name] {
			continue
		}
		surface.Destroy()
		delete(d.surfaces, name)
		if secret, ok := d.widgetSecrets[name]; ok {
			d.rpcs.RevokeWidget(secret)
			delete(d.widgetSecrets, name)
		}
	}
}

// randomSecret returns a 256-bit hex credential for a surface.
func randomSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("could not generate a widget credential: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// fitSurface refits a surface to its bar's current rectangle.
//
// The surface must track the bar's real extent, not its thickness: a top bar is wide
// and short, a left dock is narrow and full height. Sizing both to thickness squared
// renders a left dock as a small square sitting in the top-left corner, on top of the
// top bar, which reads as "the dock is missing" rather than as a sizing mistake.
func (d *daemon) fitSurface(surface *webview.Surface, ab *bar.AppBarManager) {
	if surface == nil {
		return
	}
	bounds := ab.Bounds()
	if bounds.Width() <= 0 || bounds.Height() <= 0 {
		return
	}
	surface.Resize(0, 0, bounds.Width(), bounds.Height())
	surface.NotifyParentMoved()
}

// attachSurface creates a WebView2 surface inside a bar's window and loads its widget.
func (d *daemon) attachSurface(ab *bar.AppBarManager, bc config.BarConfig, name string) (*webview.Surface, error) {
	widget := bc.Widget
	if widget == "" {
		widget = "bar"
	}

	declared, err := d.readManifestPermissions(widget)
	if err != nil {
		log.Printf("[NovaBar] Widget %q has no readable manifest: %v\n", widget, err)
	}

	// A per-surface credential, separate from the session token, is what carries this
	// widget's permissions. Generated here rather than derived from the widget name so a
	// second surface for the same widget still gets its own revocable secret.
	widgetSecret, err := randomSecret()
	if err != nil {
		return nil, err
	}
	if err := d.rpcs.RegisterWidget(widgetSecret, name, declared); err != nil {
		// A bad permission in a manifest must not take the bar down; deny the widget
		// everything and say so.
		log.Printf("[NovaBar] Widget %q has an invalid manifest: %v\n", widget, err)
		if regErr := d.rpcs.RegisterWidget(widgetSecret, name, nil); regErr != nil {
			return nil, regErr
		}
	}
	d.widgetSecrets[name] = widgetSecret

	boot := webview.Bootstrap{
		Port:        d.rpcs.Port(),
		Token:       d.rpcs.Token(),
		WidgetToken: widgetSecret,
		Widget:      widget,
		Permissions: declared,
	}
	js, err := boot.BootstrapJS()
	if err != nil {
		return nil, err
	}

	surface, err := webview.Attach(ab.HWND(), webview.Options{
		Transparent: bc.Transparent,
		OnMessage: func(payload string) {
			d.handleWidgetMessage(name, payload)
		},
	})
	if err != nil {
		return nil, err
	}

	// Inject before navigating so the endpoint exists by the time the widget's first
	// script runs.
	surface.Init(js)

	url := webview.WidgetURL(d.rpcs.Port(), widget)
	surface.Navigate(url)

	// Fit the webview to the bar's real extent, not its thickness.
	d.fitSurface(surface, ab)

	log.Printf("[NovaBar] %s bar rendering widget %q from %s\n", name, widget, url)
	return surface, nil
}

// readManifestPermissions reads a widget manifest's declared permissions.
//
// Informational only for now. The manifest is what a widget claims; enforcement belongs
// on the RPC methods, which is not wired yet, so nothing is trusted from the file.
func (d *daemon) readManifestPermissions(widget string) ([]string, error) {
	path := filepath.Join(d.widgets, widget, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var manifest struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return manifest.Permissions, nil
}

// handleWidgetMessage handles a message posted from a widget page.
func (d *daemon) handleWidgetMessage(barName, payload string) {
	// The dock reports the width of its own panel, so the overlay can be sized to it
	// instead of to the screen. A full-width overlay covers the whole strip along the
	// bottom of the display, and a window that covers the screen takes the clicks:
	// nothing underneath it could be pressed.
	if barName == dockWidgetName {
		var msg struct {
			Type  string `json:"type"`
			Width int32  `json:"width"`
		}
		if err := json.Unmarshal([]byte(payload), &msg); err == nil && msg.Type == "dock-width" {
			d.resizeDock(msg.Width)
			return
		}
	}

	log.Printf("[NovaBar] Message from widget on the %s bar: %s", barName, payload)
}

// EdgeNameFor returns a readable name for an ABE_* edge constant.
func EdgeNameFor(edge uint32) string { return bar.EdgeName(edge) }

// warnAboutCollapsedBars reports bars that registered but reserved no screen space.
//
// The common cause is a bottom or side bar while the native taskbar is still visible.
// The shell gives the real taskbar priority, ABM_QUERYPOS clips the proposal to
// whatever is left, and the bar ends up registered, invisible, and painted over. It
// looks exactly like a bug in the bar, so say so plainly.
func (d *daemon) warnAboutCollapsedBars() {
	collapsed := d.bars.Collapsed()
	if len(collapsed) == 0 {
		return
	}

	for _, info := range collapsed {
		switch {
		case info.Edge == "bottom" && !d.cfg.General.HideTaskbar:
			log.Printf("[NovaBar] WARNING: the %q bar on the bottom edge is invisible because the native taskbar "+
				"still occupies that edge. Set [general] hide_taskbar = true, or disable the bottom bar.\n",
				info.Name)
		default:
			log.Printf("[NovaBar] WARNING: the %q bar on the %s edge reserved no space (%dpx requested). "+
				"Another window may already own that edge.\n", info.Name, info.Edge, info.Size)
		}
	}
}

// teardown restores the desktop. Runs on every exit path, including rescue.
func (d *daemon) teardown() {
	log.Println("[NovaBar] Restoring the desktop")

	// The guard stands down first.
	//
	// It hides the taskbar every half second, and the teardown shows it again.
	// If the guard is still running, one of its ticks can land after the taskbar
	// has been restored, and the last word belongs to the hide: the taskbar ends
	// the shutdown hidden and parked off-screen. That is how a rescue ends with
	// no taskbar and no way to get one back.
	d.quitting.Store(true)
	d.hideTaskbar.Store(false)

	// Wait for a hide in flight to finish, so the restore below is the last word.
	d.guardMu.Lock()
	d.guardMu.Unlock()

	if d.hotkeys != nil {
		d.hotkeys.Stop()
	}
	if d.launcher != nil {
		d.launcher.Close()
		d.launcher = nil
	}
	if d.launcherSecret != "" && d.rpcs != nil {
		d.rpcs.RevokeWidget(d.launcherSecret)
		d.launcherSecret = ""
	}
	for name, surface := range d.surfaces {
		surface.Destroy()
		delete(d.surfaces, name)
	}
	for name, secret := range d.widgetSecrets {
		if d.rpcs != nil {
			d.rpcs.RevokeWidget(secret)
		}
		delete(d.widgetSecrets, name)
	}
	if d.watcher != nil {
		d.watcher.Close()
	}
	if d.bars != nil {
		d.bars.UnregisterAll()
	}
	if err := d.taskbars.Show(); err != nil {
		log.Printf("[NovaBar] Warning restoring taskbars: %v\n", err)
	}
	win.ForceRestoreTaskbar()

	// Only after the desktop is safe does the watchdog become unnecessary.
	if d.dog != nil {
		d.dog.Stop()
	}

	if d.rpcs != nil {
		d.rpcs.Stop()
	}

	log.Println("[NovaBar] Shutdown complete")
}

// registerHotkeys binds the configured global hotkeys on the pump thread.
func (d *daemon) registerHotkeys() {
	d.hotkeys = hotkey.NewManager()
	d.hotkeys.Register(d.cfg)
}

// installSignalHandler turns Ctrl+C and SIGTERM into a message-pump quit.
func (d *daemon) installSignalHandler() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		log.Printf("[NovaBar] Received %v; shutting down", sig)
		d.requestQuit()
	}()
}

// requestQuit asks the message pump to exit.
//
// PostQuitMessage is thread-affine: it sets a flag on the calling thread's queue, so
// calling it from the signal goroutine would quit the wrong thread. Post the request
// to the pump thread instead and let the pump call PostQuitMessage itself.
func (d *daemon) requestQuit() {
	d.quitOnce.Do(func() {
		if err := win.PostThreadMessage(d.pumpThread, msgQuit, 0, 0); err != nil {
			// The pump is gone or not yet listening; quit this thread so the process
			// still exits rather than hanging.
			log.Printf("[NovaBar] Could not post quit to the pump (%v); quitting directly\n", err)
			win.PostQuitMessage(0)
		}
	})
}

// pumpThread is the OS thread id of the message pump, used to target PostThreadMessage.
var pumpThread uint32

// msgQuit is a private window message used to exit the loop cleanly.
const msgQuit = 0x7F42 // WM_APP range

// msgPumpTask is a private window message used to wake the pump so it can run work
// that has to happen on the pump thread.
const msgPumpTask = 0x7F43 // WM_APP range

// pumpTaskTimeout bounds how long a hand-off waits for the pump to pick the work up.
//
// It is generous, because a slow hand-off is better than a dropped one: the work
// being handed over is usually a bar resize, and dropping it would leave the shell
// showing a bar of the wrong size with no error anywhere.
const pumpTaskTimeout = 5 * time.Second

// pump runs the Win32 message loop.
// onPumpThread runs fn on the message pump thread and waits for it to finish.
//
// WebView2 is bound to the thread that created it. Resizing a bar's surface from
// any other thread is not a no-op: the controller throws, and the exception takes
// the daemon with it, which is a crash the user sees as the shell disappearing and
// the taskbar coming back.
//
// The wait matters as much as the hand-off. The caller wants to know the change has
// been applied before it carries on, and returning early would let two changes
// overlap and leave the bars in a state neither of them intended.
func (d *daemon) onPumpThread(fn func()) {
	// Already on the pump thread: run the work here.
	//
	// This is not an optimisation. WebView2 delivers a page's messages on the
	// thread that created the controller, which is the pump thread, so a widget
	// that asks for something to happen on the pump thread is already on it.
	// Queuing the work and waiting for it would be waiting for this thread to
	// pick up a job this thread is supposed to be running: the pump would block
	// on itself and no message would be handled again.
	if windows.GetCurrentThreadId() == d.pumpThread {
		fn()
		return
	}

	if d.pumpTasks == nil {
		fn()
		return
	}

	done := make(chan struct{})
	select {
	case d.pumpTasks <- func() {
		defer close(done)
		fn()
	}:
	case <-time.After(pumpTaskTimeout):
		log.Println("[NovaBar] The message pump did not take the work; skipping it")
		return
	}

	// The pump has to be woken up, or the task sits in the channel until the next
	// message arrives.
	_ = win.PostThreadMessage(d.pumpThread, msgPumpTask, 0, 0)

	select {
	case <-done:
	case <-time.After(pumpTaskTimeout):
		log.Println("[NovaBar] The message pump did not finish the work in time")
	}
}

// runPumpTasks runs the work queued for the pump thread.
func (d *daemon) runPumpTasks() {
	for {
		select {
		case task := <-d.pumpTasks:
			task()
		default:
			return
		}
	}
}

// pump runs the message loop.
func (d *daemon) pump() {
	var msg win.MSG
	for {
		res, err := win.GetMessage(&msg, 0, 0, 0)
		if res == -1 {
			log.Printf("[NovaBar] Message loop error: %v\n", err)
			break
		}
		if res == 0 {
			break // WM_QUIT
		}

		switch msg.Message {
		case msgQuit:
			log.Println("[NovaBar] Quit requested")
			win.PostQuitMessage(0)
			return
		case msgPumpTask:
			d.runPumpTasks()
		case win.WM_HOTKEY:
			d.handleHotkey(msg.WParam)
		}

		win.TranslateMessage(&msg)
		win.DispatchMessage(&msg)
	}
}

// handleHotkey dispatches a WM_HOTKEY.
func (d *daemon) handleHotkey(id uintptr) {
	binding, ok := d.hotkeys.Resolve(id)
	if !ok {
		return
	}
	log.Printf("[NovaBar] Hotkey: %s\n", binding)

	switch binding.Action {
	case hotkey.ActionRescue:
		log.Println("[NovaBar] Rescue hotkey pressed; restoring the desktop")
		d.performRescue()
	case hotkey.ActionToggleLauncher:
		d.toggleLauncher()
	case hotkey.ActionToggleSettings:
		d.toggleSettings()
	case hotkey.ActionWorkspacePrev:
		d.spaces.Step(-1)
		d.afterWorkspaceChange()
	case hotkey.ActionWorkspaceNext:
		d.spaces.Step(1)
		d.afterWorkspaceChange()
	case hotkey.ActionFocus:
		d.focusInDirection(binding.Direction)
	case hotkey.ActionMove:
		d.moveInDirection(binding.Direction)
	case hotkey.ActionResize:
		d.openZonePicker()
	case hotkey.ActionWorkspace:
		d.spaces.Switch(binding.Workspace)
		d.afterWorkspaceChange()
	case hotkey.ActionMoveToWorkspace:
		d.moveFocusedToWorkspace(binding.Workspace)
	}
}

// zonePicker is a zone grid waiting for the user to pick a cell.
type zonePicker struct {
	focused windows.HWND
	cells   int
}

// openZonePicker opens the zone picker for the focused window.
//
// The picker is a grid over the work area: the user picks a cell, the focused window
// takes it, and the other tiled windows fill what is left.
func (d *daemon) openZonePicker() {
	if d.zoneOverlay != nil {
		d.closeZonePicker()
		return
	}

	focused, ok := d.focusedWindow()
	if !ok {
		return
	}

	subjects := d.tilingSubjects()
	if len(subjects) < 2 {
		// A single window has nothing to be arranged around, so there is nothing
		// to pick.
		return
	}

	// The window being moved goes first, so the cell the user picks belongs to it.
	subjects = moveToFront(subjects, focused)
	if subjects[0].HWnd != focused {
		return
	}

	workArea, ok := d.workAreaFor(focused)
	if !ok {
		return
	}

	cells := tiling.ZoneCells(len(subjects))

	secret, err := randomSecret()
	if err != nil {
		log.Printf("[NovaBar] Could not create a zone picker credential: %v\n", err)
		return
	}
	if err := d.rpcs.RegisterWidget(secret, "zones", d.widgetPermissions("zones")); err != nil {
		log.Printf("[NovaBar] Zone picker credential rejected: %v\n", err)
		return
	}

	boot := webview.Bootstrap{
		Port:        d.rpcs.Port(),
		Token:       d.rpcs.Token(),
		WidgetToken: secret,
		Widget:      "zones",
		Permissions: d.widgetPermissions("zones"),
		Data: map[string]any{
			"cells": cells,
			"held":  0,
			"title": d.windowTitle(focused),
		},
	}
	js, err := boot.BootstrapJS()
	if err != nil {
		d.rpcs.RevokeWidget(secret)
		log.Printf("[NovaBar] Could not build the zone picker bootstrap: %v\n", err)
		return
	}

	ov, err := overlay.Show(overlay.Options{
		Widget:      "zones",
		Rect:        &workArea,
		Transparent: true,
		Persistent:  true,
		OnMessage: func(payload string) {
			d.handleZoneMessage(payload)
		},
	})
	if err != nil {
		d.rpcs.RevokeWidget(secret)
		log.Printf("[NovaBar] Could not show the zone picker: %v\n", err)
		return
	}

	ov.SetBootstrap(js)
	ov.Navigate(webview.WidgetURL(d.rpcs.Port(), "zones"))

	d.zoneOverlay = ov
	d.zoneSecret = secret
	d.zones = &zonePicker{focused: focused, cells: cells}

	log.Printf("[NovaBar] Zone picker: %d cells over %dx%d\n", cells, workArea.Width(), workArea.Height())
}

// windowTitle returns the title of a window, for the picker to show.
func (d *daemon) windowTitle(hwnd windows.HWND) string {
	wi, ok := d.trk.Get(hwnd)
	if !ok {
		return ""
	}
	return wi.Title
}

// handleZoneMessage acts on what the picker reports.
func (d *daemon) handleZoneMessage(payload string) {
	var msg struct {
		Type string `json:"type"`
		Cell int    `json:"cell"`
	}
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		return
	}

	switch msg.Type {
	case "zone":
		d.applyZone(msg.Cell)
	case "zone-cancel":
		d.closeZonePicker()
	}
}

// applyZone gives the focused window the chosen zone and lays the rest out around it.
func (d *daemon) applyZone(cell int) {
	if d.zones == nil {
		return
	}

	subjects := d.tilingSubjects()
	subjects = moveToFront(subjects, d.zones.focused)
	if len(subjects) == 0 || subjects[0].HWnd != d.zones.focused {
		d.closeZonePicker()
		return
	}

	d.engine.ApplyZone(subjects, cell)
	d.closeZonePicker()
}

// closeZonePicker dismisses the picker.
func (d *daemon) closeZonePicker() {
	if d.zoneOverlay != nil {
		d.zoneOverlay.Close()
		d.zoneOverlay = nil
	}
	if d.zoneSecret != "" && d.rpcs != nil {
		d.rpcs.RevokeWidget(d.zoneSecret)
	}
	d.zoneSecret = ""
	d.zones = nil
}

// workAreaFor returns the work area of the display a window is on.
func (d *daemon) workAreaFor(hwnd windows.HWND) (win.RECT, bool) {
	monitors := d.engine.GetMonitors()
	if len(monitors) == 0 {
		return win.RECT{}, false
	}

	hMon := win.MonitorFromWindow(hwnd, win.MONITOR_DEFAULTTONEAREST)
	for _, m := range monitors {
		if m.HMonitor == hMon {
			return win.RECT{
				Left:   int32(m.WorkArea.X),
				Top:    int32(m.WorkArea.Y),
				Right:  int32(m.WorkArea.X + m.WorkArea.Width),
				Bottom: int32(m.WorkArea.Y + m.WorkArea.Height),
			}, true
		}
	}
	return win.RECT{}, false
}

// moveToFront puts a window at the head of a list, keeping the order of the rest.
func moveToFront(subjects []tiling.WindowSubject, hwnd windows.HWND) []tiling.WindowSubject {
	for i, s := range subjects {
		if s.HWnd != hwnd {
			continue
		}
		out := make([]tiling.WindowSubject, 0, len(subjects))
		out = append(out, s)
		out = append(out, subjects[:i]...)
		out = append(out, subjects[i+1:]...)
		return out
	}
	return subjects
}

// zoneCellCount returns the number of cells in the zone grid for a set of windows.
//
// The grid holds the windows, so a picker full of empty cells would be a picker
// full of choices that lead nowhere.
func zoneCellCount(windows int) int {
	return tiling.ZoneCells(windows)
}

func (d *daemon) focusInDirection(dir string) {
	from, ok := d.focusedWindow()
	if !ok {
		log.Printf("[NovaBar] Nothing is focused, so there is nothing to focus %s\n", dir)
		return
	}

	target, ok := d.engine.FocusInDirection(d.tilingSubjects(), from, tiling.Direction(dir))
	if !ok {
		log.Printf("[NovaBar] No window %s of the focused one\n", dir)
		return
	}
	d.focusWindow(target)
	log.Printf("[NovaBar] Focused the window %s\n", dir)
}

// moveInDirection moves the focused window into its neighbour's place.
func (d *daemon) moveInDirection(dir string) {
	focused, ok := d.focusedWindow()
	if !ok {
		return
	}
	d.engine.MoveInDirection(d.tilingSubjects(), focused, tiling.Direction(dir))
}

// moveFocusedToWorkspace moves the focused window to another workspace.
func (d *daemon) moveFocusedToWorkspace(id int) {
	hwnd, ok := d.focusedWindow()
	if !ok {
		return
	}
	d.spaces.Move(hwnd, id)
	d.afterWorkspaceChange()
}

// focusedWindow returns the window the shell should act on.
//
// The foreground window is the one the user was last using, which is what "the
// focused window" means to someone pressing a key.
//
// The shell's own surfaces are not candidates. Pressing a window key while the
// launcher is open should not move the launcher.
func (d *daemon) focusedWindow() (windows.HWND, bool) {
	hwnd := win.GetForegroundWindow()
	if hwnd == 0 {
		return 0, false
	}
	if !d.trk.IsManageable(hwnd) {
		return 0, false
	}
	if !d.spaces.Has(hwnd) {
		return 0, false
	}
	return hwnd, true
}

// focusWindow brings a window to the front.
//
// Windows refuses to change the foreground from a background process, and a hotkey
// handler is one. The shell's own thread received the key, so it is usually allowed
// to; the retry is for the case where it is not.
func (d *daemon) focusWindow(hwnd windows.HWND) {
	win.ShowWindow(hwnd, win.SW_SHOW)
	if !win.SetForegroundWindow(hwnd) {
		win.ShowWindow(hwnd, win.SW_SHOW)
	}
}

// tilingSubjects returns the windows the tiling engine should lay out.
func (d *daemon) tilingSubjects() []tiling.WindowSubject {
	visible := d.spaces.VisibleWindows()
	if len(visible) == 0 {
		return nil
	}

	subjects := make([]tiling.WindowSubject, 0, len(visible))
	for _, hwnd := range visible {
		wi, ok := d.trk.Get(hwnd)
		if !ok {
			continue
		}
		subjects = append(subjects, tiling.WindowSubject{
			HWnd:     hwnd,
			ProcName: wi.ProcName,
			Title:    wi.Title,
			Class:    wi.ClassName,
		})
	}
	return subjects
}

// toggleLauncher shows the launcher overlay, or dismisses it if already showing.
//
// Runs on the pump thread, since creating the overlay's window and its WebView2
// controller both belong there.
func (d *daemon) toggleLauncher() {
	launcherPerms := d.widgetPermissions("launcher")
	if d.launcher != nil {
		d.launcher.Close()
		d.launcher = nil
		return
	}
	if d.widgets == "" {
		log.Println("[NovaBar] No widgets directory; cannot show the launcher")
		return
	}

	secret, err := randomSecret()
	if err != nil {
		log.Printf("[NovaBar] Could not create a launcher credential: %v\n", err)
		return
	}
	if err := d.rpcs.RegisterWidget(secret, "launcher", d.widgetPermissions("launcher")); err != nil {
		log.Printf("[NovaBar] Launcher credential rejected: %v\n", err)
		return
	}

	boot := webview.Bootstrap{
		Port:        d.rpcs.Port(),
		Token:       d.rpcs.Token(),
		WidgetToken: secret,
		Widget:      "launcher",
		Permissions: launcherPerms,
	}
	js, err := boot.BootstrapJS()
	if err != nil {
		log.Printf("[NovaBar] Could not build the launcher bootstrap: %v\n", err)
		return
	}

	ov, err := overlay.Show(overlay.Options{
		Widget:  "launcher",
		Width:   660,
		Height:  440,
		AnchorX: 0.5,
		AnchorY: 0.35,
		OnMessage: func(payload string) {
			d.handleWidgetMessage("launcher", payload)
		},
	})
	if err != nil {
		d.rpcs.RevokeWidget(secret)
		log.Printf("[NovaBar] Could not show the launcher: %v\n", err)
		return
	}

	d.launcher = ov
	d.launcherSecret = secret

	ov.SetBootstrap(js)
	ov.Navigate(webview.WidgetURL(d.rpcs.Port(), "launcher"))
}

// toggleSettings opens or closes the settings overlay.
//
// Same shape as the launcher: a transient topmost window that reserves no work area, so
// opening it does not reflow the desktop.
func (d *daemon) toggleSettings() {
	if d.settings != nil {
		d.settings.Close()
		d.settings = nil
		if d.settingsSecret != "" && d.rpcs != nil {
			d.rpcs.RevokeWidget(d.settingsSecret)
		}
		d.settingsSecret = ""
		return
	}

	if d.widgets == "" {
		log.Println("[NovaBar] No widgets directory; cannot show settings")
		return
	}

	// The manifest is the single source for permissions. The hard-coded list that used to
	// live in toggleLauncher drifted out of step with the launcher widget's own manifest
	// and silently broke two features; deriving it removes that failure mode.
	perms := d.widgetPermissions("settings")
	if len(perms) == 0 {
		log.Println("[NovaBar] Settings declares no usable permissions; refusing to open it")
		return
	}

	secret, err := randomSecret()
	if err != nil {
		log.Printf("[NovaBar] Could not create a settings credential: %v\n", err)
		return
	}
	if err := d.rpcs.RegisterWidget(secret, "settings", perms); err != nil {
		log.Printf("[NovaBar] Settings credential rejected: %v\n", err)
		return
	}

	boot := webview.Bootstrap{
		Port:        d.rpcs.Port(),
		Token:       d.rpcs.Token(),
		WidgetToken: secret,
		Widget:      "settings",
		Permissions: perms,
	}
	js, err := boot.BootstrapJS()
	if err != nil {
		d.rpcs.RevokeWidget(secret)
		log.Printf("[NovaBar] Could not build the settings bootstrap: %v\n", err)
		return
	}

	ov, err := overlay.Show(overlay.Options{
		Widget:  "settings",
		Width:   560,
		Height:  620,
		AnchorX: 0.5,
		AnchorY: 0.5,
		OnMessage: func(payload string) {
			d.handleWidgetMessage("settings", payload)
		},
	})
	if err != nil {
		d.rpcs.RevokeWidget(secret)
		log.Printf("[NovaBar] Could not show settings: %v\n", err)
		return
	}

	d.settings = ov
	d.settingsSecret = secret

	ov.SetBootstrap(js)
	ov.Navigate(webview.WidgetURL(d.rpcs.Port(), "settings"))
}

// performRescue restores the desktop and exits.
//
// Only requests the quit. Cleanup happens in Run's post-pump teardown, which is the
// single place it runs, on the pump thread. Doing it here as well meant teardown
// executed twice and raced itself across two goroutines.
//
// Uses requestQuit rather than PostQuitMessage directly: this can be reached from the
// control goroutine, and PostQuitMessage posts WM_QUIT to the *calling* thread's queue,
// so calling it off the pump thread leaves the loop spinning forever after cleanup.
func (d *daemon) performRescue() {
	log.Println("[NovaBar] Rescue requested; restoring the desktop")
	d.requestQuit()
}

// handleControl serves shellctl commands.
func (d *daemon) handleControl(command string) (any, error) {
	switch command {
	case rpc.CmdRescue, rpc.CmdShutdown:
		log.Printf("[NovaBar] Control: %s requested", command)
		go func() {
			// Give the HTTP response time to flush before tearing the server down.
			time.Sleep(150 * time.Millisecond)
			d.performRescue()
		}()
		return map[string]any{"action": command}, nil

	case rpc.CmdStatus:
		return d.status(), nil
	}
	return nil, fmt.Errorf("unknown command %q", command)
}

// status builds the status document returned by `shellctl status`.
func (d *daemon) status() map[string]any {
	monitors := d.engine.GetMonitors()
	monitorInfo := make([]map[string]any, 0, len(monitors))
	for i, m := range monitors {
		monitorInfo = append(monitorInfo, map[string]any{
			"index":     i,
			"bounds":    m.Bounds,
			"work_area": m.WorkArea,
			"primary":   m.Primary,
		})
	}

	appbarState := map[string]any{"count": d.bars.Count(), "bars": d.bars.Describe()}

	return map[string]any{
		"pid":            os.Getpid(),
		"config_path":    d.configPath(),
		"taskbar_hidden": d.taskbars.IsHidden(),
		"appbar":         appbarState,
		"workspace": map[string]any{
			"active":     d.spaces.ActiveID(),
			"workspaces": d.spaces.Describe(),
		},
		"windows_tracked": d.trk.Count(),
		"monitors":        monitorInfo,
		"launcher_items":  d.indexer.Count(),
		"media":           d.media.GetCurrentSession(),
		"audio":           d.audioStatus(),
		"hotkeys":         d.hotkeys.Registered(),
		"watchdog_pid":    d.dog.PID(),
		"rpc_port":        d.rpcs.Port(),
		"tiling": map[string]any{
			"enabled": d.cfg.Tiling.Enabled,
			"layout":  d.cfg.Tiling.DefaultLayout,
		},
	}
}

func (d *daemon) configPath() string {
	p, err := config.GetConfigPath()
	if err != nil {
		return ""
	}
	return p
}

// registerRPCMethods exposes the shell's capabilities to widgets and the CLI.
func (d *daemon) registerRPCMethods() {
	r := d.rpcs

	r.RegisterMethod(rpc.MethodWorkspaceSwitch, func(params json.RawMessage) (interface{}, error) {
		var p struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if p.ID < 1 || p.ID > workspace.Count {
			return nil, fmt.Errorf("workspace id must be 1..%d", workspace.Count)
		}
		d.spaces.Switch(p.ID)
		d.afterWorkspaceChange()
		return map[string]interface{}{"active": d.spaces.ActiveID()}, nil
	})

	r.RegisterMethod(rpc.MethodWorkspaceGet, func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"active":     d.spaces.ActiveID(),
			"workspaces": d.spaces.Describe(),
		}, nil
	})

	r.RegisterMethod(rpc.MethodLauncherSearch, func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return d.indexer.Search(p.Query, p.Limit), nil
	})

	r.RegisterMethod(rpc.MethodLauncherLaunch, func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Target string `json:"target"`
			Path   string `json:"path"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		// Accept either spelling so existing widget code keeps working.
		target := p.Target
		if target == "" {
			target = p.Path
		}
		if err := launcher.Launch(target); err != nil {
			return nil, err
		}
		return map[string]interface{}{"success": true, "target": target}, nil
	})

	registerPinMethods(r, d.pinStore)
	registerVolumeMethods(r, d.audio)
	registerConfigMethods(r, d)

	r.RegisterMethod(rpc.MethodMediaGet, func(json.RawMessage) (interface{}, error) {
		return d.media.GetCurrentSession(), nil
	})

	r.RegisterMethod(rpc.MethodMediaCommand, func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		cmd := media.TransportCommand(strings.ToLower(strings.TrimSpace(p.Command)))
		switch cmd {
		case media.CommandPlay, media.CommandPause, media.CommandStop,
			media.CommandNext, media.CommandPrevious, media.CommandTogglePlay:
		default:
			return nil, fmt.Errorf("unknown media command %q", p.Command)
		}
		if err := d.media.Command(cmd); err != nil {
			return nil, err
		}
		r.Broadcast(rpc.TopicMediaState, d.media.GetCurrentSession())
		return map[string]interface{}{"ok": true}, nil
	})

	r.RegisterMethod(rpc.MethodWindowsGet, func(json.RawMessage) (interface{}, error) {
		return d.trk.Snapshot(), nil
	})

	r.RegisterMethod(rpc.MethodWindowFocus, func(params json.RawMessage) (interface{}, error) {
		hwnd, err := parseHWND(params)
		if err != nil {
			return nil, err
		}
		if !win.IsValid(hwnd) {
			return nil, fmt.Errorf("window handle %d is not valid", hwnd)
		}
		win.ShowWindow(hwnd, win.SW_RESTORE)
		if !win.SetForegroundWindow(hwnd) {
			// Windows denies foreground changes from background processes. Retrying via
			// the input queue is the standard workaround.
			win.ShowWindow(hwnd, win.SW_SHOW)
		}
		return map[string]interface{}{"focused": true}, nil
	})

	r.RegisterMethod(rpc.MethodWindowMove, func(params json.RawMessage) (interface{}, error) {
		var p struct {
			HWND   uintptr `json:"hwnd"`
			X      int     `json:"x"`
			Y      int     `json:"y"`
			Width  int     `json:"width"`
			Height int     `json:"height"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if !win.IsValid(windows.HWND(p.HWND)) {
			return nil, fmt.Errorf("window handle %d is not valid", p.HWND)
		}
		if p.Width <= 0 || p.Height <= 0 {
			return nil, fmt.Errorf("width and height must be positive")
		}
		err := win.SetWindowPos(windows.HWND(p.HWND), win.HWND_TOP,
			int32(p.X), int32(p.Y), int32(p.Width), int32(p.Height),
			win.SWP_NOACTIVATE)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"moved": true}, nil
	})

	r.RegisterMethod(rpc.MethodWindowClose, func(params json.RawMessage) (interface{}, error) {
		hwnd, err := parseHWND(params)
		if err != nil {
			return nil, err
		}
		if !win.IsValid(hwnd) {
			return nil, fmt.Errorf("window handle %d is not valid", hwnd)
		}
		if err := win.PostMessage(hwnd, win.WM_CLOSE, 0, 0); err != nil {
			return nil, err
		}
		return map[string]interface{}{"closed": true}, nil
	})

	r.RegisterMethod(rpc.MethodConfigGet, func(json.RawMessage) (interface{}, error) {
		// No secrets live in the config, so returning it wholesale is safe; it is what
		// a widget needs to render bars, gaps and enabled features.
		return d.cfg, nil
	})
}

// parseHWND extracts a window handle from a params object accepting "hwnd" as either a
// number or a hex string.
func parseHWND(params json.RawMessage) (windows.HWND, error) {
	if len(params) == 0 {
		return 0, fmt.Errorf("missing params")
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(params, &raw); err != nil {
		return 0, err
	}
	val, ok := raw["hwnd"]
	if !ok {
		return 0, fmt.Errorf("missing required parameter \"hwnd\"")
	}

	var num uint64
	if err := json.Unmarshal(val, &num); err == nil {
		return windows.HWND(uintptr(num)), nil
	}

	var str string
	if err := json.Unmarshal(val, &str); err == nil {
		var parsed uint64
		if _, err := fmt.Sscanf(str, "0x%x", &parsed); err == nil {
			return windows.HWND(uintptr(parsed)), nil
		}
	}
	return 0, fmt.Errorf("\"hwnd\" must be a number or a 0x-prefixed hex string")
}

// eventPump consumes tracker events off the WinEvent hook and reconciles state.
//
// The hook callback itself only enqueues. Everything expensive happens here, on a
// normal goroutine, with a debounce so a burst of events produces one retile.
func (d *daemon) eventPump() {
	events := d.trk.Events()

	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	var timerC <-chan time.Time

	for {
		select {
		case ev := <-events:
			switch ev.Kind {
			case tracker.EventForeground:
				// Focus changes do not change the window set; only publish.
				d.publishWindows()
			default:
				if !timer.Stop() && timerC != nil {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(eventDebounce)
				timerC = timer.C
			}

		case <-timerC:
			timerC = nil
			d.reconcile()
		}
	}
}

// reconcile re-enumerates, re-homes windows, retiles and publishes.
func (d *daemon) reconcile() {
	d.trk.Refresh()

	// Adopt any newly seen window onto the active workspace, and drop handles whose
	// window is gone.
	active := d.spaces.ActiveID()
	for _, wi := range d.trk.Snapshot() {
		if !d.spaces.Has(wi.HWnd) {
			d.spaces.Assign(wi.HWnd, active)
		}
	}
	d.spaces.Prune()

	d.retile()
	d.publishWindows()
}

// retile lays out the active workspace's windows.
func (d *daemon) retile() {
	if !d.cfg.Tiling.Enabled {
		return
	}

	// Guard re-entrancy: moving windows raises the events that drive this function.
	d.tilingMu.Lock()
	if d.tilingNow {
		d.tilingMu.Unlock()
		return
	}
	d.tilingNow = true
	d.tilingMu.Unlock()

	defer func() {
		d.tilingMu.Lock()
		d.tilingNow = false
		d.tilingMu.Unlock()
	}()

	visible := d.spaces.VisibleWindows()
	if len(visible) == 0 {
		return
	}

	d.engine.TileSubjects(d.tilingSubjects())
}

// afterWorkspaceChange retiles and announces the new workspace.
func (d *daemon) afterWorkspaceChange() {
	go func() {
		d.retile()
		d.rpcs.Broadcast(rpc.TopicWorkspaceChange, map[string]any{
			"active":     d.spaces.ActiveID(),
			"workspaces": d.spaces.Describe(),
		})
		d.publishWindows()
	}()
}

// publishWindows pushes the tracked window set to subscribers.
func (d *daemon) publishWindows() {
	d.rpcs.Broadcast(rpc.TopicWindowsUpdated, d.trk.Snapshot())
}

// statsPump publishes periodic system stats.
func (d *daemon) statsPump() {
	ticker := time.NewTicker(statsPollInterval)
	defer ticker.Stop()

	for range ticker.C {
		d.rpcs.Broadcast(rpc.TopicSystemStats, map[string]any{
			"windows_tracked": d.trk.Count(),
			"workspace":       d.spaces.ActiveID(),
			"monitors":        len(d.engine.GetMonitors()),
			"goroutines":      runtime.NumGoroutine(),
		})
	}
}

// barGuardInterval is how often the bars check that the shell still has their space.
//
// Two seconds is frequent enough that a window is never left sitting under a bar for
// long enough to be noticed, and rare enough to cost nothing.
const barGuardInterval = 2 * time.Second

// barGuard makes sure the shell still believes the bars own their space.
//
// The work area can be rewritten from outside the shell's AppBar bookkeeping, and the
// usual way that happens is NovaBar's own recovery path writing it to clear a
// reservation left behind by a dead process. The bar keeps drawing itself either
// way, but the desktop behind it is laid out as if the bar were not there, and a
// maximized window ends up under the bar with its title bar hidden.
//
// The check runs on the message pump thread, because claiming a reservation is a
// message to the shell and the shell answers on the thread that registered.
func (d *daemon) barGuard() {
	ticker := time.NewTicker(barGuardInterval)
	defer ticker.Stop()

	for range ticker.C {
		d.onPumpThread(func() { d.bars.EnsureReserved() })
	}
}

// taskbarGuard keeps the native taskbar hidden.
//
// The shell does not accept a hidden taskbar. Whenever the work area changes, which
// is whenever a bar is registered, resized or moved, the shell lays the taskbar out
// afresh, and that layout restores the window: visible again, back at the bottom of
// the screen, within a second of being put away. A hide that ends with a work-area
// write therefore undoes itself, which is what the user sees: a taskbar that keeps
// coming back.
//
// The defence is to put it away again, and crucially to do that without touching the
// work area, because touching the work area is the thing that provokes the shell.
// Re-hiding the windows alone provokes nothing, and the shell then leaves them
// alone.
//
// The guard stops the moment the setting is turned off, and the taskbars are shown
// again by the settings path.
func (d *daemon) taskbarGuard() {
	ticker := time.NewTicker(taskbarGuardInterval)
	defer ticker.Stop()

	for range ticker.C {
		if d.quitting.Load() || !d.hideTaskbar.Load() {
			continue
		}

		d.guardMu.Lock()
		if !d.quitting.Load() && d.hideTaskbar.Load() {
			if d.taskbars.HideWindows() {
				log.Println("[NovaBar Taskbar] The shell brought the taskbar back; hiding it again")
			}
		}
		d.guardMu.Unlock()
	}
}

// mediaPump keeps the media session current.
// The media stack does publish change events, but subscribing to one means a callback
// crossing a thread boundary, and that is where the crashes in this area came from.
// Polling is dull and cannot fault, and a couple of seconds is well inside what a
// person notices.
func (d *daemon) mediaPump() {
	if d.media == nil {
		return
	}

	ticker := time.NewTicker(mediaPollInterval)
	defer ticker.Stop()

	previous := d.media.GetCurrentSession()
	for range ticker.C {
		// A refresh that fails is usually "nothing is playing", and the controller
		// clears the session in that case, so the comparison below still sees it.
		_ = d.media.Refresh()

		current := d.media.GetCurrentSession()
		if !mediaSessionChanged(previous, current) {
			continue
		}
		previous = current
		d.rpcs.Broadcast(rpc.TopicMediaState, current)
	}
}

// mediaSessionChanged reports whether two sessions differ in a way the user can see.
//
// The position is deliberately not compared. It moves continuously, so comparing it
// would turn every tick into a broadcast.
func mediaSessionChanged(a, b media.MediaSession) bool {
	return a.Source != b.Source ||
		a.AppID != b.AppID ||
		a.Title != b.Title ||
		a.Artist != b.Artist ||
		a.Album != b.Album ||
		a.Playback != b.Playback ||
		a.CanPlay != b.CanPlay ||
		a.CanPause != b.CanPause ||
		a.CanNext != b.CanNext ||
		a.CanPrevious != b.CanPrevious
}

// configPump applies live config reloads.
//
// A reload that fails to parse is reported and ignored: the watcher keeps the last good
// config, so a typo in config.toml can never take the shell down mid-edit.
func (d *daemon) configPump() {
	if d.watcher == nil {
		return
	}

	for ev := range d.watcher.Events() {
		if ev.Err != nil {
			log.Printf("[NovaBar] Config change rejected, keeping previous settings: %v\n", ev.Err)
			continue
		}

		cfg := d.watcher.Current()
		d.cfg = cfg

		d.engine.SetConfig(cfg)
		d.engine.DetectMonitors()

		// Adding, removing or resizing bars takes effect immediately; this is the
		// whole point of holding several [[bars]] entries in config.
		//
		// It happens on the message pump thread, because the bars own WebView2
		// surfaces and WebView2 is bound to the thread that created it. Doing it
		// here would kill the daemon.
		d.onPumpThread(func() {
			// Taskbar visibility is applied first so the shell has recomputed the work
			// area before the bars ask it for space.
			d.applyTaskbarVisibility(cfg)

			d.applyBars(cfg)
			d.syncDock(cfg)
		})

		d.retile()
		d.publishWindows()
	}
}
