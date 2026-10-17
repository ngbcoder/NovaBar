package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Control commands accepted on the control endpoint.
const (
	// CmdRescue unregisters the AppBar, restores the native taskbars and shuts the
	// daemon down cleanly.
	//
	// This is the durable rescue path. A bare ShowWindow from another process un-hides
	// the taskbar but leaves the AppBar registered, the work area reserved, and the
	// daemon alive to re-hide it on the next tick. Rescue does all three.
	CmdRescue = "rescue"
	// CmdShutdown performs the same cleanup without logging it as an emergency.
	CmdShutdown = "shutdown"
	// CmdStatus returns a JSON status document.
	CmdStatus = "status"
)

// ControlPath is the HTTP path carrying control commands. It shares the loopback
// listener and token auth with the WebSocket endpoint.
const ControlPath = "/control"

// ControlRequest is one command sent by shellctl.
type ControlRequest struct {
	Command string `json:"command"`
}

// ControlResponse is the daemon's reply.
type ControlResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

// ControlHandler executes a control command. Returning an error produces an ok:false
// response rather than dropping the connection, so the CLI can report why.
type ControlHandler func(command string) (any, error)

func validControlCommand(cmd string) bool {
	switch cmd {
	case CmdRescue, CmdShutdown, CmdStatus:
		return true
	}
	return false
}

// handleControl serves POST /control.
func (s *Server) handleControl(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"error":"unauthorized: missing or invalid NovaBar token"}`))
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		writeControl(w, http.StatusBadRequest, ControlResponse{OK: false, Error: "could not read request body"})
		return
	}

	var req ControlRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeControl(w, http.StatusBadRequest, ControlResponse{OK: false, Error: "malformed control request: " + err.Error()})
		return
	}

	command := strings.ToLower(strings.TrimSpace(req.Command))
	if !validControlCommand(command) {
		writeControl(w, http.StatusBadRequest, ControlResponse{
			OK:    false,
			Error: fmt.Sprintf("unknown command %q; expected one of %s, %s, %s", req.Command, CmdRescue, CmdShutdown, CmdStatus),
		})
		return
	}

	data, err := s.controlHandler(command)
	if err != nil {
		writeControl(w, http.StatusInternalServerError, ControlResponse{OK: false, Error: err.Error()})
		return
	}

	writeControl(w, http.StatusOK, ControlResponse{OK: true, Data: data})
}

func writeControl(w http.ResponseWriter, status int, resp ControlResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// SendControlCommand executes one control command against a running daemon.
//
// Returns an error when no daemon is reachable, which the caller uses to fall back to
// direct recovery.
func SendControlCommand(desc IPCDescriptor, command string, timeout time.Duration) (ControlResponse, error) {
	var resp ControlResponse

	payload, err := json.Marshal(ControlRequest{Command: command})
	if err != nil {
		return resp, err
	}

	url := fmt.Sprintf("http://127.0.0.1:%d%s", desc.Port, ControlPath)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return resp, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+desc.Token)

	client := &http.Client{Timeout: timeout}
	httpResp, err := client.Do(req)
	if err != nil {
		return resp, fmt.Errorf("no response from daemon on port %d: %w", desc.Port, err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, 64*1024))
	if err != nil {
		return resp, fmt.Errorf("could not read daemon response: %w", err)
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return resp, fmt.Errorf("malformed control response (HTTP %d): %w", httpResp.StatusCode, err)
	}
	return resp, nil
}

// DaemonReachable reports whether a daemon is running and responding to a status
// request. Used by shellctl to choose between the control path and direct recovery.
func DaemonReachable(desc IPCDescriptor, timeout time.Duration) bool {
	if !IsDaemonAlive(desc) {
		return false
	}
	resp, err := SendControlCommand(desc, CmdStatus, timeout)
	return err == nil && resp.OK
}