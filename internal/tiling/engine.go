package tiling

import (
	"log"
	"sort"
	"strings"
	"sync"

	"github.com/ngbcoder/NovaBar/internal/config"
	"github.com/ngbcoder/NovaBar/internal/win"
	"golang.org/x/sys/windows"
)

// MonitorInfo represents an active display monitor.
type MonitorInfo struct {
	HMonitor uintptr
	Bounds   Rect
	WorkArea Rect
	Primary  bool
}

// Engine coordinates multi-monitor tiling.
//
// It is a pure geometry calculator plus a thin Win32 apply step: Decide() can be
// exercised without touching any window, which is what the layout tests do.
type Engine struct {
	mu       sync.RWMutex
	cfg      config.Config
	rules    *RuleSet
	monitors []MonitorInfo
}

// NewEngine initializes the tiling engine.
func NewEngine(cfg config.Config) *Engine {
	e := &Engine{
		cfg:      cfg,
		rules:    NewRuleSet(cfg.Rules),
		monitors: make([]MonitorInfo, 0, 4),
	}
	e.DetectMonitors()
	return e
}

// SetConfig swaps the active configuration, recompiling rules and re-detecting
// monitors. Used by config live-reload.
func (e *Engine) SetConfig(cfg config.Config) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg = cfg
	e.rules = NewRuleSet(cfg.Rules)
	e.detectMonitorsLocked()
}

// Config returns the active configuration snapshot.
func (e *Engine) Config() config.Config {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg
}

// DetectMonitors re-enumerates all physical displays.
func (e *Engine) DetectMonitors() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.detectMonitorsLocked()
}

func (e *Engine) detectMonitorsLocked() {
	monitors := make([]MonitorInfo, 0, 4)

	win.SetMonitorHandler(func(hMonitor uintptr, hdc uintptr, lprc uintptr, dwData uintptr) uintptr {
		var mi win.MONITORINFO
		if win.GetMonitorInfo(hMonitor, &mi) {
			monitors = append(monitors, MonitorInfo{
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
				Primary: (mi.DwFlags & win.MONITOR_DEFAULTTOPRIMARY) != 0,
			})
		}
		return 1
	})
	win.EnumDisplayMonitors(0, nil)
	win.SetMonitorHandler(nil)

	// Stable monitor order: primary first, then by position. Layout decisions must
	// not depend on EnumDisplayMonitors iteration order.
	sort.SliceStable(monitors, func(i, j int) bool {
		if monitors[i].Primary != monitors[j].Primary {
			return monitors[i].Primary
		}
		if monitors[i].Bounds.X != monitors[j].Bounds.X {
			return monitors[i].Bounds.X < monitors[j].Bounds.X
		}
		return monitors[i].Bounds.Y < monitors[j].Bounds.Y
	})

	e.monitors = monitors
}

// GetMonitors returns the active detected monitors.
func (e *Engine) GetMonitors() []MonitorInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]MonitorInfo, len(e.monitors))
	copy(out, e.monitors)
	return out
}

// WindowSubject describes a window for layout purposes, decoupled from the tracker so
// the engine can be driven from tests or from any source.
type WindowSubject struct {
	HWnd     windows.HWND
	ProcName string
	Title    string
	Class    string
}

// Placement is the decision for a single window: its target visible rectangle, or a
// directive to leave it alone.
type Placement struct {
	HWnd windows.HWND
	// Rect is the desired position of the window's *visible* frame.
	Rect Rect
	// Action is ActionTile, ActionFloat or ActionIgnore.
	Action Action
	// RuleDescription explains a non-tile decision.
	RuleDescription string
}

