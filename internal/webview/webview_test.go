package webview

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapJSCarriesEndpointAndToken(t *testing.T) {
	boot := Bootstrap{
		Port:        51234,
		Token:       "s3cr3t-token",
		Widget:      "bar",
		Permissions: []string{"windows.read"},
	}

	payload, err := boot.PayloadJSON()
	if err != nil {
		t.Fatalf("PayloadJSON: %v", err)
	}

	var got Bootstrap
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}

	if got.Port != 51234 {
		t.Errorf("Port = %d, want 51234", got.Port)
	}
	if got.Token != "s3cr3t-token" {
		t.Errorf("Token = %q, want it carried through", got.Token)
	}
	if got.Widget != "bar" {
		t.Errorf("Widget = %q, want %q", got.Widget, "bar")
	}
	if len(got.Permissions) != 1 || got.Permissions[0] != "windows.read" {
		t.Errorf("Permissions = %v, want [windows.read]", got.Permissions)
	}

	// The same payload must be embedded in the script.
	js, err := boot.BootstrapJS()
	if err != nil {
		t.Fatalf("BootstrapJS: %v", err)
	}
	if !strings.Contains(js, payload) {
		t.Errorf("BootstrapJS does not embed the payload verbatim:\n%s", js)
	}
}

// TestBootstrapJSIsValidJavascript is a cheap structural guard: the injected snippet is
// wrapped in an IIFE and must be balanced, because a syntax error here silently kills
// every widget rather than failing loudly.
func TestBootstrapJSIsValidJavascript(t *testing.T) {
	js, err := Bootstrap{Port: 1, Token: "t"}.BootstrapJS()
	if err != nil {
		t.Fatalf("BootstrapJS: %v", err)
	}

	open := strings.Count(js, "{") - strings.Count(js, "}")
	close := strings.Count(js, "(") - strings.Count(js, ")")
	if open != 0 {
		t.Errorf("unbalanced braces: %d", open)
	}
	if close != 0 {
		t.Errorf("unbalanced parens: %d", close)
	}
	if !strings.HasPrefix(strings.TrimSpace(js), "(function") {
		t.Error("bootstrap should be an IIFE so it does not leak names into the page")
	}
}

func TestBootstrapJSEscapesToken(t *testing.T) {
	// A token containing quote characters must not be able to break out of the injected
	// string literal and inject code.
	boot := Bootstrap{Port: 1, Token: `"; alert(1); //`}

	payload, err := boot.PayloadJSON()
	if err != nil {
		t.Fatalf("PayloadJSON: %v", err)
	}

	// JSON encoding is what makes this safe, so assert on the round trip rather than
	// on the presence of escapes.
	var got Bootstrap
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("payload is not valid JSON, so escaping failed: %v", err)
	}
	if got.Token != `"; alert(1); //` {
		t.Errorf("token = %q, want it preserved verbatim", got.Token)
	}

	js, err := boot.BootstrapJS()
	if err != nil {
		t.Fatalf("BootstrapJS: %v", err)
	}
	if strings.Contains(js, `alert(1); //`) && !strings.Contains(js, `\"`) {
		t.Error("token appears unescaped in the injected script")
	}
}

func TestWidgetURL(t *testing.T) {
	if got, want := WidgetURL(1234, "dock"), "http://127.0.0.1:1234/widget/dock/index.html"; got != want {
		t.Errorf("WidgetURL = %q, want %q", got, want)
	}

	// An unset widget name must still resolve to the shipped bar rather than 404.
	if got, want := WidgetURL(1234, ""), "http://127.0.0.1:1234/widget/bar/index.html"; got != want {
		t.Errorf("WidgetURL with no name = %q, want %q", got, want)
	}
}

// TestWidgetHandlerBlocksTraversal covers the path-traversal guard. A widget is code the
// shell loads, so anything that can read outside the widgets root is a way to exfiltrate
// files into a trusted surface.
func TestWidgetHandlerBlocksTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bar"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bar", "index.html"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	// A secret outside the widgets root.
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	handler := WidgetHandler(root)

	for _, path := range []string{
		"/../secret.txt",
		"/..%2fsecret.txt",
		"/bar/../../secret.txt",
	} {
		req := newRequest(t, path)
		rec := newRecorder()
		handler.ServeHTTP(rec, req)

		if strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("path %q (status %d) served a file outside the widget root", path, statusOf(rec))
		}
	}
}

func TestWidgetHandlerServesWidget(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bar"), 0755); err != nil {
		t.Fatal(err)
	}
	body := "<!doctype html><title>bar</title>"
	if err := os.WriteFile(filepath.Join(root, "bar", "index.html"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	handler := WidgetHandler(root)

	// A widget directory is served at the directory path. Requesting
	// "/bar/index.html" yields a canonical redirect, which is http.FileServer's normal
	// behaviour and not something to fight.
	req := newRequest(t, "/bar/")
	rec := newRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("serving /bar/ returned %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bar") {
		t.Errorf("widget content was not served, got %d bytes", rec.Body.Len())
	}

	req = newRequest(t, "/bar/index.html")
	rec = newRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Errorf("/bar/index.html returned %d, want a 301 to the directory", rec.Code)
	}
}

func TestListWidgets(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"bar", "dock", "launcher"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "index.html"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// A directory with no index.html is not a widget.
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0755); err != nil {
		t.Fatal(err)
	}

	got := ListWidgets(root)
	want := []string{"bar", "dock", "launcher"}
	if len(got) != len(want) {
		t.Fatalf("ListWidgets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ListWidgets[%d] = %q, want %q (must be sorted)", i, got[i], want[i])
		}
	}
}

func TestListWidgetsMissingRoot(t *testing.T) {
	if got := ListWidgets(filepath.Join(t.TempDir(), "nope")); got != nil {
		t.Errorf("ListWidgets on a missing root = %v, want nil", got)
	}
}

func TestFindWidgetRootHonoursEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(WidgetRootEnv, dir)

	got, err := FindWidgetRoot()
	if err != nil {
		t.Fatalf("FindWidgetRoot: %v", err)
	}
	if got != filepath.Clean(dir) {
		t.Errorf("root = %q, want %q", got, filepath.Clean(dir))
	}
}

func TestFindWidgetRootRejectsNonDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(WidgetRootEnv, file)

	if _, err := FindWidgetRoot(); err == nil {
		t.Error("expected an error when the override points at a file")
	}
}

// TestDefaultBrowserArgsIncludesThrottleFix pins the two switches that decide whether a
// desktop bar works at all. A window that is never focused is judged occluded, and
// Chromium throttles its timers to roughly one tick per second without them, which shows
// up as a clock that updates once a second and animations that crawl.
func TestDefaultBrowserArgsIncludesThrottleFix(t *testing.T) {
	required := []string{
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-renderer-backgrounding",
	}

	have := make(map[string]bool, len(DefaultBrowserArgs))
	for _, arg := range DefaultBrowserArgs {
		have[arg] = true
	}

	for _, arg := range required {
		if !have[arg] {
			t.Errorf("DefaultBrowserArgs is missing %q", arg)
		}
	}
}
