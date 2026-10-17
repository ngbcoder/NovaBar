package rpc

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// connectWithPermission dials as a registered widget surface.
func connectWithPermission(t *testing.T, s *Server, secret string) (*websocket.Conn, *Client) {
	t.Helper()

	u := url.URL{Scheme: "ws", Host: "127.0.0.1:" + itoa(s.Port()), Path: "/ws"}
	q := u.Query()
	q.Set("token", s.Token())
	if secret != "" {
		q.Set("widget", secret)
	}
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Wait for the server to register the client before returning it, otherwise the
	// first assertion can race the handshake.
	waitFor(t, func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return len(s.clients) == 1
	})

	s.mu.RLock()
	var client *Client
	for c := range s.clients {
		client = c
	}
	s.mu.RUnlock()

	t.Cleanup(func() { _ = conn.Close() })
	return conn, client
}

// registerSurface issues a credential for a widget with the given permissions.
func registerSurface(t *testing.T, s *Server, widget string, perms []string) string {
	t.Helper()
	secret := "secret-" + widget
	if err := s.RegisterWidget(secret, widget, perms); err != nil {
		t.Fatalf("RegisterWidget: %v", err)
	}
	return secret
}

func call(t *testing.T, conn *websocket.Conn, method string) *Response {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	var resp Response
	if err := readResponse(conn, &resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	return &resp
}

// TestWidgetPermissionEnforced is the core guarantee: a widget only reaches what its
// manifest asked for.
func TestWidgetPermissionEnforced(t *testing.T) {
	s := startTestServer(t)
	secret := registerSurface(t, s, "bar", []string{PermWindowsRead})

	conn, client := connectWithPermission(t, s, secret)

	if client.widgetID != "bar" {
		t.Errorf("widgetID = %q, want %q", client.widgetID, "bar")
	}

	// Granted.
	if resp := call(t, conn, MethodWindowsGet); resp.Error != nil {
		t.Errorf("%s should be allowed with %s, got error: %s",
			MethodWindowsGet, PermWindowsRead, resp.Error.Message)
	}

	// Not granted.
	resp := call(t, conn, MethodWindowClose)
	if resp.Error == nil {
		t.Fatalf("%s must be refused for a widget without %s", MethodWindowClose, PermWindowsControl)
	}
	if resp.Error.Code != CodeUnauthorized {
		t.Errorf("code = %d, want %d", resp.Error.Code, CodeUnauthorized)
	}
}

// TestSessionTokenAloneGrantsNothing covers the case that matters most: knowing the
// session token is not enough to do anything.
func TestSessionTokenAloneGrantsNothing(t *testing.T) {
	s := startTestServer(t)

	// No widget credential presented.
	conn, client := connectWithPermission(t, s, "")

	if len(client.permissions) != 0 {
		t.Errorf("permissions = %v, want none", client.permissions)
	}

	for _, method := range []string{MethodWindowsGet, MethodWindowClose, MethodLauncherLaunch, MethodConfigGet} {
		resp := call(t, conn, method)
		if resp.Error == nil {
			t.Errorf("%s was allowed with no widget credential", method)
		}
	}
}

// TestUnknownWidgetCredentialGrantsNothing covers a widget presenting a credential the
// server does not recognise.
func TestUnknownWidgetCredentialGrantsNothing(t *testing.T) {
	s := startTestServer(t)

	conn, client := connectWithPermission(t, s, "not-a-real-credential")

	if len(client.permissions) != 0 {
		t.Errorf("permissions = %v, want none for an unknown credential", client.permissions)
	}

	if resp := call(t, conn, MethodWindowsGet); resp.Error == nil {
		t.Error("windows.get was allowed with an unknown credential")
	}
}

// TestRevokedWidgetLosesAccess verifies that destroying a surface really removes its
// authority, rather than leaving a credential live for the rest of the session.
func TestRevokedWidgetLosesAccess(t *testing.T) {
	s := startTestServer(t)
	secret := registerSurface(t, s, "dock", []string{PermLauncherUse})

	if _, ok := s.lookupWidget(secret); !ok {
		t.Fatal("widget should be registered")
	}

	s.RevokeWidget(secret)

	if _, ok := s.lookupWidget(secret); ok {
		t.Error("widget should be gone after revoke")
	}

	// A new connection presenting the revoked credential gets nothing.
	conn, client := connectWithPermission(t, s, secret)
	if len(client.permissions) != 0 {
		t.Errorf("a revoked credential still grants %v", client.permissions)
	}
	if resp := call(t, conn, MethodLauncherSearch); resp.Error == nil {
		t.Error("launcher.search was allowed with a revoked credential")
	}
}

// TestRegisterWidgetRejectsUnknownPermission keeps a typo in a manifest from silently
// producing a widget that cannot do what it meant to.
func TestRegisterWidgetRejectsUnknownPermission(t *testing.T) {
	s := startTestServer(t)

	err := s.RegisterWidget("s", "bar", []string{"windows.destroy_everything"})
	if err == nil {
		t.Fatal("expected an error for an unknown permission")
	}
	if !contains(err.Error(), "windows.destroy_everything") {
		t.Errorf("error should name the offending permission, got: %v", err)
	}
}

// TestEveryMethodIsGated guards against a method being added to the handler table and
// silently becoming callable, which is how permission systems usually rot.
func TestEveryMethodIsGated(t *testing.T) {
	// A widget with nothing granted must be refused every mapped method, and refused
	// unmapped ones too since those are not widget-callable at all.
	empty := &Client{permissions: map[string]bool{}}
	for method := range methodPermissions {
		if err := empty.hasPermission(method); err == nil {
			t.Errorf("%s is reachable without any permission", method)
		}
	}
	if err := empty.hasPermission("some.future.method"); err == nil {
		t.Error("an unmapped method must not be callable by widgets")
	}

	// A widget holding everything can reach everything mapped.
	all := &Client{permissions: map[string]bool{}}
	for _, p := range AllPermissions {
		all.permissions[p] = true
	}
	for method := range methodPermissions {
		if err := all.hasPermission(method); err != nil {
			t.Errorf("a fully permitted widget cannot call %s: %v", method, err)
		}
	}
}

// TestTopicSubscriptionIsGated covers the leak where a widget with no permissions is
// still pushed window titles.
func TestTopicSubscriptionIsGated(t *testing.T) {
	s := startTestServer(t)
	conn, _ := connectWithPermission(t, s, "")

	if err := conn.WriteJSON(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "subscribe",
		"params": map[string]any{"topic": TopicWindowsUpdated},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	var resp Response
	if err := readResponse(conn, &resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Error == nil {
		t.Error("subscribing without windows.read should be refused")
	}
}

// TestSubscriptionAllowedWithPermission is the other half: a permitted widget does get
// its topics.
func TestSubscriptionAllowedWithPermission(t *testing.T) {
	s := startTestServer(t)
	secret := registerSurface(t, s, "bar", []string{PermWindowsRead})
	conn, _ := connectWithPermission(t, s, secret)

	if err := conn.WriteJSON(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "subscribe",
		"params": map[string]any{"topic": TopicWindowsUpdated},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	var resp Response
	if err := readResponse(conn, &resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Error != nil {
		t.Errorf("subscribe refused for a permitted widget: %+v", resp.Error)
	}
}

// TestControlEndpointUnaffectedByWidgetPermissions guards the rescue path: the CLI talks
// to /control, not /ws, and must keep working regardless of anything a widget did.
func TestControlEndpointUnaffectedByWidgetPermissions(t *testing.T) {
	s := startTestServer(t)
	desc, err := readDescriptorAt(s.DescriptorPath())
	if err != nil {
		t.Fatalf("reading the test server's descriptor: %v", err)
	}

	resp, err := SendControlCommand(desc, CmdStatus, 3*time.Second)
	if err != nil {
		t.Fatalf("SendControlCommand: %v", err)
	}
	if !resp.OK {
		t.Errorf("control endpoint returned ok=false: %s", resp.Error)
	}
}

// TestPermissionsSerializeCleanly checks the bootstrap payload a widget receives.
func TestPermissionsSerializeCleanly(t *testing.T) {
	payload, err := json.Marshal(map[string][]string{"permissions": AllPermissions})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(payload) == 0 {
		t.Error("empty payload")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		indexOfString(haystack, needle) >= 0
}

func indexOfString(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

var _ = http.StatusOK
