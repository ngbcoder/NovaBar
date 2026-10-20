package config

import (
	"fmt"
	"sort"
	"strings"
)

// This file describes the settings the shell is willing to edit from inside itself.
//
// # Why a schema rather than a hand-written UI
//
// Two things drift. The config struct grows a field and the settings page does not mention
// it; or the settings page offers a control the daemon no longer honours. Both look fine in
// review and are wrong on the user's machine.
//
// Declaring the editable surface once, with a getter and a setter per field, makes the RPC
// contract and the UI generated from the same list. A field that is not in this table is
// not editable from the shell, which is the safe direction: unknown settings stay in
// config.toml, where they are visible in a file the user is already editing.
//
// Deliberately absent: rules. They are a list of records with compound matching, which does
// not fit a generated form. Editing them stays a text-file job.

// FieldType is the control a settings page should render for a field.
type FieldType string

const (
	TypeToggle FieldType = "bool"
	TypeNumber FieldType = "int"
	TypeChoice FieldType = "enum"
	TypeText   FieldType = "text"
	TypeHotkey FieldType = "hotkey"
)

// Safety marks a field whose change could remove the user's way out.
//
// hide_taskbar and the rescue hotkey are what the watchdog recovers from. Editing them from
// inside the shell is editing the safety net, so the daemon refuses unless the rescue path
// is already proven working.
type Safety string

const (
	SafetyNone    Safety = ""
	SafetyProtect Safety = "protect"
)

// Field is one editable setting.
type Field struct {
	// Path is the dotted config path, e.g. "tiling.default_layout". It is the wire
	// format: the RPC layer keys changes by this and nothing else.
	Path string `json:"path"`
	// Label and Help are what the UI shows. Help is written for the user, not the
	// author, and explains consequences rather than restating the field name.
	Label string    `json:"label"`
	Help  string    `json:"help,omitempty"`
	Type  FieldType `json:"type"`
	// Options populates a choice control.
	Options []string `json:"options,omitempty"`
	// Min and Max bound a number control.
	Min int `json:"min,omitempty"`
	Max int `json:"max,omitempty"`
	// Safety is SafetyProtect for fields that guard the user.
	Safety Safety `json:"safety,omitempty"`

	// Get and Set are the single source of truth. The RPC layer and any future UI both
	// go through these, so a field cannot be rendered but not applied.
	Get func(*Config) any        `json:"-"`
	Set func(*Config, any) error `json:"-"`
}

// barField builds fields addressing one entry of the bars slice.
//
// The AppBar contract is at most one bar per edge, so the settings surface offers one
// control per edge rather than a list. That is what a user actually wants to decide: which
// edges have a bar, and how thick. It also sidesteps the duplicate-edge validation error
// by construction, since there is never a second entry for the same edge.
func barField(edge string, get func(*Config) (BarConfig, bool), set func(*Config, BarConfig)) []Field {
	enabled := Field{
		Path:  "bars." + edge + ".enabled",
		Label: strings.ToUpper(edge[:1]) + edge[1:] + " bar",
		Help:  "Reserve space for a bar on the " + edge + " edge.",
		Type:  TypeToggle,
		Get: func(c *Config) any {
			b, ok := get(c)
			return ok && b.Enabled
		},
		Set: func(c *Config, v any) error {
			b, _ := get(c)
			b.Edge = edge
			b.Enabled = v.(bool)
			if b.Size <= 0 {
				b.Size = 40
			}
			set(c, b)
			return nil
		},
	}

	size := Field{
		Path:  "bars." + edge + ".size",
		Label: strings.ToUpper(edge[:1]) + edge[1:] + " bar size",
		Help:  "Reserved thickness in pixels.",
		Type:  TypeNumber,
		Min:   16,
		Max:   400,
		Get: func(c *Config) any {
			b, ok := get(c)
			if !ok || b.Size <= 0 {
				return 0
			}
			return b.Size
		},
		Set: func(c *Config, v any) error {
			b, _ := get(c)
			b.Edge = edge
			b.Size = v.(int)
			b.Enabled = true // setting a size implies wanting the bar
			set(c, b)
			return nil
		},
	}

	// Size is meaningless with no bar, so it is only offered when enabled.
	enabled.Help += " The size control appears when this is on."

	return []Field{enabled, size}
}

// findBar returns the bar on an edge and whether it exists.
func findBar(c *Config, edge string) (BarConfig, bool) {
	for _, b := range c.Bars {
		if strings.EqualFold(b.Edge, edge) {
			return b, true
		}
	}
	return BarConfig{}, false
}

// setBar replaces the bar on an edge, or appends it.
//
// Replacing rather than appending on a second write is what keeps one-bar-per-edge true
// even though the schema addresses bars by edge.
func setBar(c *Config, edge string, b BarConfig) {
	b.Edge = edge
	for i, existing := range c.Bars {
		if strings.EqualFold(existing.Edge, edge) {
			c.Bars[i] = b
			return
		}
	}
	c.Bars = append(c.Bars, b)
}

