package daemon

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/novabar-shell/novabar/internal/config"
	"github.com/novabar-shell/novabar/internal/onboarding"
	"github.com/novabar-shell/novabar/internal/tiling"
	"github.com/novabar-shell/novabar/internal/tracker"
	"github.com/novabar-shell/novabar/internal/win"
	"golang.org/x/sys/windows"
)

// Run starts the core NovaBar+ daemon and message loop.
func Run() {
	// Pin message pump goroutine to dedicated OS thread
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	log.Println("[NovaBar+] Starting daemon...")

	cfg, err := config.Load()
	if err != nil {
		log.Printf("[NovaBar+] Notice: using default config: %v\n", err)
	} else {
		log.Printf("[NovaBar+] Loaded config with default layout: %s\n", cfg.Tiling.DefaultLayout)
	}

	// Onboarding check: introduce user on first launch
	if cfg.General.FirstRun {
		onboarding.ShowWelcomeNotice(&cfg)
	}

	taskbars := win.NewTaskbarState()
	if cfg.Bar.HideTaskbar {
		log.Println("[NovaBar+] Hiding native taskbars as requested in config...")
		if err := taskbars.Hide(); err != nil {
			log.Printf("[NovaBar+] Warning hiding taskbars: %v\n", err)
		}
	}

	// Always restore taskbars when daemon exits or encounters an interrupt
	cleanup := func() {
		log.Println("[NovaBar+] Restoring native taskbars and exiting cleanly...")
		_ = taskbars.Show()
		win.ForceRestoreTaskbar()
	}
	defer cleanup()

	// Handle standard graceful exit signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("[NovaBar+] Signal caught, posting quit message...")
		win.PostQuitMessage(0)
	}()

	// Initialize window tracker & tiling engine
	trk := tracker.NewWindowTracker()
	trk.Refresh()
	log.Printf("[NovaBar+] Tracking initialized. Active manageable windows: %d\n", trk.Count())

	engine := tiling.NewEngine(cfg)
	log.Printf("[NovaBar+] Detected %d monitor(s). Work area: %+v\n", len(engine.GetMonitors()), engine.GetMonitors())

	// Initial tile
	if cfg.Tiling.Enabled {
		engine.TileWorkspace(trk.GetWindowHandles())
	}

	// Register out-of-context WinEventHook
	hookCb := windows.NewCallback(func(hWinEventHook uintptr, event uint32, hwnd windows.HWND, idObject int32, idChild int32, idEventThread uint32, dwmsEventTime uint32) uintptr {
		if idObject != win.OBJID_WINDOW || hwnd == 0 {
			return 0
		}
		// Lightweight event dispatcher
		switch event {
		case win.EVENT_SYSTEM_FOREGROUND:
			// Foreground changed
		case win.EVENT_OBJECT_SHOW, win.EVENT_OBJECT_DESTROY:
			// Reconcile window tracking on new/closed windows
			trk.Refresh()
			if cfg.Tiling.Enabled {
				engine.TileWorkspace(trk.GetWindowHandles())
			}
		}
		return 0
	})

	hookHandle, err := win.SetWinEventHook(
		win.EVENT_SYSTEM_FOREGROUND,
		win.EVENT_OBJECT_HIDE,
		0,
		hookCb,
		0,
		0,
		win.WINEVENT_OUTOFCONTEXT|win.WINEVENT_SKIPOWNPROCESS,
	)
	if err != nil || hookHandle == 0 {
		log.Printf("[NovaBar+] Warning: SetWinEventHook failed: %v\n", err)
	} else {
		defer win.UnhookWinEvent(hookHandle)
		log.Println("[NovaBar+] WinEventHook registered.")
	}

	log.Println("[NovaBar+] Running message pump. Press Ctrl+C or run 'shellctl --rescue' to exit.")

	var msg win.MSG
	for {
		res, err := win.GetMessage(&msg, 0, 0, 0)
		if res <= 0 || err != nil {
			break
		}
		win.TranslateMessage(&msg)
		win.DispatchMessage(&msg)
	}

	fmt.Println("[NovaBar+] Daemon terminated cleanly.")
}
