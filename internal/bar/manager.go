package bar

import (
	"fmt"
	"log"
	"sort"
	"sync"

	"github.com/ngbcoder/NovaBar/internal/win"
)

const (
	EdgeTop    = win.ABE_TOP
	EdgeBottom = win.ABE_BOTTOM
	EdgeLeft   = win.ABE_LEFT
	EdgeRight  = win.ABE_RIGHT
)

type Spec struct {
	Edge uint32
	Size int32
	Name string
}

type Rect struct {
	Left   int32 `json:"left"`
	Top    int32 `json:"top"`
	Right  int32 `json:"right"`
	Bottom int32 `json:"bottom"`
}

func (r Rect) Width() int32  { return r.Right - r.Left }
func (r Rect) Height() int32 { return r.Bottom - r.Top }

type Info struct {
	Name          string  `json:"name"`
	Edge          string  `json:"edge"`
	Size          int32   `json:"size"`
	Registered    bool    `json:"registered"`
	HWND          uintptr `json:"hwnd"`
	Bounds        Rect    `json:"bounds"`
	Granted       int32   `json:"granted"`
	GrantedReason string  `json:"granted_reason,omitempty"`
}

const (
	ScopePrimary = "primary"
	ScopeDesktop = "desktop"
)

type Manager struct {
	mu    sync.Mutex
	bars  []*AppBarManager
	scope string
}

func NewManager() *Manager {
	return &Manager{bars: make([]*AppBarManager, 0, 4)}
}

func EdgeForName(name string) (uint32, bool) {
	switch name {
	case "top":
		return win.ABE_TOP, true
	case "bottom":
		return win.ABE_BOTTOM, true
	case "left":
		return win.ABE_LEFT, true
	case "right":
		return win.ABE_RIGHT, true
	default:
		return 0, false
	}
}

func (m *Manager) targetMonitors() []win.RECT {
	var monitors []win.RECT
	if m.scope == ScopePrimary {
		if primary, err := win.PrimaryMonitor(); err == nil && primary.Width() > 0 {
			monitors = append(monitors, primary)
		}
	}
	if len(monitors) == 0 {
		win.SetMonitorHandler(func(hMonitor uintptr, hdc uintptr, lprc uintptr, dwData uintptr) uintptr {
			var mi win.MONITORINFO
			if win.GetMonitorInfo(hMonitor, &mi) {
				monitors = append(monitors, mi.RcMonitor)
			}
			return 1
		})
		win.EnumDisplayMonitors(0, nil)
		win.SetMonitorHandler(nil)
	}
	if len(monitors) == 0 {
		if primary, err := win.PrimaryMonitor(); err == nil && primary.Width() > 0 {
			monitors = append(monitors, primary)
		} else {
			monitors = append(monitors, win.RECT{Left: 0, Top: 0, Right: 1920, Bottom: 1080})
		}
	}
	return monitors
}

func (m *Manager) Apply(specs []Spec) []error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	monitors := m.targetMonitors()

	type barKey struct {
		monitor win.RECT
		edge    uint32
	}

	desired := make(map[barKey]Spec)
	for _, mon := range monitors {
		for _, spec := range specs {
			desired[barKey{monitor: mon, edge: spec.Edge}] = spec
		}
	}

	var retained []*AppBarManager
	existing := make(map[barKey]*AppBarManager)
	for _, ab := range m.bars {
		k := barKey{monitor: ab.MonitorRect(), edge: ab.Edge()}
		if _, ok := desired[k]; ok {
			existing[k] = ab
		} else {
			log.Printf("[NovaBar AppBar] Removing %s bar on monitor (%d,%d)\n", EdgeName(ab.Edge()), ab.MonitorRect().Left, ab.MonitorRect().Top)
			ab.Unregister()
		}
	}

	for k, spec := range desired {
		if ab, ok := existing[k]; ok {
			retained = append(retained, ab)
			if ab.Size() != spec.Size {
				log.Printf("[NovaBar AppBar] Resizing %s bar from %d to %d\n", EdgeName(spec.Edge), ab.Size(), spec.Size)
				ab.SetSize(spec.Size)
			}
		} else {
			ab := NewAppBarManager(k.monitor, spec.Edge, spec.Size, spec.Name)
			if err := ab.CreateAndRegister(); err != nil {
				errs = append(errs, fmt.Errorf("could not create %s bar: %w", EdgeName(spec.Edge), err))
				continue
			}
			retained = append(retained, ab)
		}
	}

	m.bars = retained
	m.recomputeRectsLocked(m.desktop())
	return errs
}

