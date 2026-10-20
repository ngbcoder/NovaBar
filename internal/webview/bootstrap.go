package webview

import (
	"encoding/json"
	"fmt"
)

// Bootstrap is the data injected into a widget page before it loads.
//
// Injected rather than served, because the session token must not exist on disk. A
// widget file could be read by any process, whereas injected JavaScript only ever lives
// in the page's memory.
type Bootstrap struct {
	// Port is the loopback port the RPC server is listening on.
	Port int `json:"port"`
	// Token authorizes this widget against the RPC server.
	Token string `json:"token"`
	// WidgetToken is a per-surface credential that determines which permissions this
	// widget is allowed to exercise. Distinct from Token: the session token proves the
	// page belongs to this shell, while this proves which surface it is and therefore
	// what its manifest was allowed to ask for.
	WidgetToken string `json:"widget_token"`
	// Widget is the name of the widget being loaded, so a widget shipped for one
	// surface can detect it is running in another.
	Widget string `json:"widget"`
	// Permissions is the set the widget's manifest declares. The page may read it to
	// decide what to offer, but it grants nothing: the daemon enforces permissions
	// server side.
	Permissions []string `json:"permissions"`
	// Data is anything else the surface needs to know before it starts.
	//
	// The zone picker needs the shape of its grid, and it needs it before it draws
	// anything: the grid it draws has to be the grid the layout will use, and a
	// message sent after the page has loaded is a message that can be missed.
	Data map[string]any `json:"data,omitempty"`
}

// PayloadJSON encodes the bootstrap as JSON.
//
// Exposed separately from BootstrapJS so tests can assert on the payload directly
// rather than scraping it back out of the injected script, where a naive brace scan
// picks up the IIFE's own object literal instead.
func (b Bootstrap) PayloadJSON() (string, error) {
	payload, err := json.Marshal(b)
	if err != nil {
		return "", fmt.Errorf("could not encode the widget bootstrap: %w", err)
	}
	return string(payload), nil
}

// BootstrapJS returns the script that defines window.NOVABAR before the widget's own
// code runs.
//
// Injecting via Init rather than an inline <script> in the served HTML matters: the
// served file is static and cacheable, while the endpoint is per-process.
func (b Bootstrap) BootstrapJS() (string, error) {
	payload, err := b.PayloadJSON()
	if err != nil {
		return "", err
	}

	// window.chrome.webview.postMessage is the WebView2 bridge the message channel
	// listens on. The fallback keeps a widget usable in an ordinary browser during
	// development, where there is no bridge and no daemon.
	return fmt.Sprintf(`
(function () {
  var cfg = %s;
  window.NOVABAR = {
    port: cfg.port,
    token: cfg.token,
    widget_token: cfg.widget_token,
    widget: cfg.widget,
    permissions: cfg.permissions || [],
    hasBridge: typeof window.chrome !== 'undefined' && !!window.chrome.webview,
    post: function (message) {
      if (this.hasBridge) { window.chrome.webview.postMessage(JSON.stringify(message)); }
    },
    on: function (handler) {
      if (!this.hasBridge) { return; }
      window.chrome.webview.addEventListener('message', function (e) {
        try { handler(JSON.parse(e.data)); } catch (_) { /* ignore malformed */ }
      });
    }
  };
})();
`, string(payload)), nil
}
