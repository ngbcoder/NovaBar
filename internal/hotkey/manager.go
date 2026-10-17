package hotkey

import (
	"fmt"
	"log"
	"sort"
	"sync"

	"github.com/ngbcoder/NovaBar/internal/config"
	"github.com/ngbcoder/NovaBar/internal/win"
)

// Action is what a hotkey triggers.
type Action string

const (
	// ActionRescue restores the native taskbars and shuts the shell down. This is the
	// escape hatch and is bound first so it works even if everything else is wedged.
	ActionRescue Action = "rescue"
	// ActionToggleLauncher shows or hides the app launcher.
	ActionToggleLauncher Action = "toggle-launcher"
	// ActionWorkspacePrev and ActionWorkspaceNext move between virtual workspaces.
	ActionWorkspacePrev Action = "workspace-prev"
	ActionWorkspaceNext Action = "workspace-next"
)

// Manager owns the daemon's global hotkey registrations.
//
// Registration must happen on the message-pump thread: WM_HOTKEY is delivered to the
// thread that called RegisterHotKey, so binding from any other goroutine produces a
// registration that never fires.
type Manager struct {
	mu       sync.RWMutex
	bindings map[uintptr]win.HotkeyBinding
	actions  map[uintptr]Action
	// registered tracks ids we successfully bound, so Stop releases exactly those.
	registered []uintptr
	// failures records hotkeys another process already owns.
	failures []string
}

// NewManager creates an unbound hotkey manager.
func NewManager() *Manager {
	return &Manager{
		bindings: make(map[uintptr]win.HotkeyBinding, 4),
		actions:  make(map[uintptr]Action, 4),
	}
}

// Register binds the configured hotkeys.
//
// The rescue hotkey is bound first and independently of the others: a user who
// triggered a problem needs that one key to work even if every other binding conflicts.
func (m *Manager) Register(cfg config.Config) {
	m.Stop()

	// Rescue first, so its failure cannot prevent the others from being tried.
	m.tryRegister(win.HotkeyRescue, cfg.Hotkeys.Rescue, ActionRescue)
	m.tryRegister(win.HotkeyLauncher, cfg.Hotkeys.Launcher, ActionToggleLauncher)
	m.tryRegister(win.HotkeyWorkspacePrev, cfg.Hotkeys.WorkspacePrev, ActionWorkspacePrev)
	m.tryRegister(win.HotkeyWorkspaceNext, cfg.Hotkeys.WorkspaceNext, ActionWorkspaceNext)

	m.mu.RLock()
	failures := append([]string(nil), m.failures...)
	m.mu.RUnlock()

	for _, f := range failures {
		log.Printf("[NovaBar Hotkeys] Could not bind %s: already in use by another application\n", f)
	}
	if len(m.registered) == 0 {
		log.Println("[NovaBar Hotkeys] No hotkeys could be bound; use 'shellctl --rescue' for recovery")
	}
}

func (m *Manager) tryRegister(id uintptr, spec string, action Action) {
	if spec == "" {
		return
	}

	binding, err := win.ParseHotkeySpec(spec)
	if err != nil {
		log.Printf("[NovaBar Hotkeys] Invalid hotkey %q: %v\n", spec, err)
		return
	}

	if err := win.RegisterHotKey(id, binding.Modifiers, binding.VK); err != nil {
		m.mu.Lock()
		m.failures = append(m.failures, fmt.Sprintf("%s (%s)", binding.String(), err.Error()))
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	m.bindings[id] = binding
	m.actions[id] = action
	m.registered = append(m.registered, id)
	m.mu.Unlock()

	log.Printf("[NovaBar Hotkeys] Bound %-24s -> %s\n", binding.String(), action)
}

// Resolve maps a WM_HOTKEY id to its action.
func (m *Manager) Resolve(id uintptr) (Action, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	action, ok := m.actions[id]
	return action, ok
}

// Stop releases every registered hotkey.
func (m *Manager) Stop() {
	m.mu.Lock()
	ids := append([]uintptr(nil), m.registered...)
	m.registered = nil
	m.bindings = make(map[uintptr]win.HotkeyBinding, 4)
	m.actions = make(map[uintptr]Action, 4)
	m.failures = nil
	m.mu.Unlock()

	// Unregister in ascending order for deterministic teardown.
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	win.UnregisterHotKeys(ids)
}

// Registered returns a human-readable list of active bindings.
func (m *Manager) Registered() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]uintptr, 0, len(m.bindings))
	for id := range m.bindings {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, fmt.Sprintf("%s -> %s", m.bindings[id].String(), m.actions[id]))
	}
	return out
}
