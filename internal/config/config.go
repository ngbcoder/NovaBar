package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	General GeneralConfig `toml:"general"`
	Tiling  TilingConfig  `toml:"tiling"`
	Bar     BarConfig     `toml:"bar"`
	Rules   []RuleConfig  `toml:"rules"`
}

type GeneralConfig struct {
	FirstRun  bool   `toml:"first_run"`
	LogLevel  string `toml:"log_level"`
	RescueKey string `toml:"rescue_hotkey"`
}

type TilingConfig struct {
	Enabled       bool   `toml:"enabled"`
	DefaultLayout string `toml:"default_layout"`
	InnerGap      int    `toml:"inner_gap"`
	OuterGap      int    `toml:"outer_gap"`
}

type BarConfig struct {
	Enabled     bool   `toml:"enabled"`
	Position    string `toml:"position"` // "top" or "bottom"
	Height      int    `toml:"height"`
	HideTaskbar bool   `toml:"hide_taskbar"`
}

type RuleConfig struct {
	Process string `toml:"process"`
	Class   string `toml:"class"`
	Title   string `toml:"title"`
	Action  string `toml:"action"` // "float", "tile", "ignore"
}

// DefaultConfig provides safe, standard defaults.
func DefaultConfig() Config {
	return Config{
		General: GeneralConfig{
			FirstRun:  true,
			LogLevel:  "info",
			RescueKey: "Ctrl+Alt+Shift+R",
		},
		Tiling: TilingConfig{
			Enabled:       true,
			DefaultLayout: "bsp",
			InnerGap:      8,
			OuterGap:      12,
		},
		Bar: BarConfig{
			Enabled:     true,
			Position:    "top",
			Height:      40,
			HideTaskbar: false, // Default false until explicitly enabled by user
		},
		Rules: []RuleConfig{
			{Process: "Taskmgr.exe", Action: "float"},
			{Process: "explorer.exe", Class: "OperationStatusWindow", Action: "float"},
		},
	}
}

// GetConfigPath returns %APPDATA%\NovaBar+\config.toml
func GetConfigPath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appData, "NovaBar+", "config.toml"), nil
}

// Load loads or creates default config.
func Load() (Config, error) {
	path, err := GetConfigPath()
	if err != nil {
		return DefaultConfig(), err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := DefaultConfig()
		_ = Save(cfg)
		return cfg, nil
	} else if err != nil {
		return DefaultConfig(), fmt.Errorf("read config error: %w", err)
	}

	cfg := DefaultConfig()
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig(), fmt.Errorf("parse config error: %w", err)
	}
	return cfg, nil
}

// Save writes config to %APPDATA%\NovaBar+\config.toml
func Save(cfg Config) error {
	path, err := GetConfigPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	bytes, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(path, bytes, 0644)
}
