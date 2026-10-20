package config

import (
	"github.com/ngbcoder/NovaBar/internal/win"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// tomlBytes marshals a config to TOML.
func tomlBytes(cfg Config) ([]byte, error) {
	return toml.Marshal(cfg)
}

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := DefaultConfig()
	if err := Validate(&cfg); err != nil {
		t.Fatalf("DefaultConfig must validate, got: %v", err)
	}
}

// TestDefaultHideTaskbarIsSafe is the safety pin.
//
// hide_taskbar now defaults to true, because the floating dock is this shell's taskbar
// and leaving the native one visible underneath means showing two. The pin therefore no
// longer asserts the flag itself; it asserts the three things that make the flag safe, so
// that flipping the default back, or weakening the escape route, fails a test rather than
// shipping.
func TestDefaultHideTaskbarIsSafe(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.General.HideTaskbar {
		t.Error("hide_taskbar should default to true: the dock is the taskbar")
	}

	// 1. The dock has to be there, or hiding the native taskbar strands the user.
	if !cfg.Dock.Enabled {
		t.Error("the dock must be enabled whenever the native taskbar is hidden, " +
			"or there is no taskbar at all")
	}

	// 2. There must be a way out that the shell binds at startup.
	if cfg.Hotkeys.Rescue == "" {
		t.Error("no rescue hotkey configured; hiding the taskbar would strand the user")
	}
	if _, err := win.ParseHotkeySpec(cfg.Hotkeys.Rescue); err != nil {
		t.Errorf("the default rescue hotkey %q does not parse: %v", cfg.Hotkeys.Rescue, err)
	}

	// 3. Both settings that guard the user must be gated in the settings surface, so the
	// shell cannot be talked out of its own escape route.
	protected := map[string]bool{}
	for _, p := range ProtectedPaths() {
		protected[p] = true
	}
	for _, p := range []string{"general.hide_taskbar", "hotkeys.rescue"} {
		if !protected[p] {
			t.Errorf("%s is not marked SafetyProtect; it must be gated on a working rescue path", p)
		}
	}
}

// TestSafeConfigStillRestoresTheTaskbar: the unreadable-config fallback must leave the
// native taskbar alone, whatever the default is. A broken config means the user's intent
// is unknown, and the safe response is to hand them back the taskbar they can use.
func TestSafeConfigStillRestoresTheTaskbar(t *testing.T) {
	safe := SafeConfig()
	if safe.General.HideTaskbar {
		t.Error("SafeConfig must not hide the native taskbar")
	}
	if len(safe.Bars) != 0 {
		t.Error("SafeConfig must not reserve any bars")
	}
	if safe.Tiling.Enabled {
		t.Error("SafeConfig must not tile")
	}
}

// TestDefaultRescueHotkeyIsValid ensures the key onboarding tells the user to press
// actually parses, and that the duplicated field has not drifted.
func TestDefaultRescueHotkeyIsValid(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.General.RescueKey != cfg.Hotkeys.Rescue {
		t.Errorf("rescue hotkey is duplicated and has drifted: general=%q hotkeys=%q",
			cfg.General.RescueKey, cfg.Hotkeys.Rescue)
	}
	if cfg.Hotkeys.Rescue == "" {
		t.Error("a rescue hotkey must always be configured")
	}
}

// TestDefaultHasOneTopBar documents the out-of-box shape.
func TestDefaultHasOneTopBar(t *testing.T) {
	bars := DefaultConfig().Bars
	if len(bars) != 1 {
		t.Fatalf("expected exactly one default bar, got %d", len(bars))
	}
	if bars[0].Edge != "top" || !bars[0].Enabled || bars[0].Size != 40 {
		t.Errorf("unexpected default bar: %+v", bars[0])
	}
}

func TestValidateRejectsBadLayout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Tiling.DefaultLayout = "spiral"
	if err := Validate(&cfg); err == nil {
		t.Error("expected an error for an unknown layout")
	}
}

// TestValidateRejectsBadBarScope checks the bar scope.
//
// The scope decides how far the bars reach, and a value that is neither of the two
// would silently mean "primary" and leave the user wondering why the setting does
// nothing.
func TestValidateRejectsBadBarScope(t *testing.T) {
	cfg := DefaultConfig()
	cfg.General.BarScope = "everywhere"
	if err := Validate(&cfg); err == nil {
		t.Error("expected an error for an unknown bar scope")
	}
}

