package hotkey

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
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
	// ActionToggleSettings opens the settings overlay.
	ActionToggleSettings Action = "toggle-settings"
	// ActionWorkspacePrev and ActionWorkspaceNext move between virtual workspaces.
	ActionWorkspacePrev Action = "workspace-prev"
	ActionWorkspaceNext Action = "workspace-next"

	// ActionFocus focuses the neighbouring window in a direction.
	ActionFocus Action = "focus"
	// ActionMove moves the focused window into its neighbour's place.
	ActionMove Action = "move"
	// ActionResize opens the zone picker for the focused window.
	ActionResize Action = "resize"
	// ActionWorkspace switches to a workspace.
	ActionWorkspace Action = "workspace"
	// ActionMoveToWorkspace moves the focused window to a workspace.
	ActionMoveToWorkspace Action = "move-to-workspace"
)

// The directions the focus and move actions work in.
const (
	DirectionLeft  = "left"
	DirectionDown  = "down"
	DirectionUp    = "up"
	DirectionRight = "right"
)

// Binding is a hotkey and what it does.
//
// A keymap binding carries more than an action name: the focus and move actions need
// a direction, and the workspace actions need a workspace number. A plain string
// cannot carry either, so a binding is a small struct rather than an action.
type Binding struct {
	// Action is the kind of thing the binding does.
	Action Action
	// Direction is one of the directions above, for the focus and move actions.
	Direction string
	// Workspace is the workspace number, for the workspace actions.
	Workspace int
}

// String describes a binding for the log.
func (b Binding) String() string {
	switch b.Action {
	case ActionFocus:
		return fmt.Sprintf("focus %s", b.Direction)
	case ActionMove:
		return fmt.Sprintf("move %s", b.Direction)
	case ActionWorkspace:
		return fmt.Sprintf("switch to workspace %d", b.Workspace)
	case ActionMoveToWorkspace:
		return fmt.Sprintf("move to workspace %d", b.Workspace)
	}
	return string(b.Action)
}

// Manager owns the daemon's global hotkey registrations.
//
// Registration must happen on the message-pump thread: WM_HOTKEY is delivered to the
// thread that called RegisterHotKey, so binding from any other goroutine produces a
// registration that never fires.
type Manager struct {
	mu       sync.RWMutex
	bindings map[uintptr]win.HotkeyBinding
	actions  map[uintptr]Binding
	// registered tracks ids we successfully bound, so Stop releases exactly those.
	registered []uintptr
	// failures records hotkeys another process already owns.
	failures []string
	// nextID hands out ids for the keymap bindings. The fixed actions have ids of
	// their own, so the two sets cannot collide.
	nextID uintptr
}

// NewManager creates an unbound hotkey manager.
func NewManager() *Manager {
	return &Manager{
		bindings: make(map[uintptr]win.HotkeyBinding, 8),
		actions:  make(map[uintptr]Binding, 8),
		nextID:   100,
	}
}

