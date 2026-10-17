package webview

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WidgetRootEnv is the environment variable that overrides where widgets are loaded
// from. Useful for development, where the working tree is edited but the running
// executable is not rebuilt.
const WidgetRootEnv = "NOVABAR_WIDGETS"

// WidgetAssetPrefix is the URL path widgets are served under.
const WidgetAssetPrefix = "/widget"

// FindWidgetRoot locates the widgets directory.
//
// Prefers, in order:
//   - $NOVABAR_WIDGETS
//   - a "widgets" directory beside the executable, for a packaged install
//   - a "widgets" directory under the working directory, for running from a checkout
func FindWidgetRoot() (string, error) {
	if dir := os.Getenv(WidgetRootEnv); dir != "" {
		if isDir(dir) {
			return filepath.Clean(dir), nil
		}
		return "", fmt.Errorf("%s points at %q, which is not a directory", WidgetRootEnv, dir)
	}

	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "widgets")
		if isDir(candidate) {
			return candidate, nil
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, "widgets")
		if isDir(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("could not find a widgets directory; set %s", WidgetRootEnv)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// SDKAssetPrefix is the URL path the widget SDK is served under.
const SDKAssetPrefix = "/sdk"

// FindSDKRoot locates the directory holding the shared widget SDK.
//
// The SDK sits beside the widgets tree rather than inside it, because it is shared by
// every widget and should not be copied per widget.
func FindSDKRoot() (string, error) {
	widgets, err := FindWidgetRoot()
	if err != nil {
		return "", err
	}

	// widgets/ lives either beside the executable or under the working directory, so
	// the SDK is one level up in both layouts.
	parent := filepath.Dir(widgets)
	for _, candidate := range []string{
		filepath.Join(parent, "sdk"),
		filepath.Join(widgets, "sdk"),
	} {
		if isFile(filepath.Join(candidate, "novabar.js")) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("could not find novabar.js beside %s", widgets)
}

// WidgetFileSystem returns an fs.FS rooted at the widget directory, with each widget as
// a subdirectory.
func WidgetFileSystem(root string) fs.FS {
	return os.DirFS(root)
}

// WidgetHandler serves the widget tree.
//
// Path traversal is blocked explicitly rather than relying on http.Dir alone, because a
// widget can request anything it likes and the surface is embedded in a shell the user
// trusts.
func WidgetHandler(root string) http.Handler {
	fileServer := http.FileServer(http.Dir(root))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "." {
			clean = ""
		}

		// Reject anything that tries to climb out of the widgets root.
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		full := filepath.Join(root, clean)
		// Belt and braces: confirm the resolved path is still inside the root, which
		// catches symlinks that the string check cannot see.
		absRoot, err := filepath.Abs(root)
		if err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		absFull, err := filepath.Abs(full)
		if err != nil || !strings.HasPrefix(absFull, absRoot) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		fileServer.ServeHTTP(w, r)
	})
}

// WidgetURL returns the URL a surface should navigate to in order to load a widget.
//
// A default of "bar" means a widget with no explicit configuration still loads the
// shipped one rather than rendering an empty strip.
func WidgetURL(port int, name string) string {
	if name == "" {
		name = "bar"
	}
	return fmt.Sprintf("http://127.0.0.1:%d%s/%s/index.html", port, WidgetAssetPrefix, name)
}

// ListWidgets returns the widget names available in a root, sorted, for status output
// and for telling the user what they can set in config.
func ListWidgets(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !isFile(filepath.Join(root, e.Name(), "index.html")) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