func (m *Manager) UpdatePosition() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ab := range m.bars {
		ab.UpdatePosition()
	}
	m.recomputeRectsLocked(m.desktop())
}

func (m *Manager) EnsureReserved() {
	m.mu.Lock()
	defer m.mu.Unlock()

	work, err := win.PrimaryWorkAreaRect()
	if err != nil {
		return
	}

	for _, ab := range m.bars {
		if !barNeedsReclaim(ab, work) {
			continue
		}
		if !ab.claimDue() {
			continue
		}
		// Re-register rather than only re-apply the position. The usual way a
		// reservation is lost is a restart of the shell, and the shell that comes
		// back has never heard of this window: SETPOS alone is answered politely
		// and ignored.
		ab.Reclaim()
	}
}

// barNeedsReclaim reports whether a registered bar is missing the space it draws.
//
// The test is the work area, not what the shell answered the last time the bar asked.
// Those two can disagree in a way that matters: a bar that registers while another
// process' bar still owns the edge is granted nothing, and once that other bar goes
// away the work area takes the strip back. Asking "was I granted space?" would leave
// that bar skipped forever, with the desktop laid out under the bar and a maximized
// window's title bar hidden behind it.
func barNeedsReclaim(ab *AppBarManager, work win.RECT) bool {
	if !ab.Registered() {
		return false
	}
	return !ab.ownsSpace(work)
}

func screenWidth() int32  { return win.VirtualScreen().Width() }
func screenHeight() int32 { return win.VirtualScreen().Height() }

func (m *Manager) UnregisterAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ab := range m.bars {
		ab.Unregister()
	}
	m.bars = nil
}

func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.bars)
}

func (m *Manager) Get(edge uint32) *AppBarManager {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ab := range m.bars {
		if ab.Edge() == edge {
			return ab
		}
	}
	return nil
}

func (m *Manager) Describe() []Info {
	return m.Infos()
}

func (m *Manager) Collapsed() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Info
	for _, ab := range m.bars {
		if ab.Registered() && ab.Granted() == 0 {
			b := ab.Bounds()
			out = append(out, Info{
				Name:       ab.Name(),
				Edge:       EdgeName(ab.Edge()),
				Size:       ab.Size(),
				Registered: ab.Registered(),
				HWND:       uintptr(ab.HWND()),
				Bounds:     Rect{Left: b.Left, Top: b.Top, Right: b.Right, Bottom: b.Bottom},
				Granted:    ab.Granted(),
			})
		}
	}
	return out
}

func (m *Manager) Infos() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Info, len(m.bars))
	for i, ab := range m.bars {
		b := ab.Bounds()
		out[i] = Info{
			Name:       ab.Name(),
			Edge:       EdgeName(ab.Edge()),
			Size:       ab.Size(),
			Registered: ab.Registered(),
			HWND:       uintptr(ab.HWND()),
			Bounds:     Rect{Left: b.Left, Top: b.Top, Right: b.Right, Bottom: b.Bottom},
			Granted:    ab.Granted(),
		}
	}
	return out
}

func (m *Manager) SetScope(scope string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if scope != ScopePrimary {
		scope = ScopeDesktop
	}
	m.scope = scope
}

func (m *Manager) desktop() Rect {
	v := win.VirtualScreen()
	return Rect{Left: v.Left, Top: v.Top, Right: v.Right, Bottom: v.Bottom}
}

func (m *Manager) WorkArea(screen Rect) Rect {
	m.mu.Lock()
	rects := make([]Rect, 0, len(m.bars))
	for _, ab := range m.bars {
		if !ab.Registered() {
			continue
		}
		r := ab.Bounds()
		rects = append(rects, Rect{Left: r.Left, Top: r.Top, Right: r.Right, Bottom: r.Bottom})
	}
	m.mu.Unlock()

	area := screen
	for _, r := range rects {
		area = subtractEdge(area, r)
	}
	return area
}

func (m *Manager) RecomputeRects() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recomputeRectsLocked(m.desktop())
}

