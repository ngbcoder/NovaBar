package config

import (
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

// TestDefaultHideTaskbarIsOff pins the safety-critical default. If this ever flips on,
// a fresh install hides the taskbar before the user has seen the rescue instructions.
func TestDefaultHideTaskbarIsOff(t *testing.T) {
	if DefaultConfig().Bar.HideTaskbar {
		t.Error("hide_taskbar must default to false")
	}
}

// TestDefaultRescueHotkeyIsValid ensures the key onboarding tells the user to press
// actually parses.
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

func TestValidateRejectsBadLayout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Tiling.DefaultLayout = "spiral"
	if err := Validate(&cfg); err == nil {
		t.Error("expected an error for an unknown layout")
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

func TestValidateRejectsBadBarPosition(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Bar.Position = "left"
	if err := Validate(&cfg); err == nil {
		t.Error("expected an error for an unknown bar position")
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

func TestParseRoundTrip(t *testing.T) {
	// Isolate from any real user config.
	t.Setenv("APPDATA", t.TempDir())

	original := DefaultConfig()
	original.Tiling.DefaultLayout = "grid"
	original.Tiling.InnerGap = 12
	original.Bar.Position = "bottom"
	original.Bar.Height = 56
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
	if loaded.Bar != original.Bar {
		t.Errorf("bar mismatch: got %+v want %+v", loaded.Bar, original.Bar)
	}
	if loaded.Hotkeys != original.Hotkeys {
		t.Errorf("hotkeys mismatch: got %+v want %+v", loaded.Hotkeys, original.Hotkeys)
	}
	if len(loaded.Rules) != len(original.Rules) {
		t.Errorf("rule count: got %d want %d", len(loaded.Rules), len(original.Rules))
	}
}

// TestLoadCreatesDefaultOnFirstRun covers the onboarding bootstrap.
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

// TestParseRejectsMalformedTOML is the guard behind live-reload safety: a typo must
// surface as an error rather than a silently zeroed config.
func TestParseRejectsMalformedTOML(t *testing.T) {
	if _, err := Parse([]byte("[tiling\ndefault_layout = ")); err == nil {
		t.Error("expected a parse error for malformed TOML")
	}
}

// TestParseRejectsInvalidValues verifies validation runs on the parse path, not only
// when a caller remembers to call it.
func TestParseRejectsInvalidValues(t *testing.T) {
	data := []byte("[tiling]\ndefault_layout = \"diagonal\"\n")
	if _, err := Parse(data); err == nil {
		t.Error("expected a validation error for an unknown layout in the document")
	}
}

// TestParseIgnoresUnknownKeys keeps forward compatibility: a config written by a newer
// build must still load.
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

// TestPartialConfigKeepsDefaults verifies a sparse document inherits defaults rather
// than zeroing everything.
func TestPartialConfigKeepsDefaults(t *testing.T) {
	cfg, err := Parse([]byte("[bar]\nheight = 72\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Bar.Height != 72 {
		t.Errorf("height = %d, want 72", cfg.Bar.Height)
	}
	if cfg.Bar.Position != "top" {
		t.Errorf("position = %q, want the default %q", cfg.Bar.Position, "top")
	}
	if cfg.Tiling.DefaultLayout != "bsp" {
		t.Errorf("layout = %q, want the default %q", cfg.Tiling.DefaultLayout, "bsp")
	}
}

// TestConfigContainsNoSecrets documents that config.get over RPC is safe to expose in
// full: nothing here is a credential.
// TestParseToleratesUTF8BOM covers the editor behaviour that broke the first live
// run: PowerShell 5.1's Set-Content and Notepad both write a UTF-8 BOM, and go-toml
// rejects it with "invalid character at start of key". A config the user believes is
// fine silently failed to load.
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
// the screen: the first live run registered an AppBar and reserved 40px despite the
// user's config saying bar.enabled = false.
func TestSafeConfigIsInert(t *testing.T) {
	cfg := SafeConfig()

	if cfg.Bar.Enabled {
		t.Error("SafeConfig must not enable the AppBar")
	}
	if cfg.Bar.HideTaskbar {
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

// TestLoadFallsBackToSafeConfigOnBrokenFile covers the end-to-end path.
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
	if cfg.Bar.Enabled || cfg.Bar.HideTaskbar || cfg.Tiling.Enabled {
		t.Errorf("a broken config must yield an inert shell, got %+v", cfg)
	}
}

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

