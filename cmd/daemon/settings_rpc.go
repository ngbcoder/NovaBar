package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/ngbcoder/NovaBar/internal/config"
	"github.com/ngbcoder/NovaBar/internal/rpc"
	"github.com/ngbcoder/NovaBar/internal/win"
)

// registerConfigMethods wires the settings surface.
//
// The schema is returned rather than a hard-coded field list so the settings UI is
// generated from the same declaration the daemon applies through. That is the whole point
// of internal/config.Schema: a control cannot exist that the daemon would refuse, and a
// field cannot be added without appearing here.
func registerConfigMethods(r *rpc.Server, d *daemon) {
	r.RegisterMethod(rpc.MethodConfigSchema, func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"fields":    config.Schema(),
			"values":    config.Values(&d.cfg),
			"protected": config.ProtectedPaths(),
		}, nil
	})

	r.RegisterMethod(rpc.MethodConfigSet, func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Path  string `json:"path"`
			Value any    `json:"value"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}

		field, known := config.FieldByPath(p.Path)
		if !known {
			return nil, fmt.Errorf("%q is not an editable setting", p.Path)
		}

		// The two settings that guard the user are gated on the rescue path actually
		// working. Editing your own escape route is only safe once you have one.
		if field.Safety == config.SafetyProtect && !d.rescueReady() {
			return nil, fmt.Errorf(
				"%q protects the way out of NovaBar, and the rescue hotkey is not currently working; "+
					"fix that first so you cannot lock yourself out of your desktop", p.Path)
		}

		// Applied to a copy so a rejected edit cannot leave the live config half-changed.
		next := d.cfg
		if err := config.Apply(&next, p.Path, p.Value); err != nil {
			return nil, err
		}

		if err := config.Save(next); err != nil {
			return nil, fmt.Errorf("could not save the config: %w", err)
		}

		// Adopt it directly rather than waiting for the file watcher. The watcher exists
		// for edits made outside the shell; a change made here would otherwise take
		// effect a moment later, and the UI would look broken in between.
		d.cfg = next
		d.engine.SetConfig(next)
		d.engine.DetectMonitors()

		// The bars own WebView2 surfaces, and WebView2 is bound to the thread that
		// created it, so the work is handed to the message pump thread. Doing it
		// here would kill the daemon.
		d.onPumpThread(func() {
			d.applyBars(next)
			d.applyTaskbarVisibility(next)
		})

		d.hotkeys.Register(next)
		d.retile()
		d.publishWindows()

		return map[string]interface{}{
			"ok":    true,
			"value": config.Values(&d.cfg),
		}, nil
	})

	// Hotkey conflicts. Windows exposes no way to ask whether another application already
	// owns a combination, so this validates the spec and checks our own bindings, which is
	// what can be checked without stealing the key. The daemon logs the real
	// RegisterHotKey failure on reload, which is the only authoritative signal.
	r.RegisterMethod(rpc.MethodHotkeyCheck, func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Spec string `json:"spec"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}

		spec := strings.TrimSpace(p.Spec)
		if spec == "" {
			return map[string]interface{}{"ok": false, "reason": "no hotkey given"}, nil
		}
		if err := validateHotkeySpec(spec); err != nil {
			return map[string]interface{}{"ok": false, "reason": err.Error()}, nil
		}
		if other, clash := d.otherBindingUsing(spec, p.Spec); clash {
			return map[string]interface{}{
				"ok":     false,
				"reason": fmt.Sprintf("already bound to %q in NovaBar", other),
			}, nil
		}
		return map[string]interface{}{"ok": true}, nil
	})
}