// TestValidateNormalizesBarScopeCase checks that the scope is accepted in any case
// and stored in the canonical one.
func TestValidateNormalizesBarScopeCase(t *testing.T) {
	cfg := DefaultConfig()
	cfg.General.BarScope = "Primary"
	if err := Validate(&cfg); err != nil {
		t.Fatalf("bar scope casing should normalize, got: %v", err)
	}
	if cfg.General.BarScope != BarScopePrimary {
		t.Errorf("bar scope = %q, want %q", cfg.General.BarScope, BarScopePrimary)
	}
}

// TestDefaultBarScopeIsPrimary pins the default.
//
// A bar that reaches the whole desktop is drawn across every display, but the shell
// reserves the work-area strip only for the display the bar's rectangle starts on. The
// second display's windows can then slide under the bar that is drawn over them, so
// the primary display is the safe default.
func TestDefaultBarScopeIsPrimary(t *testing.T) {
	if got := DefaultConfig().General.BarScope; got != BarScopePrimary {
		t.Errorf("the default bar scope is %q, want %q", got, BarScopePrimary)
	}
}

func TestValidateNormalizesLayoutCase(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Tiling.DefaultLayout = "BSP"
	if err := Validate(&cfg); err != nil {
		t.Fatalf("layout casing should normalize, got: %v", err)
	}
	if cfg.Tiling.DefaultLayout != "bsp" {
		t.Errorf("layout = %q, want %q", cfg.Tiling.DefaultLayout, "bsp")
	}
}

func TestValidateClampsGaps(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Tiling.InnerGap = -5
	cfg.Tiling.OuterGap = 99999
	if err := Validate(&cfg); err != nil {
		t.Fatalf("out-of-range gaps should clamp, not error: %v", err)
	}
	if cfg.Tiling.InnerGap != 0 {
		t.Errorf("InnerGap = %d, want 0", cfg.Tiling.InnerGap)
	}
	if cfg.Tiling.OuterGap != 200 {
		t.Errorf("OuterGap = %d, want the 200 cap", cfg.Tiling.OuterGap)
	}
}

func TestValidateRejectsBadRuleAction(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []RuleConfig{{Process: "x.exe", Action: "explode"}}
	if err := Validate(&cfg); err == nil {
		t.Error("expected an error for an unknown rule action")
	}
}

// --- bars ---

func TestValidateAcceptsOneBarPerEdge(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Bars = []BarConfig{
		{Edge: "top", Size: 40, Enabled: true, Name: "main"},
		{Edge: "bottom", Size: 6, Enabled: true, Name: "status"},
		{Edge: "left", Size: 60, Enabled: false, Name: "dock"},
		{Edge: "right", Size: 8, Enabled: false, Name: "edge"},
	}
	if err := Validate(&cfg); err != nil {
		t.Fatalf("four bars, one per edge, must be valid: %v", err)
	}
}

// TestValidateRejectsDuplicateEdge is the important multi-bar rule. Two bars claiming
// the same edge would each shrink the work area from the same side, leaving a gap twice
// the intended size.
func TestValidateRejectsDuplicateEdge(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Bars = []BarConfig{
		{Edge: "top", Size: 40, Enabled: true},
		{Edge: "top", Size: 24, Enabled: false},
	}
	err := Validate(&cfg)
	if err == nil {
		t.Fatal("expected an error for two bars on the same edge")
	}
	if !strings.Contains(err.Error(), "top") {
		t.Errorf("error should name the offending edge, got: %v", err)
	}
}

func TestValidateRejectsBadEdge(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Bars = []BarConfig{{Edge: "diagonal", Size: 40, Enabled: true}}
	if err := Validate(&cfg); err == nil {
		t.Error("expected an error for an unknown edge")
	}
}

func TestValidateNormalizesBarEdgeCase(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Bars = []BarConfig{{Edge: "TOP", Size: 40, Enabled: true}}
	if err := Validate(&cfg); err != nil {
		t.Fatalf("edge casing should normalize: %v", err)
	}
	if cfg.Bars[0].Edge != "top" {
		t.Errorf("edge = %q, want %q", cfg.Bars[0].Edge, "top")
	}
}

