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
	Rules   []RuleConfig  `toml:"rules"`
}

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
}

// HotkeyConfig holds the global hotkey bindings as config strings, e.g. "Win+Space".
type HotkeyConfig struct {
	Rescue        string `toml:"rescue"`
	Launcher      string `toml:"launcher"`
	WorkspacePrev string `toml:"workspace_prev"`
	WorkspaceNext string `toml:"workspace_next"`
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
			FirstRun:    true,
			LogLevel:    "info",
			RescueKey:   "Ctrl+Alt+Shift+R",
			HideTaskbar: false,
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
			{Edge: "top", Size: 40, Enabled: true, Name: "main"},
		},
		Hotkeys: HotkeyConfig{
			Rescue:        "Ctrl+Alt+Shift+R",
			Launcher:      "Win+Space",
			WorkspacePrev: "Alt+Left",
			WorkspaceNext: "Alt+Right",
		},
		Rules: []RuleConfig{
			{Process: "Taskmgr.exe", Action: "float"},
			{Process: "explorer.exe", Class: "OperationStatusWindow", Action: "float"},
			{Process: "TextInputHost.exe", Action: "ignore"},
		},
	}
}

// GetConfigPath returns %APPDATA%\NovaBar\config.toml
func GetConfigPath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appData, "NovaBar", "config.toml"), nil
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

	if err := validateBars(cfg.Bars); err != nil {
		return err
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
