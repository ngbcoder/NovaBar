package bar

import (
	"fmt"
	"log"
	"sort"
	"sync"

	"github.com/ngbcoder/NovaBar/internal/win"
)

// Edge codes, re-exported so callers do not need to import internal/win for config
// translation.
const (
	EdgeTop    = win.ABE_TOP
	EdgeBottom = win.ABE_BOTTOM
	EdgeLeft   = win.ABE_LEFT
	EdgeRight  = win.ABE_RIGHT
)

// Spec describes one desired bar. Kept separate from config so this package does not
// depend on the config schema.
type Spec struct {
	// Edge is one of EdgeTop, EdgeBottom, EdgeLeft, EdgeRight.
	Edge uint32
	// Size is the reserved thickness.
	Size int32
	// Name is a label for status output.
	Name string
}

// Rect is a screen-space rectangle, mirroring win.RECT so this package's status output
// does not leak the raw Win32 type into JSON.
type Rect struct {
	Left   int32 `json:"left"`
	Top    int32 `json:"top"`
	Right  int32 `json:"right"`
	Bottom int32 `json:"bottom"`
}

// Width returns the horizontal extent.
func (r Rect) Width() int32 { return r.Right - r.Left }

// Height returns the vertical extent.
func (r Rect) Height() int32 { return r.Bottom - r.Top }

// Info describes a live bar for status output.
type Info struct {
	Name       string  `json:"name"`
	Edge       string  `json:"edge"`
	Size       int32   `json:"size"`
	Registered bool    `json:"registered"`
	HWND       uintptr `json:"hwnd"`
	// Bounds is the rectangle this bar occupies, computed by us.
	Bounds Rect `json:"bounds"`
	// Granted is the thickness the shell actually credited against the work area.
	// Zero means the bar is drawn but the shell refused to shrink the desktop for it,
	// which is the normal outcome for a bottom bar while the native taskbar is shown:
	// Windows does not allow an AppBar to overlap the real taskbar.
	Granted int32 `json:"granted"`
	// GrantedReason explains a short grant.
	GrantedReason string `json:"granted_reason,omitempty"`
}

// Manager owns every AppBar the shell is running and reconciles them against config.
//
// Must be used from the message-pump thread: window creation and AppBar registration
// both deliver shell callbacks to the owning thread.
type Manager struct {
	mu   sync.Mutex
	bars map[uint32]*AppBarManager // keyed by edge
}

// NewManager creates an empty bar manager.
func NewManager() *Manager {
	return &Manager{bars: make(map[uint32]*AppBarManager, 4)}
}

// EdgeForName maps a config edge name to its ABE_* constant.
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

// Apply reconciles the live bars against the desired set.
//
// Bars not in specs are unregistered, bars in specs that are not live are created, and
// bars in both have their size updated. This is what makes "turn bars on and off in
// settings" work against a live-reloaded config without a restart.
func (m *Manager) Apply(specs []Spec) []error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error

	// Drop bars that are no longer wanted.
	wanted := make(map[uint32]Spec, len(specs))
	for _, spec := range specs {
		wanted[spec.Edge] = spec
	}
	for edge, ab := range m.bars {
		if _, keep := wanted[edge]; keep {
			continue
		}
		log.Printf("[NovaBar AppBar] Removing %s bar (no longer enabled)\n", EdgeName(edge))
		ab.Unregister()
		delete(m.bars, edge)
	}

	// Create or resize the rest. Specs arrive sorted so log output is deterministic.
	ordered := make([]Spec, 0, len(wanted))
	for _, spec := range wanted {
		ordered = append(ordered, spec)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Edge < ordered[j].Edge })

	for _, spec := range ordered {
		ab, exists := m.bars[spec.Edge]
		if !exists {
			ab = NewAppBarManager(spec.Edge, spec.Size, spec.Name)
			if err := ab.CreateAndRegister(); err != nil {
				errs = append(errs, fmt.Errorf("could not create %s bar: %w", EdgeName(spec.Edge), err))
				continue
			}
			m.bars[spec.Edge] = ab
			continue
		}
		if ab.Size() != spec.Size {
			log.Printf("[NovaBar AppBar] Resizing %s bar from %d to %d\n", EdgeName(spec.Edge), ab.Size(), spec.Size)
			ab.SetSize(spec.Size)
		}
	}

	return errs
}

