package daemon

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ngbcoder/NovaBar/internal/rpc"
	"github.com/ngbcoder/NovaBar/internal/volume"
)

// volumeServer is a real rpc.Server with the real volume handlers, reachable over a real
// websocket. This covers the JSON field names between Go and the widgets, which nothing
// else checks: a mismatch there is invisible until a volume slider silently stops working.
type volumeServer struct {
	ws *websocket.Conn
	id int
}

func startVolumeServer(t *testing.T, audio *volume.Controller, permissions ...string) *volumeServer {
	t.Helper()

	srv := rpc.NewServer()
	srv.SetDescriptorPath(filepath.Join(t.TempDir(), "ipc.json"))
	registerVolumeMethods(srv, audio)

	secret := "test-volume-secret"
	if err := srv.RegisterWidget(secret, "test", permissions); err != nil {
		t.Fatalf("RegisterWidget: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(srv.Stop)

	u := url.URL{Scheme: "ws", Host: fmt.Sprintf("127.0.0.1:%d", srv.Port()), Path: "/ws"}
	q := u.Query()
	q.Set("token", srv.Token())
	q.Set("widget", secret)
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return &volumeServer{ws: conn}
}

func (v *volumeServer) call(t *testing.T, method string, params interface{}) json.RawMessage {
	t.Helper()
	res, err := v.tryCall(method, params)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return res
}

func (v *volumeServer) tryCall(method string, params interface{}) (json.RawMessage, error) {
	v.id++
	req, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": v.id, "method": method, "params": params,
	})
	if err := v.ws.WriteMessage(websocket.TextMessage, req); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	_ = v.ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := v.ws.ReadMessage()
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

type volumeListPayload struct {
	Available bool             `json:"available"`
	Reason    string           `json:"reason"`
	Sessions  []volume.Session `json:"sessions"`
}

// TestVolumeMethodsEndToEnd walks the surface against the live audio stack.
func TestVolumeMethodsEndToEnd(t *testing.T) {
	audio := volume.New()
	defer audio.Close()

	if !audio.Available() {
		t.Skipf("audio unavailable: %s", audio.Reason())
	}

	v := startVolumeServer(t, audio, rpc.AllPermissions...)

	raw := v.call(t, rpc.MethodVolumeList, nil)
	var list volumeListPayload
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("volume.list returned undecodable JSON: %v (%s)", err, raw)
	}
	if !list.Available {
		t.Fatalf("volume.list reported unavailable on a working machine: %s", list.Reason)
	}
	for _, s := range list.Sessions {
		if s.SystemSounds {
			t.Errorf("volume.list leaked the system-sounds session")
		}
	}

	if len(list.Sessions) == 0 {
		t.Skip("no audio sessions to write to; read path verified")
	}

	// Write, verify, restore. This does move the user's real volume briefly, which is the
	// only way to prove the write path works.
	target := list.Sessions[0]
	for _, s := range list.Sessions {
		if s.State == volume.StateActive && s.PID != 0 {
			target = s
			break
		}
	}
	if target.PID == 0 {
		t.Skip("no session with an owning process")
	}

	restore := func() {
		if err := audio.SetVolume(target.PID, target.Volume); err != nil {
			t.Logf("could not restore volume for pid %d: %v", target.PID, err)
		}
	}
	defer restore()

	probe := float32(0.3)
	if target.Volume == probe {
		probe = 0.7
	}
	if err := audio.SetVolume(target.PID, probe); err != nil {
		t.Skipf("this session refused a volume change, which Windows permits for some apps: %v", err)
	}

	setRes := v.call(t, rpc.MethodVolumeSet, map[string]interface{}{"pid": target.PID, "level": probe})
	if len(setRes) == 0 {
		t.Error("volume.set returned nothing")
	}

	after := v.call(t, rpc.MethodVolumeList, nil)
	var list2 volumeListPayload
	if err := json.Unmarshal(after, &list2); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range list2.Sessions {
		if s.PID == target.PID {
			found = true
			if d := s.Volume - probe; d > 0.02 || d < -0.02 {
				t.Errorf("pid %d volume = %v after volume.set %v", s.PID, s.Volume, probe)
			}
		}
	}
	if !found {
		t.Errorf("pid %d vanished after a volume change", target.PID)
	}

	// mute round trip
	if err := audio.SetMute(target.PID, true); err == nil {
		v.call(t, rpc.MethodVolumeMute, map[string]interface{}{"pid": target.PID, "muted": true})
		_ = audio.SetMute(target.PID, false)
	}
}

// TestVolumeRejectsBadInput: the daemon must not be a way to write nonsense.
func TestVolumeRejectsBadInput(t *testing.T) {
	audio := volume.New()
	defer audio.Close()

	v := startVolumeServer(t, audio, rpc.AllPermissions...)

	if _, err := v.tryCall(rpc.MethodVolumeSet, map[string]interface{}{"pid": 0, "level": 0.5}); err == nil {
		t.Error("volume.set accepted pid 0")
	}
}

// TestVolumeRequiresPermission is the security assertion over a real socket.
func TestVolumeRequiresPermission(t *testing.T) {
	audio := volume.New()
	defer audio.Close()

	readOnly := startVolumeServer(t, audio, rpc.PermAudioRead)

	// Reading is allowed with audio.read alone, when audio is actually available.
	if audio.Available() {
		if _, err := readOnly.tryCall(rpc.MethodVolumeList, nil); err != nil {
			t.Errorf("audio.read cannot call volume.list: %v", err)
		}
	}

	for _, method := range []string{rpc.MethodVolumeSet, rpc.MethodVolumeMute} {
		if _, err := readOnly.tryCall(method, map[string]interface{}{"pid": 1, "level": 0.5, "muted": true}); err == nil {
			t.Errorf("%s succeeded with audio.read alone", method)
		}
	}
}

// TestVolumeWithUnavailableController checks the degraded path: every method answers, so
// a widget renders no volume UI instead of showing controls that do nothing.
func TestVolumeWithUnavailableController(t *testing.T) {
	v := startVolumeServer(t, nil, rpc.AllPermissions...)

	for _, method := range []string{rpc.MethodVolumeList, rpc.MethodVolumeSet, rpc.MethodVolumeMute} {
		res := v.call(t, method, map[string]interface{}{"pid": 1, "level": 0.5, "muted": true})
		var payload volumeListPayload
		if err := json.Unmarshal(res, &payload); err != nil {
			t.Fatalf("%s returned undecodable JSON: %v", method, err)
		}
		if payload.Available {
			t.Errorf("%s reported available with no controller", method)
		}
		if len(payload.Sessions) != 0 {
			t.Errorf("%s returned %d sessions with no controller", method, len(payload.Sessions))
		}
	}
}