// Decide computes layout placements for a set of windows without touching Win32.
//
// Windows are grouped by the monitor they currently occupy. Floating and ignored
// windows are excluded from the layout and returned with ActionFloat/ActionIgnore so
// the caller can apply them as a separate, cheaper step.
func (e *Engine) Decide(subjects []WindowSubject) []Placement {
	e.mu.RLock()
	cfg := e.cfg
	rules := e.rules
	monitors := make([]MonitorInfo, len(e.monitors))
	copy(monitors, e.monitors)
	e.mu.RUnlock()

	if !cfg.Tiling.Enabled || len(subjects) == 0 || len(monitors) == 0 {
		return nil
	}

	gaps := Gaps{Outer: cfg.Tiling.OuterGap, Inner: cfg.Tiling.InnerGap}

	// Partition by monitor, keeping caller order within each group.
	byMonitor := make(map[int][]WindowSubject, len(monitors))
	tileable := make([]WindowSubject, 0, len(subjects))
	placements := make([]Placement, 0, len(subjects))

	for _, s := range subjects {
		decision := rules.Evaluate(WindowAttributes{
			ProcName: s.ProcName,
			Class:    s.Class,
			Title:    s.Title,
		})

		// Built-in guarantees hold even with an empty or broken rule list.
		if decision.RuleIndex < 0 && IsDefaultFloating(s.ProcName) {
			decision = Decision{
				Action:          ActionFloat,
				RuleIndex:       -1,
				RuleDescription: "built-in floating default",
			}
		}

		if decision.Action != ActionTile {
			action := decision.Action
			if action == "" {
				action = ActionFloat
			}
			placements = append(placements, Placement{
				HWnd:            s.HWnd,
				Action:          action,
				RuleDescription: decision.RuleDescription,
			})
			continue
		}

		tileable = append(tileable, s)
		idx := e.monitorIndexFor(s.HWnd, monitors)
		byMonitor[idx] = append(byMonitor[idx], s)
	}

	for monitorIdx, group := range byMonitor {
		if monitorIdx < 0 || monitorIdx >= len(monitors) || len(group) == 0 {
			continue
		}
		workArea := monitors[monitorIdx].WorkArea
		rects := e.layoutFor(workArea, len(group), gaps)

		for i, s := range group {
			if i >= len(rects) {
				break
			}
			placements = append(placements, Placement{
				HWnd:   s.HWnd,
				Rect:   rects[i],
				Action: ActionTile,
			})
		}
	}

	return placements
}

// layoutFor dispatches to the configured layout function.
func (e *Engine) layoutFor(bounds Rect, count int, gaps Gaps) []Rect {
	switch strings.ToLower(e.cfg.Tiling.DefaultLayout) {
	case "columns":
		return LayoutColumns(bounds, count, gaps)
	case "rows":
		return LayoutRows(bounds, count, gaps)
	case "monocle":
		return LayoutMonocle(bounds, count, gaps)
	case "grid":
		return LayoutGridCols(applyOuterGaps(bounds, gaps.Outer), count, gaps, 0)
	case "bsp":
		fallthrough
	default:
		return LayoutBSP(bounds, count, gaps)
	}
}

// monitorIndexFor returns the index in monitors of the display owning hwnd, or 0.
func (e *Engine) monitorIndexFor(hwnd windows.HWND, monitors []MonitorInfo) int {
	if hwnd == 0 {
		return 0
	}
	hMon := win.MonitorFromWindow(hwnd, win.MONITOR_DEFAULTTONEAREST)
	for i, m := range monitors {
		if m.HMonitor == hMon {
			return i
		}
	}
	return 0
}

func normalizeProcName(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".exe"))
}

// TileSubjects computes placements and applies them to Win32 in one batched
// BeginDeferWindowPos/EndDeferWindowPos pass.
func (e *Engine) TileSubjects(subjects []WindowSubject) []Placement {
	placements := e.Decide(subjects)
	if len(placements) == 0 {
		return nil
	}

	toMove := make([]Placement, 0, len(placements))
	for _, p := range placements {
		if p.Action == ActionTile {
			toMove = append(toMove, p)
		}
	}

	if len(toMove) > 0 {
		e.applyPlacements(toMove)
	}

	// Float ignored and floating windows over the primary work area so they are
	// actually usable rather than left at a stale position.
	e.applyFloating(placements)

	return placements
}