// UpdatePosition re-applies the work-area reservation for every bar.
//
// Called when the display resolution changes or a monitor is added or removed. Also
// recomputes each bar's rectangle, because a resolution change moves every edge.
func (m *Manager) UpdatePosition() {
	m.mu.Lock()
	defer m.mu.Unlock()

	edges := make([]uint32, 0, len(m.bars))
	for edge := range m.bars {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i] < edges[j] })

	for _, edge := range edges {
		m.bars[edge].UpdatePosition()
	}
	m.recomputeRectsLocked(screenWidth(), screenHeight())
}

// EnsureReserved re-applies the shell reservation for every bar whose space has
// been taken away.
//
// The work area can be rewritten from outside the shell's AppBar bookkeeping, and
// when it is, the shell forgets that the bars were given any space. The bar keeps
// drawing itself, but the desktop is laid out as if the bar were not there, so a
// maximized window ends up underneath it with its title bar hidden behind it.
//
// The recovery path is the usual cause. It writes the work area outright, because
// writing it is the only way to clear a reservation left behind by a process that
// died, and it clears everybody's while it is at it.
//
// The repair is the same call that registers a bar, so it costs nothing.
func (m *Manager) EnsureReserved() {
	m.mu.Lock()
	defer m.mu.Unlock()

	work, err := win.PrimaryWorkAreaRect()
	if err != nil {
		return
	}
	screenW, screenH := screenWidth(), screenHeight()

	edges := make([]uint32, 0, len(m.bars))
	for edge := range m.bars {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i] < edges[j] })

	for _, edge := range edges {
		ab := m.bars[edge]

		granted := ab.Granted()
		if granted <= 0 {
			continue
		}

		lost := false
		switch edge {
		case win.ABE_TOP:
			lost = work.Top < granted
		case win.ABE_BOTTOM:
			lost = work.Bottom > screenH-granted
		case win.ABE_LEFT:
			lost = work.Left < granted
		case win.ABE_RIGHT:
			lost = work.Right > screenW-granted
		}
		if !lost {
			continue
		}

		log.Printf("[NovaBar AppBar] The %s bar lost its work-area reservation; claiming it again\n",
			EdgeName(edge))
		ab.UpdatePosition()
	}
}

// screenWidth and screenHeight read the primary display size.
func screenWidth() int32  { return win.GetSystemMetrics(0) }
func screenHeight() int32 { return win.GetSystemMetrics(1) }

// UnregisterAll tears down every bar. Safe on a nil or empty manager.
func (m *Manager) UnregisterAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	edges := make([]uint32, 0, len(m.bars))
	for edge := range m.bars {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i] < edges[j] })

	for _, edge := range edges {
		m.bars[edge].Unregister()
		delete(m.bars, edge)
	}
}

// Count returns the number of live bars.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.bars)
}

// Describe returns a status summary of every live bar.
func (m *Manager) Describe() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Info, 0, len(m.bars))
	for _, ab := range m.bars {
		bounds := ab.Bounds()
		info := Info{
			Name:       ab.Name(),
			Edge:       EdgeName(ab.Edge()),
			Size:       ab.Size(),
			Registered: ab.Registered(),
			HWND:       uintptr(ab.HWND()),
			Bounds: Rect{
				Left:   bounds.Left,
				Top:    bounds.Top,
				Right:  bounds.Right,
				Bottom: bounds.Bottom,
			},
		}
		info.Granted = ab.Granted()
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Edge < out[j].Edge })
	return out
}

// Collapsed returns bars that are registered but were granted no reserved space.
//
// This is not a cosmetic check. A bar in this state is a real configuration mistake:
// the native taskbar already owns that edge, so the bar is registered, reports
// success, reserves nothing, and is painted over by the taskbar. Surfacing it is the
// difference between "my bar is broken" and "my bar is fine but not visible".
func (m *Manager) Collapsed() []Info {
	var out []Info
	for _, info := range m.Describe() {
		if info.Registered && info.Granted == 0 {
			out = append(out, info)
		}
	}
	return out
}

// WorkArea returns the usable desktop area for a display: the full screen minus every
// bar rectangle.
//
// Computed by us rather than read from the shell, and that is the point. The shell's
// work area lags reality here: it will not let an AppBar overlap the native taskbar, so
// a bottom bar is denied credit and RcWork keeps excluding that strip. Since the tiling
// engine already does its own geometry, subtracting our own bar rects is both simpler
// and more accurate than believing a stale RcWork.
//
// screen is the display's full bounds, in screen coordinates.
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

// RecomputeRects reassigns every bar's rectangle so adjacent edges do not overlap.
//
// Without this a top bar claims (0,0)-(1920,44) and a left bar claims
// (0,0)-(64,1080), and the two fight over the top-left corner: the left bar's content
// is drawn underneath the top bar's, which is exactly what a dock that appears not to
// exist looks like.
//
// Each bar is placed against the screen with every *other* bar's thickness already
// removed, so the corner belongs to whichever bar registered first and the others start
// past it. Idempotent, and safe to call on every change.
func (m *Manager) RecomputeRects(screenW, screenH int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recomputeRectsLocked(screenW, screenH)
}