func (m *Manager) recomputeRectsLocked(screen Rect) {
	if screen.Width() <= 0 || screen.Height() <= 0 {
		screen = Rect{Left: 0, Top: 0, Right: 1920, Bottom: 1080}
	}

	byMonitor := make(map[win.RECT][]*AppBarManager)
	for _, ab := range m.bars {
		if !ab.Registered() {
			continue
		}
		mon := ab.MonitorRect()
		byMonitor[mon] = append(byMonitor[mon], ab)
	}

	for mon, monitorBars := range byMonitor {
		monRect := Rect{Left: mon.Left, Top: mon.Top, Right: mon.Right, Bottom: mon.Bottom}
		thickness := map[uint32]int32{}
		for _, ab := range monitorBars {
			thickness[ab.Edge()] = ab.Size()
		}

		edges := make([]uint32, 0, len(thickness))
		for edge := range thickness {
			edges = append(edges, edge)
		}
		sort.Slice(edges, func(i, j int) bool { return edges[i] < edges[j] })

		for _, edge := range edges {
			var targetAb *AppBarManager
			for _, ab := range monitorBars {
				if ab.Edge() == edge {
					targetAb = ab
					break
				}
			}
			if targetAb == nil {
				continue
			}

			area := monRect
			for _, other := range edges {
				if other == edge || !isPerpendicular(edge, other) {
					continue
				}
				if isHorizontal(edge) {
					continue
				}
				area = subtractEdge(area, edgeRect(other, monRect, thickness[other]))
			}

			targetAb.SetRequestedRect(toWinRect(inflateTowardEdge(area, edge, thickness[edge])))
		}
	}

	if primaryMon, err := win.PrimaryMonitor(); err == nil && primaryMon.Width() > 0 {
		pScreen := Rect{Left: primaryMon.Left, Top: primaryMon.Top, Right: primaryMon.Right, Bottom: primaryMon.Bottom}
		pWork := pScreen
		for _, ab := range m.bars {
			if !ab.Registered() {
				continue
			}
			pWork = subtractEdge(pWork, Rect{Left: ab.Bounds().Left, Top: ab.Bounds().Top, Right: ab.Bounds().Right, Bottom: ab.Bounds().Bottom})
		}
		_ = win.SetWorkArea(win.RECT{Left: pWork.Left, Top: pWork.Top, Right: pWork.Right, Bottom: pWork.Bottom}, false)
	}
}

func isHorizontal(edge uint32) bool {
	return edge == win.ABE_TOP || edge == win.ABE_BOTTOM
}

func isPerpendicular(a, b uint32) bool {
	return isHorizontal(a) != isHorizontal(b)
}

func toWinRect(r Rect) win.RECT {
	return win.RECT{Left: r.Left, Top: r.Top, Right: r.Right, Bottom: r.Bottom}
}

func edgeRect(edge uint32, screen Rect, thickness int32) Rect {
	return inflateTowardEdge(screen, edge, thickness)
}

func inflateTowardEdge(area Rect, edge uint32, thickness int32) Rect {
	if area.Width() <= 0 || area.Height() <= 0 {
		return area
	}
	r := area
	switch edge {
	case win.ABE_TOP:
		r.Bottom = min(r.Top+thickness, area.Bottom)
	case win.ABE_BOTTOM:
		r.Top = max(r.Bottom-thickness, area.Top)
	case win.ABE_LEFT:
		r.Right = min(r.Left+thickness, area.Right)
	case win.ABE_RIGHT:
		r.Left = max(r.Right-thickness, area.Left)
	}
	return r
}

func subtractEdge(area Rect, edge Rect) Rect {
	if area.Width() <= 0 || area.Height() <= 0 {
		return area
	}
	if edge.Top <= area.Top && edge.Bottom >= area.Bottom {
		if edge.Left <= area.Left {
			area.Left = max(area.Left, edge.Right)
		} else if edge.Right >= area.Right {
			area.Right = min(area.Right, edge.Left)
		}
	}
	if edge.Left <= area.Left && edge.Right >= area.Right {
		if edge.Top <= area.Top {
			area.Top = max(area.Top, edge.Bottom)
		} else if edge.Bottom >= area.Bottom {
			area.Bottom = min(area.Bottom, edge.Top)
		}
	}
	return area
}

func max(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func min(a, b int32) int32 {
	if a > b {
		return b
	}
	return a
}