// layoutOptions is the layout list, sorted for a stable UI order.
func layoutOptions() []string {
	out := make([]string, 0, len(validLayouts))
	for k := range validLayouts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Schema returns the editable settings surface.
//
// Ordering is deliberate: the things a user is most likely to change come first, and the
// protected fields sit at the end where they are harder to reach by accident.
func Schema() []Field {
	fields := []Field{
		{
			Path:  "dock.enabled",
			Label: "Dock",
			Help:  "Show the floating dock at the bottom of the screen. It sits over the desktop and reserves no space.",
			Type:  TypeToggle,
			Get:   func(c *Config) any { return c.Dock.Enabled },
			Set: func(c *Config, v any) error {
				c.Dock.Enabled = v.(bool)
				return nil
			},
		},
		{
			Path:  "dock.size",
			Label: "Dock icon size",
			Type:  TypeNumber,
			Min:   28,
			Max:   112,
			Get:   func(c *Config) any { return c.Dock.Size },
			Set: func(c *Config, v any) error {
				c.Dock.Size = v.(int)
				return nil
			},
		},
		{
			Path:  "tiling.enabled",
			Label: "Tile windows",
			Help:  "Arrange windows automatically as they open and move.",
			Type:  TypeToggle,
			Get:   func(c *Config) any { return c.Tiling.Enabled },
			Set: func(c *Config, v any) error {
				c.Tiling.Enabled = v.(bool)
				return nil
			},
		},
		{
			Path:    "tiling.default_layout",
			Label:   "Default layout",
			Help:    "How new windows are arranged.",
			Type:    TypeChoice,
			Options: layoutOptions(),
			Get:     func(c *Config) any { return c.Tiling.DefaultLayout },
			Set: func(c *Config, v any) error {
				s := strings.ToLower(strings.TrimSpace(fmt.Sprint(v)))
				if !validLayouts[s] {
					return fmt.Errorf("%q is not one of %s", s, strings.Join(layoutOptions(), ", "))
				}
				c.Tiling.DefaultLayout = s
				return nil
			},
		},
		{
			Path:  "tiling.inner_gap",
			Label: "Gap between windows",
			Type:  TypeNumber,
			Min:   0,
			Max:   200,
			Get:   func(c *Config) any { return c.Tiling.InnerGap },
			Set: func(c *Config, v any) error {
				c.Tiling.InnerGap = v.(int)
				return nil
			},
		},
		{
			Path:  "tiling.outer_gap",
			Label: "Gap at screen edges",
			Type:  TypeNumber,
			Min:   0,
			Max:   200,
			Get:   func(c *Config) any { return c.Tiling.OuterGap },
			Set: func(c *Config, v any) error {
				c.Tiling.OuterGap = v.(int)
				return nil
			},
		},
		{
			Path:  "hotkeys.launcher",
			Label: "Open launcher",
			Type:  TypeHotkey,
			Get:   func(c *Config) any { return c.Hotkeys.Launcher },
			Set: func(c *Config, v any) error {
				c.Hotkeys.Launcher = strings.TrimSpace(fmt.Sprint(v))
				return nil
			},
		},
		{
			Path:  "hotkeys.settings",
			Label: "Open settings",
			Type:  TypeHotkey,
			Get:   func(c *Config) any { return c.Hotkeys.Settings },
			Set: func(c *Config, v any) error {
				c.Hotkeys.Settings = strings.TrimSpace(fmt.Sprint(v))
				return nil
			},
		},
		{
			Path:  "hotkeys.workspace_prev",
			Label: "Previous workspace",
			Type:  TypeHotkey,
			Get:   func(c *Config) any { return c.Hotkeys.WorkspacePrev },
			Set: func(c *Config, v any) error {
				c.Hotkeys.WorkspacePrev = strings.TrimSpace(fmt.Sprint(v))
				return nil
			},
		},
		{
			Path:  "hotkeys.workspace_next",
			Label: "Next workspace",
			Type:  TypeHotkey,
			Get:   func(c *Config) any { return c.Hotkeys.WorkspaceNext },
			Set: func(c *Config, v any) error {
				c.Hotkeys.WorkspaceNext = strings.TrimSpace(fmt.Sprint(v))
				return nil
			},
		},
		{
			Path:  "general.log_level",
			Label: "Log level",
			Help:  "debug records a great deal and slows the shell.",
			Type:  TypeChoice,
			Options: []string{
				"error", "warn", "info", "debug",
			},
			Get: func(c *Config) any { return c.General.LogLevel },
			Set: func(c *Config, v any) error {
				c.General.LogLevel = strings.ToLower(strings.TrimSpace(fmt.Sprint(v)))
				return nil
			},
		},
	}

	for _, edge := range []string{"top", "bottom", "left", "right"} {
		e := edge
		fields = append(fields, barField(e,
			func(c *Config) (BarConfig, bool) { return findBar(c, e) },
			func(c *Config, b BarConfig) { setBar(c, e, b) },
		)...)
	}

	// Protected last, and marked. The daemon refuses these unless the rescue hotkey is
	// currently bound and working.
	fields = append(fields,
		Field{
			Path:   "general.hide_taskbar",
			Label:  "Hide the Windows taskbar",
			Help:   "Removes the native taskbar. NovaBar's bars replace it, and the rescue hotkey restores it.",
			Type:   TypeToggle,
			Safety: SafetyProtect,
			Get:    func(c *Config) any { return c.General.HideTaskbar },
			Set: func(c *Config, v any) error {
				c.General.HideTaskbar = v.(bool)
				return nil
			},
		},
		Field{
			Path:  "general.bar_scope",
			Label: "Bar width",
			Help:  "Whether the bars span every display or stay on the primary one.",
			Type:  TypeChoice,
			Options: []string{
				BarScopeDesktop,
				BarScopePrimary,
			},
			Get: func(c *Config) any { return c.General.BarScope },
			Set: func(c *Config, v any) error {
				s := strings.ToLower(strings.TrimSpace(fmt.Sprint(v)))
				if s != BarScopeDesktop && s != BarScopePrimary {
					return fmt.Errorf("%q is not one of %s, %s", s, BarScopeDesktop, BarScopePrimary)
				}
				c.General.BarScope = s
				return nil
			},
		},
		Field{
			Path:   "hotkeys.rescue",
			Label:  "Rescue hotkey",
			Help:   "Restores the taskbar and exits NovaBar. Changing this changes how you get out.",
			Type:   TypeHotkey,
			Safety: SafetyProtect,
			Get:    func(c *Config) any { return c.Hotkeys.Rescue },
			Set: func(c *Config, v any) error {
				c.Hotkeys.Rescue = strings.TrimSpace(fmt.Sprint(v))
				// Kept in sync so the two cannot disagree about how to get out.
				c.General.RescueKey = c.Hotkeys.Rescue
				return nil
			},
		},
	)

	return fields
}

// FieldByPath finds a schema field.
func FieldByPath(path string) (Field, bool) {
	for _, f := range Schema() {
		if f.Path == path {
			return f, true
		}
	}
	return Field{}, false
}

// Values reads the current value of every schema field.
func Values(c *Config) map[string]any {
	out := make(map[string]any, len(Schema()))
	for _, f := range Schema() {
		out[f.Path] = f.Get(c)
	}
	return out
}

// Apply sets one field and revalidates the whole document.
//
// Revalidating is the point: the shell's live-reload watcher already treats an invalid
// config as "keep what you had", so returning the error here means a bad edit is rejected
// with a reason instead of being persisted and taking effect at the next launch.
func Apply(c *Config, path string, value any) error {
	f, ok := FieldByPath(path)
	if !ok {
		return fmt.Errorf("%q is not an editable setting", path)
	}

	coerced, err := coerce(f, value)
	if err != nil {
		return err
	}
	if err := f.Set(c, coerced); err != nil {
		return err
	}
	return Validate(c)
}

// coerce turns a wire value into the type the field's setter expects.
//
// JSON numbers arrive as float64, so a number control would otherwise reach an int setter
// as 8.0 and panic on the type assertion. Rejecting out-of-range values here also means the
// bounds in the schema are enforced rather than advisory.
func coerce(f Field, value any) (any, error) {
	switch f.Type {
	case TypeToggle:
		b, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%s expects true or false, got %v", f.Path, value)
		}
		return b, nil

	case TypeNumber:
		var n int
		switch v := value.(type) {
		case float64:
			if v != float64(int(v)) {
				return nil, fmt.Errorf("%s expects a whole number, got %v", f.Path, v)
			}
			n = int(v)
		case int:
			n = v
		default:
			return nil, fmt.Errorf("%s expects a number, got %v", f.Path, value)
		}
		if f.Max > 0 && (n < f.Min || n > f.Max) {
			return nil, fmt.Errorf("%s must be between %d and %d, got %d", f.Path, f.Min, f.Max, n)
		}
		if n < 0 {
			return nil, fmt.Errorf("%s cannot be negative", f.Path)
		}
		return n, nil

	case TypeChoice:
		s := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		for _, opt := range f.Options {
			if opt == s {
				return s, nil
			}
		}
		return nil, fmt.Errorf("%s must be one of %s, got %q", f.Path, strings.Join(f.Options, ", "), s)

	default: // text, hotkey
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s expects a string, got %v", f.Path, value)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("%s cannot be empty", f.Path)
		}
		if len(s) > 128 {
			return nil, fmt.Errorf("%s is too long", f.Path)
		}
		return s, nil
	}
}

// ProtectedPaths lists the fields the daemon gates on a working rescue path.
func ProtectedPaths() []string {
	var out []string
	for _, f := range Schema() {
		if f.Safety == SafetyProtect {
			out = append(out, f.Path)
		}
	}
	sort.Strings(out)
	return out
}