// rescueReady reports whether the rescue hotkey is currently bound.
//
// Deliberately conservative: it must be able to say yes, and anything short of a bound,
// registered hotkey is not enough. This gate exists to stop a user removing their way out,
// so an optimistic answer defeats it.
func (d *daemon) rescueReady() bool {
	if d.hotkeys == nil {
		return false
	}
	want := strings.TrimSpace(d.cfg.Hotkeys.Rescue)
	if want == "" {
		return false
	}
	for _, registered := range d.hotkeys.Registered() {
		// Registered reports "Ctrl+Alt+Shift+R -> rescue".
		if strings.HasSuffix(registered, "-> rescue") && strings.Contains(registered, want) {
			return true
		}
	}
	return false
}

// otherBindingUsing reports whether a spec is already used by a different hotkey.
func (d *daemon) otherBindingUsing(spec, changingPath string) (string, bool) {
	bindings := map[string]string{
		"hotkeys.rescue":         d.cfg.Hotkeys.Rescue,
		"hotkeys.launcher":       d.cfg.Hotkeys.Launcher,
		"hotkeys.workspace_prev": d.cfg.Hotkeys.WorkspacePrev,
		"hotkeys.workspace_next": d.cfg.Hotkeys.WorkspaceNext,
	}
	for path, bound := range bindings {
		if path == changingPath {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(bound), spec) {
			return path, true
		}
	}
	return "", false
}

// validateHotkeySpec checks a combination is one the shell could actually bind.
//
// Delegates to the same parser Register uses, rather than reimplementing the rules. A
// second implementation of "what is a valid chord" drifts from the first, and the failure
// mode is a settings page that cheerfully accepts a combination which then silently fails
// to bind on reload.
func validateHotkeySpec(spec string) error {
	if _, err := win.ParseHotkeySpec(spec); err != nil {
		return err
	}

	// A bare key would swallow ordinary typing system-wide.
	parts := strings.Split(spec, "+")
	modifierCount := 0
	for _, raw := range parts {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "ctrl", "control", "alt", "shift", "win", "windows", "meta", "super", "cmd":
			modifierCount++
		}
	}
	if modifierCount == 0 {
		return fmt.Errorf("%q has no modifier; that would swallow ordinary typing", spec)
	}
	return nil
}

// applyTaskbarVisibility applies the taskbar setting, extracted so config.set and the file
// watcher cannot drift apart.
func (d *daemon) applyTaskbarVisibility(cfg config.Config) {
	if cfg.General.HideTaskbar && !d.taskbars.IsHidden() {
		if err := d.taskbars.Hide(); err != nil {
			log.Printf("[NovaBar] Could not hide taskbars: %v\n", err)
		}
		d.hideTaskbar.Store(true)
	} else if !cfg.General.HideTaskbar && d.taskbars.IsHidden() {
		if err := d.taskbars.Show(); err != nil {
			log.Printf("[NovaBar] Could not restore taskbars: %v\n", err)
		}
		d.hideTaskbar.Store(false)
	} else {
		// The taskbar state already matches the config, but the guard's flag still
		// has to follow it.
		d.hideTaskbar.Store(cfg.General.HideTaskbar)
	}
}

// widgetPermissions reads a widget's declared permissions.
//
// Replaces the hard-coded lists that were being passed to RegisterWidget at each call site.
// Those drifted: the launcher was granted only launcher.use and windows.read while its own
// code called pins.toggle and window.focus, so both failed at runtime with an
// authorization error the user only saw in the page console.
//
// A manifest is still a request, not a grant, so unknown permissions are dropped here and
// still refused by RegisterWidget if the daemon does not understand them.
func (d *daemon) widgetPermissions(widget string) []string {
	declared, err := d.readManifestPermissions(widget)
	if err != nil {
		log.Printf("[NovaBar] Widget %q declares no readable manifest: %v\n", widget, err)
		return nil
	}

	out := make([]string, 0, len(declared))
	for _, p := range declared {
		if rpc.ValidPermission(p) {
			out = append(out, p)
			continue
		}
		log.Printf("[NovaBar] Widget %q declares unknown permission %q; ignoring it\n", widget, p)
	}
	return out
}
