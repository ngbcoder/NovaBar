package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ngbcoder/NovaBar/internal/bar"
	"github.com/ngbcoder/NovaBar/internal/config"
	"github.com/ngbcoder/NovaBar/internal/hotkey"
	"github.com/ngbcoder/NovaBar/internal/launcher"
	"github.com/ngbcoder/NovaBar/internal/media"
	"github.com/ngbcoder/NovaBar/internal/onboarding"
	"github.com/ngbcoder/NovaBar/internal/rpc"
	"github.com/ngbcoder/NovaBar/internal/tiling"
	"github.com/ngbcoder/NovaBar/internal/tracker"
	"github.com/ngbcoder/NovaBar/internal/watchdog"
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

// daemon owns the long-lived components and their shutdown ordering.
type daemon struct {
	cfg     config.Config
	watcher *config.Watcher

	taskbars *win.TaskbarState
	appbar   *bar.AppBarManager
	trk      *tracker.WindowTracker
	engine   *tiling.Engine
	spaces   *workspace.Manager
	indexer  *launcher.Indexer
	media    *media.Controller
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
		taskbars: win.NewTaskbarState(),
		trk:      tracker.NewWindowTracker(),
		spaces:   workspace.NewManager(),
	}
	// Capture the pump thread before anything can signal us, so an early Ctrl+C still
	// reaches the message loop.
	d.pumpThread = windows.GetCurrentThreadId()

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
//
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
	if d.cfg.Bar.HideTaskbar {
		if err := d.taskbars.Hide(); err != nil {
			log.Printf("[NovaBar] Warning hiding taskbars: %v\n", err)
		} else {
			log.Println("[NovaBar] Native taskbars hidden (watchdog is watching this process)")
		}
	} else {
		// Make sure a previous crashed run did not leave them hidden.
		win.ForceRestoreTaskbar()
	}

	if d.cfg.Bar.Enabled {
		edge := uint32(win.ABE_TOP)
		if d.cfg.Bar.Position == "bottom" {
			edge = uint32(win.ABE_BOTTOM)
		}
		d.appbar = bar.NewAppBarManager(edge, int32(d.cfg.Bar.Height))
		if err := d.appbar.CreateAndRegister(); err != nil {
			log.Printf("[NovaBar] Warning: could not register AppBar: %v\n", err)
			d.appbar = nil
		}
	}

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
		log.Printf("[NovaBar] Media: no Windows media session bridge attached; transport controls unavailable\n")
	}

	d.engine = tiling.NewEngine(d.cfg)

	d.rpcs = rpc.NewServer()
	d.registerRPCMethods()
	d.rpcs.SetControlHandler(d.handleControl)
	if err := d.rpcs.Start(); err != nil {
		return fmt.Errorf("could not start IPC server: %w", err)
	}

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

// teardown restores the desktop. Runs on every exit path, including rescue.
func (d *daemon) teardown() {
	log.Println("[NovaBar] Restoring the desktop")

	if d.hotkeys != nil {
		d.hotkeys.Stop()
	}
	if d.watcher != nil {
		d.watcher.Close()
	}
	if d.appbar != nil {
		d.appbar.Unregister()
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

// pump runs the Win32 message loop.
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
		case win.WM_HOTKEY:
			d.handleHotkey(msg.WParam)
		}

		win.TranslateMessage(&msg)
		win.DispatchMessage(&msg)
	}
}

// handleHotkey dispatches a WM_HOTKEY.
func (d *daemon) handleHotkey(id uintptr) {
	action, ok := d.hotkeys.Resolve(id)
	if !ok {
		return
	}
	log.Printf("[NovaBar] Hotkey: %s\n", action)

	switch action {
	case hotkey.ActionRescue:
		log.Println("[NovaBar] Rescue hotkey pressed; restoring the desktop")
		d.performRescue()
	case hotkey.ActionToggleLauncher:
		// The launcher surface is an M4 deliverable. Until then the toggle still
		// resolves to something honest rather than silently doing nothing.
		log.Println("[NovaBar] Launcher toggle: no launcher surface attached yet (arrives with the WebView2 UI in M4)")
	case hotkey.ActionWorkspacePrev:
		d.spaces.Step(-1)
		d.afterWorkspaceChange()
	case hotkey.ActionWorkspaceNext:
		d.spaces.Step(1)
		d.afterWorkspaceChange()
	}
}

// performRescue restores the desktop and exits.
//
// Uses requestQuit rather than calling PostQuitMessage directly: this can be reached
// from the control goroutine, and PostQuitMessage posts WM_QUIT to the *calling*
// thread's queue. Calling it off the pump thread leaves the loop spinning forever
// after cleanup, which is exactly how the first rescue test left an orphaned process.
func (d *daemon) performRescue() {
	d.teardown()
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
			"index":       i,
			"bounds":      m.Bounds,
			"work_area":   m.WorkArea,
			"primary":     m.Primary,
		})
	}

	appbarState := map[string]any{"enabled": d.cfg.Bar.Enabled}
	if d.appbar != nil {
		appbarState["registered"] = d.appbar.Registered()
		appbarState["hwnd"] = uintptr(d.appbar.HWND())
		appbarState["bounds"] = d.appbar.Bounds()
	}

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
		"hotkeys":        d.hotkeys.Registered(),
		"watchdog_pid":   d.dog.PID(),
		"rpc_port":       d.rpcs.Port(),
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

	d.engine.TileSubjects(subjects)
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
			"workspace":      d.spaces.ActiveID(),
			"monitors":       len(d.engine.GetMonitors()),
			"goroutines":     runtime.NumGoroutine(),
		})
	}
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

		if d.appbar != nil {
			if cfg.Bar.Position == "bottom" && d.appbar.Edge() != uint32(win.ABE_BOTTOM) {
				log.Println("[NovaBar] Bar position changed; restart required to move the AppBar edge")
			}
			d.appbar.UpdatePosition()
		}

		// Hotkeys are owned by the pump thread; rebinding here would register against
		// the wrong thread, so this takes effect on next start.
		if cfg.Bar.HideTaskbar && !d.taskbars.IsHidden() {
			if err := d.taskbars.Hide(); err != nil {
				log.Printf("[NovaBar] Could not hide taskbars on config reload: %v\n", err)
			}
		} else if !cfg.Bar.HideTaskbar && d.taskbars.IsHidden() {
			if err := d.taskbars.Show(); err != nil {
				log.Printf("[NovaBar] Could not restore taskbars on config reload: %v\n", err)
			}
		}

		d.retile()
		d.publishWindows()
	}
}