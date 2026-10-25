package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Valid layout names accepted by the tiling engine.
var validLayouts = map[string]bool{
	"bsp":     true,
	"columns": true,
	"rows":    true,
	"grid":    true,
	"monocle": true,
}

// Valid rule actions.
var validActions = map[string]bool{
	"tile":   true,
	"float":  true,
	"ignore": true,
}

// Config is the root configuration document.
type Config struct {
	General GeneralConfig `toml:"general"`
	Tiling  TilingConfig  `toml:"tiling"`
	Bars    []BarConfig   `toml:"bars"`
	Hotkeys HotkeyConfig  `toml:"hotkeys"`
	Keymap  KeymapConfig  `toml:"keymap"`
	Rules   []RuleConfig  `toml:"rules"`
	Dock    DockConfig    `toml:"dock"`
}

// DockConfig is the floating macOS-style dock.
//
// It is not a [[bars]] entry because it does not reserve screen space. A dock that floats
// over the wallpaper leaves windows underneath it fully usable, which is the point of the
// design, and modelling it as an AppBar would mean lying about the work area.
type DockConfig struct {
	// Enabled shows the dock.
	Enabled bool `toml:"enabled"`
	// Size is the icon size in pixels.
	Size int `toml:"size"`
	// BottomMargin is the gap between the dock and the bottom of the work area.
	BottomMargin int `toml:"bottom_margin"`
}

// The bar scopes.
const (
	// BarScopeDesktop gives every bar the whole desktop.
	BarScopeDesktop = "desktop"
	// BarScopePrimary keeps every bar on the primary display.
	BarScopePrimary = "primary"
)

type GeneralConfig struct {
	FirstRun bool   `toml:"first_run"`
	LogLevel string `toml:"log_level"`
	// RescueKey is kept in sync with Hotkeys.Rescue; see DefaultConfig.
	RescueKey string `toml:"rescue_hotkey"`
	// HideTaskbar hides the native Windows taskbar.
	//
	// Global rather than per-bar because it is one action on one shell object, not a
	// property of any individual bar. Two bars each asking to hide it would be
	// meaningless.
	HideTaskbar bool `toml:"hide_taskbar"`
	// BarScope is how far the bars reach: "desktop" or "primary".
	//
	// "desktop" gives every bar the whole desktop, so one top bar spans both
	// displays. "primary" keeps each bar on the primary display, and the other
	// displays are left alone.
	//
	// The AppBar API reserves a rectangle rather than an edge, so both are possible:
	// the shell reserves the rectangle the bar asks for and nothing beyond it.
	BarScope string `toml:"bar_scope"`
}

type TilingConfig struct {
	Enabled       bool   `toml:"enabled"`
	DefaultLayout string `toml:"default_layout"`
	InnerGap      int    `toml:"inner_gap"`
	OuterGap      int    `toml:"outer_gap"`
}

// BarConfig is one AppBar. Multiple bars may be enabled simultaneously, at most one
// per screen edge: the AppBar API reserves a single strip per edge, so two bars on the
// same edge would fight over the work area.
type BarConfig struct {
	// Edge is "top", "bottom", "left" or "right".
	Edge string `toml:"edge"`
	// Size is the reserved thickness: vertical extent for top and bottom bars,
	// horizontal extent for left and right bars.
	Size    int  `toml:"size"`
	Enabled bool `toml:"enabled"`
	// Name is a free-form label for status output and future per-bar styling.
	Name string `toml:"name"`
	// Widget names the widget directory this bar renders, under the widgets root.
	// Empty means "bar", the shipped default.
	Widget string `toml:"widget"`
	// Transparent composites the widget over the desktop instead of painting an
	// opaque background.
	Transparent bool `toml:"transparent"`
}

// HotkeyConfig holds the global hotkey bindings as config strings, e.g. "Win+Space".
type HotkeyConfig struct {
	Rescue        string `toml:"rescue"`
	Launcher      string `toml:"launcher"`
	WorkspacePrev string `toml:"workspace_prev"`
	WorkspaceNext string `toml:"workspace_next"`
	// Settings opens the settings overlay.
	Settings string `toml:"settings"`
}

