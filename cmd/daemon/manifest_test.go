package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/ngbcoder/NovaBar/internal/rpc"
)

// widgetsDir is the shipped widget tree, relative to cmd/daemon.
const widgetsDir = "../../widgets"

// manifest mirrors what readManifestPermissions parses, plus the id for reporting.
//
// Dir is the directory name, which is not the id: a widget's manifest calls itself
// "novabar.dock" while it lives in widgets/dock. Confusing the two makes every path in
// this file silently wrong.
type manifest struct {
	Dir         string   `json:"-"`
	ID          string   `json:"id"`
	Entry       string   `json:"entry"`
	Permissions []string `json:"permissions"`
}

// loadManifests reads every widget manifest under widgets/.
func loadManifests(t *testing.T) []manifest {
	t.Helper()

	entries, err := os.ReadDir(widgetsDir)
	if err != nil {
		t.Fatalf("cannot read %s: %v", widgetsDir, err)
	}

	var out []manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(widgetsDir, e.Name(), "manifest.json"))
		if err != nil {
			continue // not a widget directory
		}
		var m manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Errorf("widget %q has a manifest that is not valid JSON: %v", e.Name(), err)
			continue
		}
		m.Dir = e.Name()
		out = append(out, m)
	}

	if len(out) == 0 {
		t.Fatalf("no widget manifests found under %s; this test would pass vacuously", widgetsDir)
	}
	return out
}

// TestShippedManifestsDeclareKnownPermissions is the check that stops a typo in a
// manifest from silently blanking a bar.
//
// An unknown permission makes the daemon deny the whole widget, so the bar still
// reserves its space and renders nothing, with the only explanation in a log line. That
// is exactly the kind of failure that reaches a user as "the dock just doesn't show up".
func TestShippedManifestsDeclareKnownPermissions(t *testing.T) {
	for _, m := range loadManifests(t) {
		for _, p := range m.Permissions {
			if !rpc.ValidPermission(p) {
				t.Errorf("widget %q declares unknown permission %q; the daemon would deny the widget entirely",
					m.ID, p)
			}
		}
	}
}

// TestShippedManifestsHaveNoDuplicatePermissions keeps the declared set honest; a repeat
// suggests the list was edited carelessly.
func TestShippedManifestsHaveNoDuplicatePermissions(t *testing.T) {
	for _, m := range loadManifests(t) {
		seen := map[string]bool{}
		for _, p := range m.Permissions {
			if seen[p] {
				t.Errorf("widget %q declares %q more than once", m.ID, p)
			}
			seen[p] = true
		}
	}
}

// callPattern finds sdk.call('method', ...) in a widget's JavaScript.
//
// Deliberately a narrow textual scan rather than a JS parser: the point is to notice a
// call the manifest forgot to grant, and a regex over the literal first argument catches
// that in practice without pulling a parser into the build.
var callPattern = regexp.MustCompile(`sdk\.call\(\s*'([a-z][a-z0-9_.]*)'`)

// TestShippedWidgetsOnlyCallMethodsTheyMayCall is the load-bearing consistency check.
//
// A widget calling a method its manifest did not request fails at runtime with an
// authorization error in the page console, which surfaces as a feature that silently does
// nothing. Adding a permission to the manifest and a method call in the same change is
// the normal way this breaks, so it is checked here instead of being discovered on a
// user's desktop.
//
// This has already caught one real bug: the launcher focused an already-running window
// instead of launching a second copy, but never declared windows.control, so that path
// failed silently for every user.
//
// Known imprecision: the permission model has no windows.focus, so a widget that only
// focuses must also be granted move and close. That is wider than the launcher needs. It
// is left as is rather than growing the permission model mid-milestone, and recorded
// here so it is not mistaken for an oversight when someone revisits permissions.
func TestShippedWidgetsOnlyCallMethodsTheyMayCall(t *testing.T) {
	for _, m := range loadManifests(t) {
		if m.Entry == "" {
			t.Errorf("widget %q declares no entry", m.ID)
			continue
		}

		// Scan every .js beside the entry, since the entry only pulls in main.js.
		scripts, err := filepath.Glob(filepath.Join(widgetsDir, m.Dir, "*.js"))
		if err != nil || len(scripts) == 0 {
			t.Errorf("widget %q has no JavaScript beside %s", m.ID, m.Entry)
			continue
		}

		granted := map[string]bool{}
		for _, p := range m.Permissions {
			granted[p] = true
		}

		available := rpc.MethodPermissions()
		var problems []string
		for _, script := range scripts {
			raw, err := os.ReadFile(script)
			if err != nil {
				t.Errorf("cannot read %s: %v", script, err)
				continue
			}
			for _, match := range callPattern.FindAllStringSubmatch(string(raw), -1) {
				method := match[1]
				required, known := available[method]
				if !known {
					problems = append(problems,
						fmt.Sprintf("%s calls %q, which no widget may call", filepath.Base(script), method))
					continue
				}
				if !granted[required] {
					problems = append(problems,
						fmt.Sprintf("%s calls %q, which needs %q that %s does not declare",
							filepath.Base(script), method, required, m.ID))
				}
			}
		}

		sort.Strings(problems)
		for _, p := range problems {
			t.Error(p)
		}
	}
}
