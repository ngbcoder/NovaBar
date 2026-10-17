package daemon

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ngbcoder/NovaBar/internal/pins"
	"github.com/ngbcoder/NovaBar/internal/rpc"
)

// pinServer is a real rpc.Server with the real pin handlers attached, reachable over a
// real websocket. This exercises the JSON shapes, the permission enforcement and the
// broadcast together, which unit tests of the store cannot.
type pinServer struct {
	srv *rpc.Server
	ws  *websocket.Conn
	id  int
}

// startPinServer builds the server and dials it as a widget holding the given
// permissions.
func startPinServer(t *testing.T, store *pins.Store, permissions ...string) *pinServer {
	t.Helper()

	// Keep the discovery descriptor out of the real %APPDATA%.
	t.Setenv("APPDATA", t.TempDir())

	srv := rpc.NewServer()
	registerPinMethods(srv, store)

	secret := "test-widget-secret"
	if err := srv.RegisterWidget(secret, "test", permissions); err != nil {
		t.Fatalf("RegisterWidget: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(srv.Stop)

	u := url.URL{
		Scheme: "ws",
		Host:   fmt.Sprintf("127.0.0.1:%d", srv.Port()),
		Path:   "/ws",
	}
	q := u.Query()
	q.Set("token", srv.Token())
	q.Set("widget", secret)
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return &pinServer{srv: srv, ws: conn}
}

// call invokes a method and returns the result, failing the test on an RPC error.
func (p *pinServer) call(t *testing.T, method string, params interface{}) json.RawMessage {
	t.Helper()
	res, err := p.tryCall(method, params)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return res
}

// tryCall invokes a method and returns any RPC error rather than failing.
func (p *pinServer) tryCall(method string, params interface{}) (json.RawMessage, error) {
	p.id++
	req, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      p.id,
		"method":  method,
		"params":  params,
	})
	if err := p.ws.WriteMessage(websocket.TextMessage, req); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	_ = p.ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := p.ws.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		return nil, fmt.Errorf("bad response %q: %w", msg, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}
	return resp.Result, nil
}