// recomputeRectsLocked is RecomputeRects with the lock already held.
func (m *Manager) recomputeRectsLocked(screenW, screenH int32) {
	if screenW <= 0 || screenH <= 0 {
		screenW, screenH = 1920, 1080
	}

	screen := Rect{Left: 0, Top: 0, Right: screenW, Bottom: screenH}

	// Thickness of each edge, taken from whichever bar owns it.
	thickness := map[uint32]int32{}
	for edge, ab := range m.bars {
		if !ab.Registered() {
			continue
		}
		thickness[edge] = ab.Size()
	}

	// Deterministic order so two runs from the same state agree on which bar gets the
	// contested corner.
	edges := make([]uint32, 0, len(thickness))
	for edge := range thickness {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i] < edges[j] })

	for _, edge := range edges {
		ab := m.bars[edge]
		if ab == nil || !ab.Registered() {
			continue
		}

		// Only perpendicular bars cause the corner problem, and only one of the two
		// should pay for it.
		//
		// A horizontal bar keeps the full width and owns the corner; a vertical bar
		// starts below or beside it. Symmetric exclusion, which was the first attempt,
		// inset both and left a 64x40 hole belonging to neither bar, which is worse than
		// an overlap because it is visible as a dead square.
		area := screen
		for _, other := range edges {
			if other == edge || !isPerpendicular(edge, other) {
				continue
			}
			// Horizontal wins the corner.
			if isHorizontal(edge) {
				continue
			}
			area = subtractEdge(area, edgeRect(other, screen, thickness[other]))
		}

		ab.SetRequestedRect(toWinRect(inflateTowardEdge(area, edge, thickness[edge])))
	}
}

// isHorizontal reports whether an edge runs left to right.
func isHorizontal(edge uint32) bool {
	return edge == win.ABE_TOP || edge == win.ABE_BOTTOM
}

// isPerpendicular reports whether two edges meet at a corner.
func isPerpendicular(a, b uint32) bool {
	return isHorizontal(a) != isHorizontal(b)
}

// toWinRect converts an internal Rect to the native RECT the window APIs take.
func toWinRect(r Rect) win.RECT {
	return win.RECT{Left: r.Left, Top: r.Top, Right: r.Right, Bottom: r.Bottom}
}

// edgeRect builds the strip a bar of the given thickness would occupy on an edge.
func edgeRect(edge uint32, screen Rect, thickness int32) Rect {
	return inflateTowardEdge(screen, edge, thickness)
}

// inflateTowardEdge grows a rectangle from one edge of itself by thickness.
func inflateTowardEdge(area Rect, edge uint32, thickness int32) Rect {
	if area.Width() <= 0 || area.Height() <= 0 {
		return area
	}

	r := area
	switch edge {
	case win.ABE_TOP:
		r.Bottom = minInt32(r.Top+thickness, r.Bottom)
	case win.ABE_BOTTOM:
		r.Top = maxInt32(r.Bottom-thickness, r.Top)
	case win.ABE_LEFT:
		r.Right = minInt32(r.Left+thickness, r.Right)
	case win.ABE_RIGHT:
		r.Left = maxInt32(r.Right-thickness, r.Left)
	}
	return r
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// subtractEdge removes the overlap between area and an edge-anchored bar.
func subtractEdge(area, bar Rect) Rect {
	// No overlap at all: leave the area alone.
	if bar.Right <= area.Left || bar.Left >= area.Right ||
		bar.Bottom <= area.Top || bar.Top >= area.Bottom {
		return area
	}

	// A full-width bar trims top or bottom; a full-height bar trims left or right.
	// Anything narrower than the area is assumed to be an overlay and ignored, since
	// removing a middle band would mean splitting the area in two.
	if bar.Left <= area.Left && bar.Right >= area.Right {
		if bar.Top <= area.Top {
			area.Top = bar.Bottom
		} else {
			area.Bottom = bar.Top
		}
		return area
	}
	if bar.Top <= area.Top && bar.Bottom >= area.Bottom {
		if bar.Left <= area.Left {
			area.Left = bar.Right
		} else {
			area.Right = bar.Left
		}
	}
	return area
}

// Get returns the bar on an edge, or nil.
func (m *Manager) Get(edge uint32) *AppBarManager {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bars[edge]
}