// KeymapConfig holds the window management keymap.
//
// A table rather than a field per binding, because there are a lot of bindings and
// they belong together: the four directions of three actions, and the workspaces.
// The names are fixed and the values are hotkey specs, e.g.
//
//	[keymap]
//	focus_left = 'Ctrl+Alt+H'
//	workspace_3 = 'Alt+3'
type KeymapConfig struct {
	// Bindings maps an action name to its hotkey spec.
	Bindings map[string]string `toml:"keymap"`
}

// The keymap action names.
//
// These are the vocabulary: a binding may use any of them and nothing else, so a
// typo in a binding is reported rather than silently ignored.
const (
	// KeymapFocusLeft and friends focus the neighbouring window.
	KeymapFocusLeft  = "focus_left"
	KeymapFocusDown  = "focus_down"
	KeymapFocusUp    = "focus_up"
	KeymapFocusRight = "focus_right"

	// KeymapMoveLeft and friends move the focused window into its neighbour's place.
	KeymapMoveLeft  = "move_left"
	KeymapMoveDown  = "move_down"
	KeymapMoveUp    = "move_up"
	KeymapMoveRight = "move_right"

	// KeymapResize opens the zone picker for the focused window.
	KeymapResize = "resize"

	// KeymapWorkspaceN switches to workspace N, and KeymapMoveWorkspaceN moves the
	// focused window there. N is 1 to 9.
	KeymapWorkspace1     = "workspace_1"
	KeymapWorkspace2     = "workspace_2"
	KeymapWorkspace3     = "workspace_3"
	KeymapWorkspace4     = "workspace_4"
	KeymapWorkspace5     = "workspace_5"
	KeymapWorkspace6     = "workspace_6"
	KeymapWorkspace7     = "workspace_7"
	KeymapWorkspace8     = "workspace_8"
	KeymapWorkspace9     = "workspace_9"
	KeymapMoveWorkspace1 = "move_workspace_1"
	KeymapMoveWorkspace2 = "move_workspace_2"
	KeymapMoveWorkspace3 = "move_workspace_3"
	KeymapMoveWorkspace4 = "move_workspace_4"
	KeymapMoveWorkspace5 = "move_workspace_5"
	KeymapMoveWorkspace6 = "move_workspace_6"
	KeymapMoveWorkspace7 = "move_workspace_7"
	KeymapMoveWorkspace8 = "move_workspace_8"
	KeymapMoveWorkspace9 = "move_workspace_9"
)

// KeymapActions lists every action name the keymap accepts.
func KeymapActions() []string {
	out := []string{
		KeymapFocusLeft, KeymapFocusDown, KeymapFocusUp, KeymapFocusRight,
		KeymapMoveLeft, KeymapMoveDown, KeymapMoveUp, KeymapMoveRight,
		KeymapResize,
	}
	for n := 1; n <= 9; n++ {
		out = append(out, fmt.Sprintf("workspace_%d", n))
		out = append(out, fmt.Sprintf("move_workspace_%d", n))
	}
	return out
}

// DefaultKeymap returns the default window management bindings.
//
// The chords are ones Windows does not already own. The obvious candidates are taken:
// Win+H, Win+K and Win+L are Windows' own, and so is every one of Win+1 to
// Win+9, which switch virtual desktops. A binding that cannot be registered is a
// binding that silently does nothing, so the defaults avoid the collisions.
func DefaultKeymap() map[string]string {
	out := map[string]string{
		KeymapFocusLeft:  "Ctrl+Alt+H",
		KeymapFocusDown:  "Ctrl+Alt+J",
		KeymapFocusUp:    "Ctrl+Alt+K",
		KeymapFocusRight: "Ctrl+Alt+L",

		KeymapMoveLeft:  "Ctrl+Alt+Shift+H",
		KeymapMoveDown:  "Ctrl+Alt+Shift+J",
		KeymapMoveUp:    "Ctrl+Alt+Shift+K",
		KeymapMoveRight: "Ctrl+Alt+Shift+L",

		KeymapResize: "Ctrl+Alt+Win+R",
	}

	for n := 1; n <= 9; n++ {
		out[fmt.Sprintf("workspace_%d", n)] = fmt.Sprintf("Alt+%d", n)
		out[fmt.Sprintf("move_workspace_%d", n)] = fmt.Sprintf("Ctrl+Shift+%d", n)
	}

	return out
}