func TestValidateDefaultsEmptyEdgeAndSize(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Bars = []BarConfig{{Enabled: true}}
	if err := Validate(&cfg); err != nil {
		t.Fatalf("empty edge and size should get defaults: %v", err)
	}
	if cfg.Bars[0].Edge != "top" {
		t.Errorf("edge = %q, want the default %q", cfg.Bars[0].Edge, "top")
	}
	if cfg.Bars[0].Size != 40 {
		t.Errorf("size = %d, want the default 40", cfg.Bars[0].Size)
	}
}

func TestValidateRejectsAbsurdBarSize(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Bars = []BarConfig{{Edge: "top", Size: 5000, Enabled: true}}
	if err := Validate(&cfg); err == nil {
		t.Error("expected an error for an implausible bar size")
	}
}

func TestValidateAllowsNoBars(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Bars = nil
	if err := Validate(&cfg); err != nil {
		t.Errorf("zero bars must be valid (a shell with no bars is still coherent): %v", err)
	}
}

// --- parse / load ---

func TestParseRoundTrip(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	original := DefaultConfig()
	original.Tiling.DefaultLayout = "grid"
	original.Tiling.InnerGap = 12
	original.General.HideTaskbar = true
	original.Bars = []BarConfig{
		// Widget is set explicitly because validation defaults an empty one to "bar",
		// so a fixture leaving it blank would not round-trip byte-identically.
		{Edge: "top", Size: 44, Enabled: true, Name: "main", Widget: "bar", Transparent: true},
		{Edge: "right", Size: 6, Enabled: true, Name: "edge", Widget: "dock"},
	}
	original.Hotkeys.Launcher = "Win+Space"

	if err := Save(original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Tiling != original.Tiling {
		t.Errorf("tiling mismatch: got %+v want %+v", loaded.Tiling, original.Tiling)
	}
	if loaded.General.HideTaskbar != original.General.HideTaskbar {
		t.Errorf("hide_taskbar = %v, want %v", loaded.General.HideTaskbar, original.General.HideTaskbar)
	}
	if loaded.Hotkeys != original.Hotkeys {
		t.Errorf("hotkeys mismatch: got %+v want %+v", loaded.Hotkeys, original.Hotkeys)
	}
	if len(loaded.Bars) != len(original.Bars) {
		t.Fatalf("bar count: got %d want %d", len(loaded.Bars), len(original.Bars))
	}
	for i := range original.Bars {
		if loaded.Bars[i] != original.Bars[i] {
			t.Errorf("bar %d: got %+v want %+v", i, loaded.Bars[i], original.Bars[i])
		}
	}
}

func TestLoadCreatesDefaultOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.General.FirstRun {
		t.Error("a freshly created config must have first_run = true")
	}

	path := filepath.Join(dir, "NovaBar", "config.toml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Load must write a default config: %v", err)
	}
}

// TestParseParsesMultipleBars covers the [[bars]] array form in a real document.
func TestParseParsesMultipleBars(t *testing.T) {
	data := []byte(`
[[bars]]
edge = "top"
size = 44
enabled = true
name = "main"

[[bars]]
edge = "bottom"
size = 6
enabled = true
name = "status"

[[bars]]
edge = "left"
size = 64
enabled = false
name = "dock"
`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Bars) != 3 {
		t.Fatalf("expected 3 bars, got %d", len(cfg.Bars))
	}
	if cfg.Bars[1].Edge != "bottom" || cfg.Bars[1].Size != 6 || !cfg.Bars[1].Enabled {
		t.Errorf("second bar = %+v", cfg.Bars[1])
	}
	if cfg.Bars[2].Enabled {
		t.Error("third bar should have stayed disabled")
	}
}

func TestParseRejectsMalformedTOML(t *testing.T) {
	if _, err := Parse([]byte("[tiling\ndefault_layout = ")); err == nil {
		t.Error("expected a parse error for malformed TOML")
	}
}

func TestParseRejectsInvalidValues(t *testing.T) {
	data := []byte("[tiling]\ndefault_layout = \"diagonal\"\n")
	if _, err := Parse(data); err == nil {
		t.Error("expected a validation error for an unknown layout in the document")
	}
}

