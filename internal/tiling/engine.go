package tiling

import (
	"log"

	"github.com/novabar-shell/novabar/internal/config"
	"github.com/novabar-shell/novabar/internal/win"
	"golang.org/x/sys/windows"
)

// MonitorInfo represents an active display monitor.
type MonitorInfo struct {
	HMonitor uintptr
	Bounds   Rect
	WorkArea Rect
	Primary  bool
}

// WindowNode represents a window being tiled.
type WindowNode struct {
	HWnd  windows.HWND
	Title string
	Class string
	Float bool
}

// Workspace represents a single workspace containing tiled and floating windows.
type Workspace struct {
	ID      int
	Name    string
	Layout  string // "bsp", "columns", "rows", "monocle"
	Windows []*WindowNode
}

// Engine coordinates multi-monitor tiling and workspace layouts.
type Engine struct {
	cfg        config.Config
	monitors   []MonitorInfo
	workspaces map[int]*Workspace
	activeWS   int
}

// NewEngine initializes the tiling engine.
func NewEngine(cfg config.Config) *Engine {
	e := &Engine{
		cfg:        cfg,
		monitors:   make([]MonitorInfo, 0),
		workspaces: make(map[int]*Workspace),
		activeWS:   1,
	}
	e.workspaces[1] = &Workspace{
		ID:     1,
		Name:   "1",
		Layout: cfg.Tiling.DefaultLayout,
	}
	e.DetectMonitors()
	return e
}

// DetectMonitors enumerates all physical displays.
func (e *Engine) DetectMonitors() {
	e.monitors = e.monitors[:0]
	cb := windows.NewCallback(func(hMonitor uintptr, hdc uintptr, lprc uintptr, dwData uintptr) uintptr {
		var mi win.MONITORINFO
		if win.GetMonitorInfo(hMonitor, &mi) {
			primary := (mi.DwFlags & win.MONITOR_DEFAULTTOPRIMARY) != 0
			e.monitors = append(e.monitors, MonitorInfo{
				HMonitor: hMonitor,
				Bounds: Rect{
					X:      int(mi.RcMonitor.Left),
					Y:      int(mi.RcMonitor.Top),
					Width:  int(mi.RcMonitor.Width()),
					Height: int(mi.RcMonitor.Height()),
				},
				WorkArea: Rect{
					X:      int(mi.RcWork.Left),
					Y:      int(mi.RcWork.Top),
					Width:  int(mi.RcWork.Width()),
					Height: int(mi.RcWork.Height()),
				},
				Primary: primary,
			})
		}
		return 1
	})
	_ = win.EnumDisplayMonitors(0, nil, cb, 0)
}

// GetMonitors returns the active detected monitors.
func (e *Engine) GetMonitors() []MonitorInfo {
	return e.monitors
}

// TileWorkspace applies the chosen layout to all managed windows in the active workspace.
func (e *Engine) TileWorkspace(windowsList []windows.HWND) {
	if !e.cfg.Tiling.Enabled || len(windowsList) == 0 || len(e.monitors) == 0 {
		return
	}

	primaryWorkArea := e.monitors[0].WorkArea
	for _, m := range e.monitors {
		if m.Primary {
			primaryWorkArea = m.WorkArea
			break
		}
	}

	gaps := Gaps{
		Outer: e.cfg.Tiling.OuterGap,
		Inner: e.cfg.Tiling.InnerGap,
	}

	var targetRects []Rect
	switch e.cfg.Tiling.DefaultLayout {
	case "columns":
		targetRects = LayoutColumns(primaryWorkArea, len(windowsList), gaps)
	case "rows":
		targetRects = LayoutRows(primaryWorkArea, len(windowsList), gaps)
	case "monocle":
		targetRects = LayoutMonocle(primaryWorkArea, len(windowsList), gaps)
	case "bsp":
		fallthrough
	default:
		targetRects = LayoutBSP(primaryWorkArea, len(windowsList), gaps)
	}

	if len(targetRects) != len(windowsList) {
		return
	}

	// Batch update window positions smoothly with BeginDeferWindowPos
	hDWP := win.BeginDeferWindowPos(int32(len(windowsList)))
	if hDWP == 0 {
		return
	}

	for i, hwnd := range windowsList {
		rect := targetRects[i]
		// Compensate for invisible DWM drop shadow borders
		extBounds, err := win.GetExtendedFrameBounds(hwnd)
		adjX := int32(rect.X)
		adjY := int32(rect.Y)
		adjW := int32(rect.Width)
		adjH := int32(rect.Height)

		if err == nil {
			var regularRect win.RECT
			// If frame bounds differ from regular rect, compensate offsets
			diffX := extBounds.Left - regularRect.Left
			diffY := extBounds.Top - regularRect.Top
			adjX -= diffX
			adjY -= diffY
		}

		hDWP = win.DeferWindowPos(
			hDWP,
			hwnd,
			win.HWND_TOP,
			adjX,
			adjY,
			adjW,
			adjH,
			win.SWP_NOACTIVATE|win.SWP_NOZORDER,
		)
		if hDWP == 0 {
			log.Println("[NovaBar+ Tiling] DeferWindowPos failed during batching")
			break
		}
	}

	if hDWP != 0 {
		win.EndDeferWindowPos(hDWP)
	}
}
