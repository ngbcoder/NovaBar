package media

import "sync"

// unavailableBridge is the implementation used when no GSMTC access exists.
//
// It is honest: Current reports "no session", Command refuses, and Reason explains
// what is missing. This is preferable to the previous stub, which reported a
// permanently Stopped session with empty metadata, indistinguishable from a real
// player that had been paused.
type unavailableBridge struct {
	mu     sync.RWMutex
	reason string
}

// NewBridge returns the platform media bridge.
//
// On Windows without an implemented WinRT activation path this is an
// unavailableBridge carrying the reason.
func NewBridge() (Bridge, error) {
	b := &unavailableBridge{
		reason: "Windows GSMTC access requires WinRT activation, which needs go-ole and therefore cgo; " +
			"NovaBar builds with CGO_ENABLED=0 so no media bridge is attached. Transport controls " +
			"are reported as unavailable rather than faked.",
	}
	return b, nil
}

func (b *unavailableBridge) Current() (MediaSession, error) {
	return MediaSession{Playback: StateUnknown, Source: "none"}, errNoSession
}

func (b *unavailableBridge) Command(TransportCommand) error {
	return errNoBridge
}

func (b *unavailableBridge) Available() bool { return false }

func (b *unavailableBridge) Reason() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.reason
}