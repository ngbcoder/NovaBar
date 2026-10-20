package daemon

import (
	"encoding/json"
	"log"

	"github.com/ngbcoder/NovaBar/internal/pins"
	"github.com/ngbcoder/NovaBar/internal/rpc"
	"github.com/ngbcoder/NovaBar/internal/win"
)

// registerPinMethods wires the dock's pin RPC surface.
//
// Extracted from registerRPCMethods so the handlers can be exercised over a real
// websocket in a test. The permission wiring is the security-relevant part and it was
// previously only reachable by running the whole daemon and attaching a widget surface,
// which is not something a test can do.
//
// A nil store is allowed and answers with an empty list rather than an error: a dock
// with no pin store shows no pinned tiles, which is a usable dock. Returning an error
// instead would leave the widget unable to render at all.
func registerPinMethods(r *rpc.Server, store *pins.Store) {
	empty := func() (interface{}, error) {
		return map[string]interface{}{"pins": []interface{}{}}, nil
	}

	// Helper to enrich pins with extracted Windows icons
	enrichPins := func(rawPins []pins.Pin) []map[string]interface{} {
		enriched := make([]map[string]interface{}, len(rawPins))
		for i, p := range rawPins {
			iconBase64, err := win.ExtractIconBase64(p.Target)
			if err != nil {
				iconBase64 = "" // Fallback gracefully if extraction fails
			}
			enriched[i] = map[string]interface{}{
				"target": p.Target,
				"name":   p.Name,
				"icon":   iconBase64,
			}
		}
		return enriched
	}

	r.RegisterMethod(rpc.MethodPinsList, func(json.RawMessage) (interface{}, error) {
		if store == nil {
			return empty()
		}
		return map[string]interface{}{"pins": enrichPins(store.List())}, nil
	})

	// Every mutation broadcasts, so the dock and the launcher stay consistent when both
	// are open and a pin is changed in one of them.
	announce := func() {
		r.Broadcast(rpc.TopicPinsChanged, map[string]interface{}{"pins": enrichPins(store.List())})
	}

	r.RegisterMethod(rpc.MethodPinsAdd, func(params json.RawMessage) (interface{}, error) {
		if store == nil {
			return empty()
		}
		var p struct {
			Target string `json:"target"`
			Name   string `json:"name"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if err := store.Add(pins.Pin{Target: p.Target, Name: p.Name}); err != nil {
			return nil, err
		}
		announce()
		return map[string]interface{}{"ok": true, "pinned": true}, nil
	})

	r.RegisterMethod(rpc.MethodPinsRemove, func(params json.RawMessage) (interface{}, error) {
		if store == nil {
			return empty()
		}
		var p struct {
			Target string `json:"target"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if err := store.Remove(p.Target); err != nil {
			return nil, err
		}
		announce()
		return map[string]interface{}{"ok": true, "pinned": false}, nil
	})

	r.RegisterMethod(rpc.MethodPinsToggle, func(params json.RawMessage) (interface{}, error) {
		if store == nil {
			return empty()
		}
		var p struct {
			Target string `json:"target"`
			Name   string `json:"name"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		now, err := store.Toggle(pins.Pin{Target: p.Target, Name: p.Name})
		if err != nil {
			return nil, err
		}
		announce()
		return map[string]interface{}{"ok": true, "pinned": now}, nil
	})

	r.RegisterMethod(rpc.MethodPinsMove, func(params json.RawMessage) (interface{}, error) {
		if store == nil {
			return empty()
		}
		var p struct {
			Target string `json:"target"`
			To     int    `json:"to"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if err := store.Move(p.Target, p.To); err != nil {
			return nil, err
		}
		announce()
		return map[string]interface{}{"ok": true}, nil
	})

	if store != nil && store.Degraded != "" {
		// Say so at startup. An empty dock caused by an unreadable file looks identical
		// to a user who has not pinned anything yet.
		log.Printf("[NovaBar] Pins: %s\n", store.Degraded)
	}
}