type RuleConfig struct {
	Process string `toml:"process"`
	Class   string `toml:"class"`
	Title   string `toml:"title"`
	Action  string `toml:"action"` // "float", "tile", "ignore"
}

// DefaultConfig provides safe, standard defaults.
//
// HideTaskbar defaults to false. It is the single most dangerous setting in the file:
// it is what the watchdog exists to recover from, and defaulting it off means a fresh
// install cannot strand a user with no desktop.
func DefaultConfig() Config {
	return Config{
		General: GeneralConfig{
			FirstRun:  true,
			LogLevel:  "info",
			RescueKey: "Ctrl+Alt+Shift+R",
			// The floating dock is this shell's taskbar, so the native one is hidden by
			// default. Leaving Windows' taskbar on screen underneath the dock means
			// showing two, which is not what a taskbar replacement should look like.
			//
			// This was previously false because hiding the taskbar on a first run, before
			// the user has been told how to get out, is the worst time to do it. The
			// safety reasoning is unchanged, only the default moved: the rescue hotkey is
			// bound at startup, the watchdog restores the taskbars if the daemon dies, and
			// the settings surface refuses to change either this or the rescue hotkey while
			// the rescue path is not working. TestDefaultHideTaskbarIsSafe asserts those,
			// rather than asserting the flag itself.
			HideTaskbar: true,
			// The bars stay on the primary display by default.
			//
			// A bar that reaches the whole desktop is drawn across every display,
			// but the shell reserves the work-area strip only for the display the
			// bar's rectangle starts on. So the second display's windows can still
			// slide under the bar that is drawn over them, which is the one thing a
			// bar must not do.
			BarScope: BarScopePrimary,
		},
		Tiling: TilingConfig{
			Enabled:       true,
			DefaultLayout: "bsp",
			InnerGap:      8,
			OuterGap:      12,
		},
		// One top bar out of the box. Users can enable more from config.toml by
		// adding further [[bars]] entries, at most one per edge.
		Bars: []BarConfig{
			{Edge: "top", Size: 40, Enabled: true, Name: "main", Widget: "bar"},
		},
		Hotkeys: HotkeyConfig{
			Rescue:        "Ctrl+Alt+Shift+R",
			Launcher:      "Win+Space",
			WorkspacePrev: "Alt+Left",
			WorkspaceNext: "Alt+Right",
			Settings:      "Ctrl+Alt+S",
		},
		Keymap: KeymapConfig{
			Bindings: DefaultKeymap(),
		},
		Rules: []RuleConfig{
			{Process: "Taskmgr.exe", Action: "float"},
			{Process: "explorer.exe", Class: "OperationStatusWindow", Action: "float"},
			{Process: "TextInputHost.exe", Action: "ignore"},
		},
		Dock: DockConfig{
			Enabled:      true,
			Size:         56,
			BottomMargin: 10,
		},
	}
}

// GetConfigPath returns the config file location.
//
// The config lives under %APPDATA%\NovaBar, next to the pin store and the IPC
// discovery file, so all three subsystems agree on where state lives.
func GetConfigPath() (string, error) {
	base := os.Getenv("APPDATA")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(base, "NovaBar", "config.toml"), nil
}