// Register binds the configured hotkeys.
//
// The rescue hotkey is bound first and independently of the others: a user who
// triggered a problem needs that one key to work even if every other binding conflicts.
func (m *Manager) Register(cfg config.Config) {
	m.Stop()

	// Rescue first, so its failure cannot prevent the others from being tried.
	m.tryRegister(win.HotkeyRescue, cfg.Hotkeys.Rescue, Binding{Action: ActionRescue})
	m.tryRegister(win.HotkeyLauncher, cfg.Hotkeys.Launcher, Binding{Action: ActionToggleLauncher})
	m.tryRegister(win.HotkeyWorkspacePrev, cfg.Hotkeys.WorkspacePrev, Binding{Action: ActionWorkspacePrev})
	m.tryRegister(win.HotkeyWorkspaceNext, cfg.Hotkeys.WorkspaceNext, Binding{Action: ActionWorkspaceNext})
	m.tryRegister(win.HotkeySettings, cfg.Hotkeys.Settings, Binding{Action: ActionToggleSettings})

	m.registerKeymap(cfg.Keymap.Bindings)

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

// registerKeymap binds the window management keymap.
//
// The bindings are sorted so the log and the registration order do not depend on the
// order the map happens to be walked in.
func (m *Manager) registerKeymap(bindings map[string]string) {
	names := make([]string, 0, len(bindings))
	for name := range bindings {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		binding, ok := ParseKeymapAction(name)
		if !ok {
			log.Printf("[NovaBar Hotkeys] Keymap action %q is not one this shell knows; ignoring it\n", name)
			continue
		}
		m.tryRegisterDynamic(bindings[name], binding)
	}
}

// tryRegisterDynamic binds a keymap binding under an id of its own.
func (m *Manager) tryRegisterDynamic(spec string, binding Binding) {
	if strings.TrimSpace(spec) == "" {
		return
	}

	parsed, err := win.ParseHotkeySpec(spec)
	if err != nil {
		log.Printf("[NovaBar Hotkeys] Invalid hotkey %q for %s: %v\n", spec, binding, err)
		return
	}

	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.mu.Unlock()

	if err := win.RegisterHotKey(id, parsed.Modifiers, parsed.VK); err != nil {
		m.mu.Lock()
		m.failures = append(m.failures, fmt.Sprintf("%s (%s)", parsed.String(), err.Error()))
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	m.bindings[id] = parsed
	m.actions[id] = binding
	m.registered = append(m.registered, id)
	m.mu.Unlock()

	log.Printf("[NovaBar Hotkeys] Bound %-24s -> %s\n", parsed.String(), binding)
}

// ParseKeymapAction turns a keymap action name into the thing it does.
//
// The names are fixed, so this is a lookup rather than a guess: a name that is not
// in the vocabulary is reported rather than bound to nothing.
func ParseKeymapAction(name string) (Binding, bool) {
	switch name {
	case config.KeymapFocusLeft:
		return Binding{Action: ActionFocus, Direction: DirectionLeft}, true
	case config.KeymapFocusDown:
		return Binding{Action: ActionFocus, Direction: DirectionDown}, true
	case config.KeymapFocusUp:
		return Binding{Action: ActionFocus, Direction: DirectionUp}, true
	case config.KeymapFocusRight:
		return Binding{Action: ActionFocus, Direction: DirectionRight}, true
	case config.KeymapMoveLeft:
		return Binding{Action: ActionMove, Direction: DirectionLeft}, true
	case config.KeymapMoveDown:
		return Binding{Action: ActionMove, Direction: DirectionDown}, true
	case config.KeymapMoveUp:
		return Binding{Action: ActionMove, Direction: DirectionUp}, true
	case config.KeymapMoveRight:
		return Binding{Action: ActionMove, Direction: DirectionRight}, true
	case config.KeymapResize:
		return Binding{Action: ActionResize}, true
	}

	if n, ok := workspaceNumber(name, "workspace_"); ok {
		return Binding{Action: ActionWorkspace, Workspace: n}, true
	}
	if n, ok := workspaceNumber(name, "move_workspace_"); ok {
		return Binding{Action: ActionMoveToWorkspace, Workspace: n}, true
	}

	return Binding{}, false
}

// workspaceNumber reads the number out of a workspace action name.
func workspaceNumber(name, prefix string) (int, bool) {
	if !strings.HasPrefix(name, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(name, prefix))
	if err != nil || n < 1 || n > 9 {
		return 0, false
	}
	return n, true
}

func (m *Manager) tryRegister(id uintptr, spec string, binding Binding) {
	if spec == "" {
		return
	}

	parsed, err := win.ParseHotkeySpec(spec)
	if err != nil {
		log.Printf("[NovaBar Hotkeys] Invalid hotkey %q: %v\n", spec, err)
		return
	}

	if err := win.RegisterHotKey(id, parsed.Modifiers, parsed.VK); err != nil {
		m.mu.Lock()
		m.failures = append(m.failures, fmt.Sprintf("%s (%s)", parsed.String(), err.Error()))
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	m.bindings[id] = parsed
	m.actions[id] = binding
	m.registered = append(m.registered, id)
	m.mu.Unlock()

	log.Printf("[NovaBar Hotkeys] Bound %-24s -> %s\n", parsed.String(), binding)
}

// Resolve maps a WM_HOTKEY id to what it does.
func (m *Manager) Resolve(id uintptr) (Binding, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	binding, ok := m.actions[id]
	return binding, ok
}

// Stop releases every registered hotkey.
func (m *Manager) Stop() {
	m.mu.Lock()
	ids := append([]uintptr(nil), m.registered...)
	m.registered = nil
	m.bindings = make(map[uintptr]win.HotkeyBinding, 8)
	m.actions = make(map[uintptr]Binding, 8)
	m.failures = nil
	m.nextID = 100
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
