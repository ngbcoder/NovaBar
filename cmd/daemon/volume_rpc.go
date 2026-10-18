package daemon

import (
	"encoding/json"
	"log"
	"time"

	"github.com/ngbcoder/NovaBar/internal/rpc"
	"github.com/ngbcoder/NovaBar/internal/volume"
)

// audioPollInterval is how often the audio session list is re-read.
//
// Polling rather than subscribing to IAudioSessionNotifications deliberately. That
// interface is a further hand-written COM callback with a lifetime tied to the thread that
// registered it, and a callback delivered to a collected thread is a crash. Polling cannot
// leak, costs a cheap enumeration, and volume changes are not so fast that a two second
// lag would be noticed.
const audioPollInterval = 2 * time.Second

// registerVolumeMethods wires the per-application volume surface.
//
// Split out of registerRPCMethods so the handlers can be driven over a real websocket in a
// test, the same reason the pin handlers were extracted. Volume writes have real-world
// effects, so they are worth exercising without starting the whole shell.
func registerVolumeMethods(r *rpc.Server, audio *volume.Controller) {
	// unavailable answers every audio method when there is no usable output device, so
	// a widget renders no volume UI rather than showing controls that silently do
	// nothing.
	unavailable := func() (interface{}, error) {
		return map[string]interface{}{
			"sessions":  []interface{}{},
			"available": false,
			"reason":    audio.Reason(),
		}, nil
	}

	r.RegisterMethod(rpc.MethodVolumeList, func(json.RawMessage) (interface{}, error) {
		if audio == nil || !audio.Available() {
			return unavailable()
		}
		sessions, err := audio.List()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"sessions":  sessions,
			"available": true,
		}, nil
	})

	r.RegisterMethod(rpc.MethodVolumeSet, func(params json.RawMessage) (interface{}, error) {
		if audio == nil || !audio.Available() {
			return unavailable()
		}
		var p struct {
			PID   uint32  `json:"pid"`
			Level float32 `json:"level"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if p.PID == 0 {
			return nil, volume.ErrNoSession
		}
		if err := audio.SetVolume(p.PID, p.Level); err != nil {
			return nil, err
		}
		return map[string]interface{}{"ok": true, "pid": p.PID, "level": p.Level}, nil
	})

	r.RegisterMethod(rpc.MethodVolumeMute, func(params json.RawMessage) (interface{}, error) {
		if audio == nil || !audio.Available() {
			return unavailable()
		}
		var p struct {
			PID   uint32 `json:"pid"`
			Muted bool   `json:"muted"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if p.PID == 0 {
			return nil, volume.ErrNoSession
		}
		if err := audio.SetMute(p.PID, p.Muted); err != nil {
			return nil, err
		}
		return map[string]interface{}{"ok": true, "pid": p.PID, "muted": p.Muted}, nil
	})
}

// audioState remembers the last broadcast payload so only genuine changes are sent.
type audioState struct {
	last []byte
}

// pollAudio re-reads the audio sessions on an interval and broadcasts on change.
//
// stop is closed when the daemon tears down. Shutdown normally signals the pump thread
// with a posted window message, which this goroutine never sees, so it needs its own
// signal rather than assuming one exists.
func (d *daemon) pollAudio(stop <-chan struct{}) {
	ticker := time.NewTicker(audioPollInterval)
	defer ticker.Stop()

	var state audioState
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}

		sessions, err := d.audio.List()
		if err != nil {
			// A transient failure is not worth spamming. The daemon keeps running and
			// the next tick tries again.
			continue
		}

		payload, err := json.Marshal(map[string]interface{}{
			"sessions":  sessions,
			"available": true,
		})
		if err != nil {
			continue
		}

		if string(payload) == string(state.last) {
			continue
		}
		state.last = payload

		if d.rpcs != nil {
			d.rpcs.Broadcast(rpc.TopicAudioUpdated, json.RawMessage(payload))
		}
	}
}

// audioStatus adds the audio subsystem to `shellctl status`.
func (d *daemon) audioStatus() map[string]any {
	out := map[string]any{"available": false}
	if d.audio == nil {
		out["reason"] = "audio subsystem is not running"
		return out
	}
	if !d.audio.Available() {
		out["reason"] = d.audio.Reason()
		return out
	}
	if sessions, err := d.audio.List(); err == nil {
		out["available"] = true
		out["count"] = len(sessions)
	} else {
		out["reason"] = err.Error()
	}
	return out
}

// logAudioStartup reports the audio subsystem state once, so a missing output device is
// visible in the log rather than only as an absent dock affordance.
func logAudioStartup(a *volume.Controller) {
	if a == nil {
		log.Printf("[NovaBar Volume] subsystem not running\n")
		return
	}
	if !a.Available() {
		log.Printf("[NovaBar Volume] unavailable: %s\n", a.Reason())
		return
	}
	if sessions, err := a.List(); err == nil {
		log.Printf("[NovaBar Volume] %d audio session(s)\n", len(sessions))
	} else {
		log.Printf("[NovaBar Volume] could not enumerate sessions: %v\n", err)
	}
}