// Load loads the config, creating a default file on first run.
//
// A malformed file returns DefaultConfig plus an error: the caller decides whether to
// run with defaults rather than dying on a typo.
func Load() (Config, error) {
	path, err := GetConfigPath()
	if err != nil {
		return DefaultConfig(), err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := DefaultConfig()
		if saveErr := Save(cfg); saveErr != nil {
			return cfg, fmt.Errorf("could not create default config at %s: %w", path, saveErr)
		}
		return cfg, nil
	} else if err != nil {
		return SafeConfig(), fmt.Errorf("read config error: %w", err)
	}

	cfg, err := Parse(data)
	if err != nil {
		// The file exists but is unusable. Go inert instead of defaulting to a fully
		// featured shell: see SafeConfig.
		return SafeConfig(), fmt.Errorf("parse config error: %w", err)
	}
	return cfg, nil
}

// utf8BOM is the byte-order mark Windows editors prepend to UTF-8 files.
//
// PowerShell 5.1's Set-Content, Notepad and several editors all write it. go-toml
// rejects it outright with "invalid character at start of key", so a config the user
// believes is fine silently fails to load.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// Parse decodes and validates a config document.
//
// Unmarshals onto DefaultConfig so a sparse document inherits defaults rather than
// zeroing every field.
func Parse(data []byte) (Config, error) {
	data = bytes.TrimPrefix(data, utf8BOM)

	cfg := DefaultConfig()
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if err := Validate(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// SafeConfig returns an inert configuration.
//
// Used as the fallback when a config file exists but cannot be read. A desktop shell
// must not treat an unreadable config as permission to enable everything: the whole
// point of a broken config is that the user's intent is unknown, so the correct
// response is to change nothing until they fix it. Falling back to DefaultConfig here
// once registered an AppBar and reserved screen space despite the user's config
// having bar.enabled = false.
func SafeConfig() Config {
	cfg := DefaultConfig()

	// Nothing takes over the desktop.
	cfg.Bars = []BarConfig{}
	cfg.General.HideTaskbar = false
	cfg.Tiling.Enabled = false

	// But the recovery paths stay live, because those are what protect the user.
	cfg.General.FirstRun = false

	return cfg
}

// Validate checks the config and normalizes safe values in place.
//
// It returns an error for values that would produce a broken or unsafe desktop, and
// clamps values that are merely out of range.
//
// Takes a pointer on purpose: validation normalizes (lowercases the layout, caps gaps,
// defaults an empty rule action). A value receiver would discard every one of those
// fixes, leaving the caller's config unchanged while reporting success.
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	layout := strings.ToLower(strings.TrimSpace(cfg.Tiling.DefaultLayout))
	if layout == "" {
		cfg.Tiling.DefaultLayout = "bsp"
	} else if !validLayouts[layout] {
		return fmt.Errorf("tiling.default_layout %q is not one of bsp, columns, rows, grid, monocle", cfg.Tiling.DefaultLayout)
	} else {
		cfg.Tiling.DefaultLayout = layout
	}

	if cfg.Tiling.InnerGap < 0 {
		cfg.Tiling.InnerGap = 0
	}
	if cfg.Tiling.OuterGap < 0 {
		cfg.Tiling.OuterGap = 0
	}
	// Gaps larger than this make tiles smaller than any usable window.
	if cfg.Tiling.InnerGap > 200 {
		cfg.Tiling.InnerGap = 200
	}
	if cfg.Tiling.OuterGap > 200 {
		cfg.Tiling.OuterGap = 200
	}

	scope := strings.ToLower(strings.TrimSpace(cfg.General.BarScope))
	if scope == "" {
		cfg.General.BarScope = BarScopePrimary
	} else if scope != BarScopeDesktop && scope != BarScopePrimary {
		return fmt.Errorf("general.bar_scope %q is not one of desktop, primary", cfg.General.BarScope)
	} else {
		cfg.General.BarScope = scope
	}

	if err := validateBars(cfg.Bars); err != nil {
		return err
	}

	if err := validateKeymap(cfg); err != nil {
		return err
	}

	// Dock bounds are clamped rather than rejected: an odd icon size is cosmetic, and
	// refusing to start because of one would be a poor trade.
	if cfg.Dock.Size < 28 {
		cfg.Dock.Size = 28
	}
	if cfg.Dock.Size > 112 {
		cfg.Dock.Size = 112
	}
	if cfg.Dock.BottomMargin < 0 {
		cfg.Dock.BottomMargin = 0
	}
	if cfg.Dock.BottomMargin > 200 {
		cfg.Dock.BottomMargin = 200
	}

	for i := range cfg.Rules {
		action := strings.ToLower(strings.TrimSpace(cfg.Rules[i].Action))
		if action == "" {
			cfg.Rules[i].Action = "tile"
			continue
		}
		if !validActions[action] {
			return fmt.Errorf("rules[%d].action %q must be one of tile, float, ignore", i, cfg.Rules[i].Action)
		}
		cfg.Rules[i].Action = action
	}

	return nil
}

// Valid screen edges for a bar.
var validEdges = map[string]bool{
	"top":    true,
	"bottom": true,
	"left":   true,
	"right":  true,
}

// validateBars normalizes and checks the bar list in place.
//
// The important rule is one bar per edge. The AppBar API reserves a single strip per
// screen edge, so two bars claiming the same edge would each shrink the work area from
// the same side and leave the user with a gap that is twice as large as either bar.
// validateKeymap checks the window management bindings.
//
// An unknown action name is an error rather than something to ignore: a binding
// named "focus_lft" does nothing at all, and the user would be left with a key
// that does not work and no explanation.
func validateKeymap(cfg *Config) error {
	if len(cfg.Keymap.Bindings) == 0 {
		cfg.Keymap.Bindings = DefaultKeymap()
		return nil
	}

	known := make(map[string]bool, len(KeymapActions()))
	for _, action := range KeymapActions() {
		known[action] = true
	}

	for name, spec := range cfg.Keymap.Bindings {
		if !known[name] {
			return fmt.Errorf("keymap action %q is not one of %s", name, strings.Join(KeymapActions(), ", "))
		}
		if strings.TrimSpace(spec) == "" {
			// An empty binding means "not bound", which is a legitimate thing to
			// want, so it is dropped rather than rejected.
			delete(cfg.Keymap.Bindings, name)
			continue
		}
	}

	return nil
}

func validateBars(bars []BarConfig) error {
	seen := make(map[string]int, len(bars))

	for i := range bars {
		edge := strings.ToLower(strings.TrimSpace(bars[i].Edge))
		if edge == "" {
			edge = "top"
		}
		if !validEdges[edge] {
			return fmt.Errorf("bars[%d].edge %q must be one of top, bottom, left, right", i, bars[i].Edge)
		}
		bars[i].Edge = edge

		if first, dup := seen[edge]; dup {
			return fmt.Errorf("bars[%d] duplicates the %q edge already used by bars[%d]; "+
				"at most one bar per screen edge is allowed", i, edge, first)
		}
		seen[edge] = i

		if bars[i].Size <= 0 {
			bars[i].Size = 40
		}
		if bars[i].Size > 400 {
			return fmt.Errorf("bars[%d].size %d is implausible; expected 1..400", i, bars[i].Size)
		}

		bars[i].Widget = strings.TrimSpace(bars[i].Widget)
		if bars[i].Widget == "" {
			bars[i].Widget = "bar"
		}
	}
	return nil
}

// Save writes config to %APPDATA%\NovaBar\config.toml.
func Save(cfg Config) error {
	path, err := GetConfigPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Write via a temp file in the same directory then rename, so a crash mid-write
	// cannot leave a truncated config that fails to parse on next launch.
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "config-*.toml.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Chmod(tmpName, 0644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// MarkFirstRunComplete flips the onboarding flag and persists it.
func MarkFirstRunComplete(cfg *Config) error {
	cfg.General.FirstRun = false
	return Save(*cfg)
}
