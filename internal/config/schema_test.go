package config

import (
	"strings"
	"testing"
)

// TestSchemaFieldsAreUsable is the invariant that makes the generated UI trustworthy: every
// declared field can be read and written. A field whose getter or setter is wrong would
// otherwise render as a control that silently does nothing.
func TestSchemaFieldsAreUsable(t *testing.T) {
	cfg := DefaultConfig()
	for _, f := range Schema() {
		if f.Get == nil || f.Set == nil {
			t.Errorf("%s has a nil getter or setter", f.Path)
			continue
		}
		if f.Get(&cfg) == nil {
			t.Errorf("%s has no current value", f.Path)
		}
		if f.Label == "" {
			t.Errorf("%s has no label", f.Path)
		}
	}
}

// TestSchemaPathsAreUnique: two fields on one path would make Apply ambiguous and a
// generated UI render duplicate controls.
func TestSchemaPathsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Schema() {
		if seen[f.Path] {
			t.Errorf("duplicate schema path %q", f.Path)
		}
		seen[f.Path] = true
	}
}

// TestApplyRejectsBadValues covers the path from a widget to a config file. Each of these
// would otherwise persist something the daemon later rejects at an awkward moment.
func TestApplyRejectsBadValues(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		value any
	}{
		{"unknown path", "not.a.setting", true},
		{"wrong type for toggle", "tiling.enabled", "yes"},
		{"fraction for number", "tiling.inner_gap", 8.5},
		{"negative number", "tiling.inner_gap", -1},
		{"number above max", "tiling.inner_gap", 5000},
		{"unknown layout", "tiling.default_layout", "spiral"},
		{"unknown log level", "general.log_level", "shout"},
		{"empty hotkey", "hotkeys.launcher", ""},
		{"number for text", "hotkeys.launcher", 42},
		{"overlong hotkey", "hotkeys.launcher", strings.Repeat("a", 200)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			err := Apply(&cfg, tc.path, tc.value)
			if err == nil {
				t.Fatalf("Apply(%q, %v) was accepted; it must be rejected", tc.path, tc.value)
			}
			// The message has to name the problem, or a user is told only "failed".
			if !strings.Contains(err.Error(), tc.path) && !strings.Contains(err.Error(), "expects") {
				t.Errorf("error %q does not explain which setting failed", err)
			}
		})
	}
}

// TestApplyAcceptsValidValues walks the ordinary path.
func TestApplyAcceptsValidValues(t *testing.T) {
	cfg := DefaultConfig()

	valid := []struct {
		path  string
		value any
		check func(*Config) bool
	}{
		{"tiling.enabled", false, func(c *Config) bool { return !c.Tiling.Enabled }},
		{"tiling.default_layout", "monocle", func(c *Config) bool { return c.Tiling.DefaultLayout == "monocle" }},
		// JSON numbers arrive as float64; this must not panic on an int assertion.
		{"tiling.inner_gap", float64(16), func(c *Config) bool { return c.Tiling.InnerGap == 16 }},
		{"tiling.outer_gap", 4.0, func(c *Config) bool { return c.Tiling.OuterGap == 4 }},
		{"hotkeys.launcher", "Ctrl+Alt+L", func(c *Config) bool { return c.Hotkeys.Launcher == "Ctrl+Alt+L" }},
		{"general.log_level", "debug", func(c *Config) bool { return c.General.LogLevel == "debug" }},
	}

	for _, v := range valid {
		if err := Apply(&cfg, v.path, v.value); err != nil {
			t.Errorf("Apply(%q, %v) = %v, want success", v.path, v.value, err)
			continue
		}
		if !v.check(&cfg) {
			t.Errorf("Apply(%q, %v) did not take effect", v.path, v.value)
		}
	}
}

// TestBarFieldsKeepOnePerEdge covers the addressing scheme.
//
// The schema offers one control per edge rather than a list, so a second write for the same
// edge must replace the entry rather than append and trip the duplicate-edge validation.
func TestBarFieldsKeepOnePerEdge(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Bars = nil

	if err := Apply(&cfg, "bars.top.enabled", true); err != nil {
		t.Fatalf("enabling the top bar: %v", err)
	}
	if err := Apply(&cfg, "bars.left.enabled", true); err != nil {
		t.Fatalf("enabling the left bar: %v", err)
	}
	if len(cfg.Bars) != 2 {
		t.Fatalf("bars = %d, want 2", len(cfg.Bars))
	}

	// Re-writing the top bar must replace, not append.
	if err := Apply(&cfg, "bars.top.size", 60); err != nil {
		t.Fatalf("resizing the top bar: %v", err)
	}
	if len(cfg.Bars) != 2 {
		t.Errorf("bars = %d after resizing; a second entry was appended for the same edge", len(cfg.Bars))
	}

	top, ok := findBar(&cfg, "top")
	if !ok || top.Size != 60 {
		t.Errorf("top bar = %+v, want size 60", top)
	}
	if !top.Enabled {
		t.Error("setting a size left the bar disabled; a size implies wanting the bar")
	}
}