// TestParseRejectsDuplicateBarsInDocument covers validation running on the parse path.
func TestParseRejectsDuplicateBarsInDocument(t *testing.T) {
	data := []byte(`
[[bars]]
edge = "top"
size = 40
enabled = true

[[bars]]
edge = "top"
size = 20
enabled = false
`)
	if _, err := Parse(data); err == nil {
		t.Error("expected a validation error for two bars on the top edge")
	}
}

func TestParseIgnoresUnknownKeys(t *testing.T) {
	data := []byte(`
[general]
log_level = "debug"
future_option = "ignored"

[tiling]
enabled = true
default_layout = "columns"
inner_gap = 4
outer_gap = 4
`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("unknown keys must not break parsing: %v", err)
	}
	if cfg.Tiling.DefaultLayout != "columns" {
		t.Errorf("layout = %q, want %q", cfg.Tiling.DefaultLayout, "columns")
	}
	if cfg.General.LogLevel != "debug" {
		t.Errorf("log_level = %q, want %q", cfg.General.LogLevel, "debug")
	}
}

func TestPartialConfigKeepsDefaults(t *testing.T) {
	cfg, err := Parse([]byte("[[bars]]\nsize = 72\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Bars[0].Size != 72 {
		t.Errorf("size = %d, want 72", cfg.Bars[0].Size)
	}
	if cfg.Bars[0].Edge != "top" {
		t.Errorf("edge = %q, want the default %q", cfg.Bars[0].Edge, "top")
	}
	if cfg.Tiling.DefaultLayout != "bsp" {
		t.Errorf("layout = %q, want the default %q", cfg.Tiling.DefaultLayout, "bsp")
	}
}

// TestParseToleratesUTF8BOM covers the editor behaviour that broke the first live run:
// PowerShell 5.1's Set-Content and Notepad both write a UTF-8 BOM, and go-toml rejects
// it with "invalid character at start of key".
func TestParseToleratesUTF8BOM(t *testing.T) {
	body := []byte("[tiling]\ndefault_layout = \"columns\"\n")
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, body...)

	cfg, err := Parse(withBOM)
	if err != nil {
		t.Fatalf("a UTF-8 BOM must be tolerated: %v", err)
	}
	if cfg.Tiling.DefaultLayout != "columns" {
		t.Errorf("layout = %q, want %q", cfg.Tiling.DefaultLayout, "columns")
	}
}

// TestSafeConfigIsInert pins the fallback used when a config cannot be read. A desktop
// shell must not interpret "I could not read your config" as permission to take over
// the screen.
func TestSafeConfigIsInert(t *testing.T) {
	cfg := SafeConfig()

	if len(cfg.Bars) != 0 {
		t.Errorf("SafeConfig must not enable any bar, got %+v", cfg.Bars)
	}
	if cfg.General.HideTaskbar {
		t.Error("SafeConfig must never hide the taskbar")
	}
	if cfg.Tiling.Enabled {
		t.Error("SafeConfig must not take over window layout")
	}
	// Recovery paths must stay available: that is what protects the user.
	if cfg.Hotkeys.Rescue == "" {
		t.Error("SafeConfig must keep a rescue hotkey configured")
	}
	if err := Validate(&cfg); err != nil {
		t.Errorf("SafeConfig must be valid: %v", err)
	}
}

func TestLoadFallsBackToSafeConfigOnBrokenFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	path := filepath.Join(dir, "NovaBar", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("[tiling\nbroken"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := Load()
	if err == nil {
		t.Fatal("expected Load to report the parse error")
	}
	if len(cfg.Bars) != 0 || cfg.General.HideTaskbar || cfg.Tiling.Enabled {
		t.Errorf("a broken config must yield an inert shell, got %+v", cfg)
	}
}

// TestConfigContainsNoSecrets documents that config.get over RPC is safe to expose in
// full: nothing here is a credential.
func TestConfigContainsNoSecrets(t *testing.T) {
	data, err := tomlBytes(DefaultConfig())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	lower := strings.ToLower(string(data))
	for _, forbidden := range []string{"password", "token", "secret", "apikey", "api_key"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("default config must not contain %q", forbidden)
		}
	}
}