// pinTargets pulls the target list out of a pins.list result, which is what the widgets
// actually receive.
func pinTargets(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var payload struct {
		Pins []struct {
			Target   string `json:"target"`
			Name     string `json:"name"`
			ProcName string `json:"proc_name"`
		} `json:"pins"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("cannot decode %s: %v", raw, err)
	}
	out := make([]string, 0, len(payload.Pins))
	for _, p := range payload.Pins {
		out = append(out, p.Target)
	}
	return out
}

// TestPinMethodsEndToEnd walks the whole surface over a real socket with the real
// handlers. This is what covers the JSON field names, which nothing else checks: a
// mismatch between the Go struct tags and what widgets send is invisible until a pin
// silently fails to save.
func TestPinMethodsEndToEnd(t *testing.T) {
	store, err := pins.Open(filepath.Join(t.TempDir(), "pins.json"))
	if err != nil {
		t.Fatal(err)
	}

	p := startPinServer(t, store, rpc.AllPermissions...)

	code := `C:\Program Files\Microsoft VS Code\Code.exe`
	term := `C:\Windows\System32\WindowsTerminal.exe`

	if got := pinTargets(t, p.call(t, rpc.MethodPinsList, nil)); len(got) != 0 {
		t.Fatalf("a fresh store lists %v, want nothing", got)
	}

	p.call(t, rpc.MethodPinsAdd, map[string]string{"target": code, "name": "Code"})
	got := pinTargets(t, p.call(t, rpc.MethodPinsList, nil))
	if len(got) != 1 || got[0] != code {
		t.Fatalf("after add: %v, want [%s]", got, code)
	}

	// Adding the same app again must not duplicate it, and must not fail loudly enough
	// to make a widget show an error.
	p.call(t, rpc.MethodPinsAdd, map[string]string{"target": code, "name": "Code"})
	if got := pinTargets(t, p.call(t, rpc.MethodPinsList, nil)); len(got) != 1 {
		t.Errorf("re-adding produced %v, want one entry", got)
	}

	p.call(t, rpc.MethodPinsAdd, map[string]string{"target": term, "name": "Terminal"})
	if got := pinTargets(t, p.call(t, rpc.MethodPinsList, nil)); len(got) != 2 {
		t.Fatalf("after second add: %v, want two", got)
	}

	// toggle must report the new state, since a star button needs to know what it did.
	res := p.call(t, rpc.MethodPinsToggle, map[string]string{"target": code, "name": "Code"})
	if !strings.Contains(string(res), `"pinned":false`) {
		t.Errorf("toggle of a pinned app returned %s, want pinned:false", res)
	}
	if got := pinTargets(t, p.call(t, rpc.MethodPinsList, nil)); len(got) != 1 || got[0] != term {
		t.Errorf("after unpin: %v, want only %s", got, term)
	}

	// move reorders, and the dock relies on it to persist an arrangement.
	p.call(t, rpc.MethodPinsAdd, map[string]string{"target": code, "name": "Code"})
	p.call(t, rpc.MethodPinsMove, map[string]interface{}{"target": code, "to": 0})
	got = pinTargets(t, p.call(t, rpc.MethodPinsList, nil))
	if len(got) != 2 || got[0] != code {
		t.Errorf("after move: %v, want %s first", got, code)
	}

	// Removing something absent must succeed, so a stale dock cannot surface a failure.
	p.call(t, rpc.MethodPinsRemove, map[string]string{"target": `C:\never-pinned.exe`})

	// The store must reflect all of this on disk, not just in memory.
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(store.Path()), "pins.json"))
	if err != nil {
		t.Fatalf("pins.json was not written: %v", err)
	}
	if !strings.Contains(string(raw), "Code.exe") || !strings.Contains(string(raw), "Terminal.exe") {
		t.Errorf("pins.json does not contain both pins: %s", raw)
	}
}

// TestPinMethodsRejectInvalidInput: the daemon must not be a way to write junk into the
// user's configuration.
func TestPinMethodsRejectInvalidInput(t *testing.T) {
	store, err := pins.Open(filepath.Join(t.TempDir(), "pins.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := startPinServer(t, store, rpc.AllPermissions...)

	cases := []struct {
		name   string
		method string
		params map[string]string
	}{
		{"no target", rpc.MethodPinsAdd, map[string]string{"target": "", "name": "X"}},
		{"no name", rpc.MethodPinsAdd, map[string]string{"target": `C:\x.exe`, "name": ""}},
		{"nul in target", rpc.MethodPinsAdd, map[string]string{"target": "C:\\x\x00.exe", "name": "X"}},
	}
	for _, tc := range cases {
		if _, err := p.tryCall(tc.method, tc.params); err == nil {
			t.Errorf("%s: the daemon accepted it", tc.name)
		}
	}

	if got := pinTargets(t, p.call(t, rpc.MethodPinsList, nil)); len(got) != 0 {
		t.Errorf("rejected pins still reached the store: %v", got)
	}
}

// TestPinMethodsRequirePermission is the security assertion, over a real socket.
//
// Reading needs launcher.use; changing needs pins.manage. A widget holding only the read
// permission must not be able to alter what the user keeps.
func TestPinMethodsRequirePermission(t *testing.T) {
	store, err := pins.Open(filepath.Join(t.TempDir(), "pins.json"))
	if err != nil {
		t.Fatal(err)
	}
	readOnly := startPinServer(t, store, rpc.PermLauncherUse)

	if got := pinTargets(t, readOnly.call(t, rpc.MethodPinsList, nil)); len(got) != 0 {
		t.Errorf("a launcher.use widget listed %v, want nothing", got)
	}

	for _, method := range []string{rpc.MethodPinsAdd, rpc.MethodPinsRemove, rpc.MethodPinsToggle, rpc.MethodPinsMove} {
		if _, err := readOnly.tryCall(method, map[string]interface{}{"target": `C:\x.exe`, "name": "X", "to": 0}); err == nil {
			t.Errorf("%s succeeded with launcher.use alone", method)
		} else if !strings.Contains(err.Error(), "pins.manage") {
			t.Errorf("%s failed for the wrong reason: %v", method, err)
		}
	}
}

// TestPinMethodsWithNoStore checks the degraded case: the daemon starts without a
// usable pin store and every method still answers, because a dock that cannot render is
// worse than a dock with nothing pinned.
func TestPinMethodsWithNoStore(t *testing.T) {
	p := startPinServer(t, nil, rpc.AllPermissions...)

	for _, method := range []string{rpc.MethodPinsList, rpc.MethodPinsAdd, rpc.MethodPinsRemove, rpc.MethodPinsToggle, rpc.MethodPinsMove} {
		res := p.call(t, method, map[string]interface{}{"target": `C:\x.exe`, "name": "X", "to": 0})
		if !strings.Contains(string(res), `"pins":[]`) {
			t.Errorf("%s with no store returned %s, want an empty pin list", method, res)
		}
	}
}