// TestDisablingABarRemovesIt is a real question: does turning a bar off delete the entry or
// leave it disabled? Leaving it disabled is better, because re-enabling restores the size
// the user chose.
func TestDisablingABarKeepsItsSize(t *testing.T) {
	cfg := DefaultConfig()

	if err := Apply(&cfg, "bars.top.size", 72); err != nil {
		t.Fatal(err)
	}
	if err := Apply(&cfg, "bars.top.enabled", false); err != nil {
		t.Fatal(err)
	}
	if err := Apply(&cfg, "bars.top.enabled", true); err != nil {
		t.Fatal(err)
	}

	top, ok := findBar(&cfg, "top")
	if !ok {
		t.Fatal("the top bar entry disappeared; its size would be lost")
	}
	if top.Size != 72 {
		t.Errorf("size = %d after a disable and re-enable, want 72", top.Size)
	}
}

// TestRescueHotkeyStaysInSync: the config carries the rescue key twice, and if those
// disagree then one of them is wrong about how the user gets out.
func TestRescueHotkeyStaysInSync(t *testing.T) {
	cfg := DefaultConfig()
	if err := Apply(&cfg, "hotkeys.rescue", "Ctrl+Alt+Shift+F9"); err != nil {
		t.Fatal(err)
	}
	if cfg.Hotkeys.Rescue != "Ctrl+Alt+Shift+F9" {
		t.Errorf("hotkeys.rescue = %q", cfg.Hotkeys.Rescue)
	}
	if cfg.General.RescueKey != cfg.Hotkeys.Rescue {
		t.Errorf("general.rescue_hotkey = %q but hotkeys.rescue = %q; they must agree",
			cfg.General.RescueKey, cfg.Hotkeys.Rescue)
	}
}

// TestProtectedFieldsAreMarked pins which settings the daemon gates.
//
// hide_taskbar and the rescue hotkey are what the watchdog recovers from. If either loses
// its marker, the shell will let a user edit its own escape route with no working check.
func TestProtectedFieldsAreMarked(t *testing.T) {
	paths := ProtectedPaths()

	want := map[string]bool{
		"general.hide_taskbar": false,
		"hotkeys.rescue":       false,
	}
	for _, p := range paths {
		if _, ok := want[p]; ok {
			want[p] = true
		}
	}
	for p, found := range want {
		if !found {
			t.Errorf("%s should be marked SafetyProtect", p)
		}
	}

	// Nothing else should be gated, or the settings page becomes needlessly timid.
	for _, f := range Schema() {
		if f.Safety != SafetyProtect {
			continue
		}
		if _, ok := want[f.Path]; !ok {
			t.Errorf("%s is marked SafetyProtect but is not one of the two settings that guard the user", f.Path)
		}
	}
}

// TestApplyRevalidatesTheDocument: a change is only accepted if the result is a valid
// config, because the shell's watcher treats an invalid file as "keep what you had".
func TestApplyRevalidatesTheDocument(t *testing.T) {
	cfg := DefaultConfig()
	// A pre-existing duplicate edge would make any edit fail revalidation, which is
	// correct: the document as a whole is broken.
	cfg.Bars = append(cfg.Bars, BarConfig{Edge: "top", Size: 40})

	if err := Apply(&cfg, "tiling.inner_gap", 10); err == nil {
		t.Error("Apply accepted an edit to a config that is already invalid")
	}
}

// TestValuesCoversEveryField keeps the wire payload and the schema in step.
func TestValuesCoversEveryField(t *testing.T) {
	cfg := DefaultConfig()
	values := Values(&cfg)

	for _, f := range Schema() {
		if _, ok := values[f.Path]; !ok {
			t.Errorf("Values() is missing %q", f.Path)
		}
	}
	if len(values) != len(Schema()) {
		t.Errorf("Values() returned %d entries for %d fields", len(values), len(Schema()))
	}
}

// TestLayoutOptionsAreSorted keeps a generated choice control from reshuffling itself
// between calls.
func TestLayoutOptionsAreSorted(t *testing.T) {
	f, ok := FieldByPath("tiling.default_layout")
	if !ok {
		t.Fatal("no default_layout field")
	}
	for i := 1; i < len(f.Options); i++ {
		if f.Options[i-1] > f.Options[i] {
			t.Errorf("options are not sorted: %v", f.Options)
			break
		}
	}
}
