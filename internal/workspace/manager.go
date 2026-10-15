package workspace

import (
	"log"
	"sync"

	"github.com/novabar-shell/novabar/internal/win"
	"golang.org/x/sys/windows"
)

// Workspace encapsulates a virtual desktop grouping of windows.
type Workspace struct {
	ID      int
	Name    string
	Windows map[windows.HWND]bool
}

// Manager coordinates switching and assigning windows between virtual workspaces.
type Manager struct {
	mu         sync.RWMutex
	activeID   int
	workspaces map[int]*Workspace
}

// NewManager creates a workspace manager with standard 1-9 desktop slots.
func NewManager() *Manager {
	m := &Manager{
		activeID:   1,
		workspaces: make(map[int]*Workspace),
	}
	for i := 1; i <= 9; i++ {
		m.workspaces[i] = &Workspace{
			ID:      i,
			Name:    string(rune('0' + i)),
			Windows: make(map[windows.HWND]bool),
		}
	}
	return m
}

// Switch changes the active workspace, hiding windows of the previous and showing the target.
func (m *Manager) Switch(targetID int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if targetID == m.activeID || m.workspaces[targetID] == nil {
		return
	}

	prevWS := m.workspaces[m.activeID]
	nextWS := m.workspaces[targetID]

	// Hide windows from current workspace
	for hwnd := range prevWS.Windows {
		if win.IsWindowVisible(hwnd) {
			win.ShowWindow(hwnd, win.SW_HIDE)
		}
	}

	// Show windows assigned to target workspace
	for hwnd := range nextWS.Windows {
		win.ShowWindow(hwnd, win.SW_SHOW)
	}

	m.activeID = targetID
	log.Printf("[NovaBar+ Workspace] Switched to workspace %d\n", targetID)
}

// AssignWindow attaches a window handle to a designated workspace.
func (m *Manager) AssignWindow(hwnd windows.HWND, wsID int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ws := m.workspaces[wsID]
	if ws == nil {
		return
	}

	// Remove from other workspaces first
	for _, other := range m.workspaces {
		delete(other.Windows, hwnd)
	}

	ws.Windows[hwnd] = true

	// If assigned to a non-active workspace, hide it immediately
	if wsID != m.activeID {
		win.ShowWindow(hwnd, win.SW_HIDE)
	}
}

// GetActiveID returns the currently visible workspace ID.
func (m *Manager) GetActiveID() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeID
}

// GetWorkspaceWindows returns the windows assigned to the specified workspace.
func (m *Manager) GetWorkspaceWindows(wsID int) []windows.HWND {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ws := m.workspaces[wsID]
	if ws == nil {
		return nil
	}

	res := make([]windows.HWND, 0, len(ws.Windows))
	for h := range ws.Windows {
		res = append(res, h)
	}
	return res
}
