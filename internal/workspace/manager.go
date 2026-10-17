package workspace

import (
	"log"
	"sort"
	"sync"

	"github.com/ngbcoder/NovaBar/internal/win"
	"golang.org/x/sys/windows"
)

// Count is the number of virtual workspace slots.
const Count = 9

// Workspace is one virtual desktop: a named set of windows.
type Workspace struct {
	ID     int
	Name   string
	Window map[windows.HWND]*Member
}

// Member records the visibility state a window had before its workspace was left, so
// it can be restored rather than blindly shown.
type Member struct {
	// WasMinimized is captured on hide. Windows hidden with SW_HIDE and then shown
	// with SW_SHOW reappear un-maximized, which is jarring for the user.
	WasMinimized bool
}

// Manager coordinates virtual workspaces.
//
// Switching hides the outgoing workspace's windows and restores the incoming one's,
// restoring prior minimized/maximized state. Windows on a hidden workspace are not
// destroyed or minimized, so no state is lost.
type Manager struct {
	mu         sync.RWMutex
	activeID   int
	workspaces map[int]*Workspace
}

// NewManager creates a workspace manager with slots 1-9.
func NewManager() *Manager {
	m := &Manager{
		activeID:   1,
		workspaces: make(map[int]*Workspace, Count),
	}
	for i := 1; i <= Count; i++ {
		m.workspaces[i] = &Workspace{
			ID:     i,
			Name:   string(rune('0' + i)),
			Window: make(map[windows.HWND]*Member),
		}
	}
	return m
}

// ActiveID returns the currently visible workspace.
func (m *Manager) ActiveID() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeID
}

// IDs returns every workspace id in ascending order.
func (m *Manager) IDs() []int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]int, 0, len(m.workspaces))
	for id := range m.workspaces {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// Switch moves to targetID. Unknown ids are ignored.
func (m *Manager) Switch(targetID int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if targetID == m.activeID {
		return
	}
	next, ok := m.workspaces[targetID]
	if !ok {
		log.Printf("[NovaBar Workspace] Ignoring switch to unknown workspace %d\n", targetID)
		return
	}

	prev := m.workspaces[m.activeID]

	// Hide the outgoing workspace, remembering minimized state so it can be restored.
	for hwnd, member := range prev.Window {
		if !win.IsValid(hwnd) {
			delete(prev.Window, hwnd)
			continue
		}
		member.WasMinimized = win.IsMinimized(hwnd)
		win.ShowWindow(hwnd, win.SW_HIDE)
	}

	// Restore the incoming workspace.
	for hwnd, member := range next.Window {
		if !win.IsValid(hwnd) {
			delete(next.Window, hwnd)
			continue
		}
		if member.WasMinimized {
			win.ShowWindow(hwnd, win.SW_SHOW)
			win.ShowWindow(hwnd, win.SW_MINIMIZE)
			continue
		}
		win.ShowWindow(hwnd, win.SW_SHOW)
	}

	m.activeID = targetID
	log.Printf("[NovaBar Workspace] Switched to workspace %d\n", targetID)
}

// Step moves the active workspace by delta, wrapping within 1..Count.
//
// The wrapper matters because Alt+Left / Alt+Right held down, or a keypress that
// repeats, would otherwise run off the end of the range.
func (m *Manager) Step(delta int) {
	current := m.ActiveID()
	next := ((current-1+delta)%Count+Count)%Count + 1
	m.Switch(next)
}

// Assign attaches a window to a workspace. A window belongs to exactly one workspace.
func (m *Manager) Assign(hwnd windows.HWND, wsID int) {
	if hwnd == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ws, ok := m.workspaces[wsID]
	if !ok {
		return
	}

	for _, other := range m.workspaces {
		if other == ws {
			continue
		}
		delete(other.Window, hwnd)
	}

	if _, exists := ws.Window[hwnd]; !exists {
		ws.Window[hwnd] = &Member{}
	}

	// A window moved onto a background workspace must not stay visible.
	if wsID != m.activeID && win.IsWindowVisible(hwnd) {
		win.ShowWindow(hwnd, win.SW_HIDE)
	}
}

// Move takes a window from the active workspace to targetID.
func (m *Manager) Move(hwnd windows.HWND, targetID int) {
	m.Assign(hwnd, targetID)
}

// AssignAll puts every handle on one workspace. Used at startup so existing windows
// are not orphaned in no workspace at all.
func (m *Manager) AssignAll(handles []windows.HWND, wsID int) {
	for _, h := range handles {
		m.Assign(h, wsID)
	}
}

// Prune drops handles that no longer refer to live windows.
//
// Without this the workspace maps grow for the life of the session and Switch spends
// time ShowWindow-ing dead handles.
func (m *Manager) Prune() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	removed := 0
	for _, ws := range m.workspaces {
		for hwnd := range ws.Window {
			if !win.IsValid(hwnd) {
				delete(ws.Window, hwnd)
				removed++
			}
		}
	}
	return removed
}

// Has reports whether a window is assigned to any workspace.
func (m *Manager) Has(hwnd windows.HWND) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, ws := range m.workspaces {
		if _, ok := ws.Window[hwnd]; ok {
			return true
		}
	}
	return false
}

// Windows returns the live handles on a workspace in ascending handle order.
func (m *Manager) Windows(wsID int) []windows.HWND {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ws, ok := m.workspaces[wsID]
	if !ok {
		return nil
	}

	out := make([]windows.HWND, 0, len(ws.Window))
	for h := range ws.Window {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// VisibleWindows returns the handles on the active workspace only, which is the set
// the tiling engine should lay out.
func (m *Manager) VisibleWindows() []windows.HWND {
	return m.Windows(m.ActiveID())
}

// Describe returns a compact per-workspace summary for status output.
func (m *Manager) Describe() []WorkspaceSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]WorkspaceSummary, 0, len(m.workspaces))
	for _, id := range sortedIDs(m.workspaces) {
		ws := m.workspaces[id]
		out = append(out, WorkspaceSummary{
			ID:          ws.ID,
			Name:        ws.Name,
			Active:      ws.ID == m.activeID,
			WindowCount: len(ws.Window),
		})
	}
	return out
}

// WorkspaceSummary is one entry in Describe's output.
type WorkspaceSummary struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Active      bool   `json:"active"`
	WindowCount int    `json:"window_count"`
}

func sortedIDs(m map[int]*Workspace) []int {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}