// applyPlacements performs the batched window move.
func (e *Engine) applyPlacements(placements []Placement) {
	hDWP := win.BeginDeferWindowPos(int32(len(placements)))
	if hDWP == 0 {
		log.Printf("[NovaBar Tiling] BeginDeferWindowPos(%d) failed; falling back to direct moves\n", len(placements))
		for _, p := range placements {
			e.moveOne(p)
		}
		return
	}

	for _, p := range placements {
		if !win.IsValid(p.HWnd) {
			continue
		}

		target := p.Rect
		if correction, ok := frameCorrectionFor(p.HWnd); ok {
			target = correction.ApplyToTarget(target)
		}

		hDWP = win.DeferWindowPos(
			hDWP,
			p.HWnd,
			win.HWND_TOP,
			int32(target.X),
			int32(target.Y),
			int32(target.Width),
			int32(target.Height),
			win.SWP_NOACTIVATE|win.SWP_NOZORDER,
		)
		if hDWP == 0 {
			log.Println("[NovaBar Tiling] DeferWindowPos failed mid-batch; aborting batch")
			return
		}
	}

	if !win.EndDeferWindowPos(hDWP) {
		log.Println("[NovaBar Tiling] EndDeferWindowPos failed; no windows were moved")
	}
}

// moveOne applies a single placement without batching. Used as the fallback path.
func (e *Engine) moveOne(p Placement) {
	if !win.IsValid(p.HWnd) {
		return
	}
	target := p.Rect
	if correction, ok := frameCorrectionFor(p.HWnd); ok {
		target = correction.ApplyToTarget(target)
	}
	_ = win.SetWindowPos(
		p.HWnd,
		win.HWND_TOP,
		int32(target.X),
		int32(target.Y),
		int32(target.Width),
		int32(target.Height),
		win.SWP_NOACTIVATE|win.SWP_NOZORDER,
	)
}

// applyFloating brings floating windows forward, sized to the work area of the
// display they are on.
//
// The window's own display, not the primary one. A floating window the user dragged
// to a second screen belongs to that screen, and moving it back to the primary is the
// wrong answer twice over: the window lands where the user did not put it, and it
// lands somewhere they cannot see it.
func (e *Engine) applyFloating(placements []Placement) {
	monitors := e.GetMonitors()
	if len(monitors) == 0 {
		return
	}

	for _, p := range placements {
		if p.Action != ActionFloat || !win.IsValid(p.HWnd) {
			continue
		}

		workArea := workAreaForMonitor(win.MonitorFromWindow(p.HWnd, win.MONITOR_DEFAULTTONEAREST), monitors)
		size := Rect{
			X:      workArea.X + workArea.Width*2/5,
			Y:      workArea.Y + workArea.Height/5,
			Width:  workArea.Width * 3 / 5,
			Height: workArea.Height * 3 / 5,
		}

		_ = win.SetWindowPos(
			p.HWnd,
			win.HWND_TOP,
			int32(size.X),
			int32(size.Y),
			int32(size.Width),
			int32(size.Height),
			win.SWP_NOACTIVATE,
		)
	}
}

// workAreaForMonitor returns the work area of a display.
//
// A display that is not in the list falls back to the first one, which is the
// primary: a window whose display cannot be resolved was on the primary to begin
// with.
func workAreaForMonitor(hMonitor uintptr, monitors []MonitorInfo) Rect {
	if hMonitor != 0 {
		for _, m := range monitors {
			if m.HMonitor == hMonitor {
				return m.WorkArea
			}
		}
	}
	return monitors[0].WorkArea
}

// frameCorrectionFor measures a window's DWM border and returns the correction to
// apply so its visible frame lands where intended.
//
// Returns false when frame bounds are unavailable, which happens for windows DWM has
// not yet computed and for windows the current process cannot query.
func frameCorrectionFor(hwnd windows.HWND) (FrameCorrection, bool) {
	windowRect, err := win.GetWindowRect(hwnd)
	if err != nil {
		return FrameCorrection{}, false
	}
	frameRect, err := win.GetExtendedFrameBounds(hwnd)
	if err != nil {
		return FrameCorrection{}, false
	}

	pair := FromWinRects(
		int(windowRect.Left), int(windowRect.Top), int(windowRect.Width()), int(windowRect.Height()),
		int(frameRect.Left), int(frameRect.Top), int(frameRect.Width()), int(frameRect.Height()),
	)

	correction := ComputeFrameCorrection(pair)
	if correction.OffsetX == 0 && correction.OffsetY == 0 && correction.InsetX == 0 && correction.InsetY == 0 {
		// Genuinely borderless window, or degenerate measurement. Either way, no
		// correction is the correct answer.
		return FrameCorrection{}, true
	}
	return correction, true
}
