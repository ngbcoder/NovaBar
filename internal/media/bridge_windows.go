//go:build windows

package media

import "sync"

// unavailableBridge is the implementation used when no usable GSMTC access exists.
//
// It is honest: Current reports "no session", Command refuses, and Reason explains what
// is actually missing. This is preferable to a stub reporting a permanently Stopped
// session with empty metadata, which is indistinguishable from a real player that had
// been paused.
//
// The reason string is carried rather than hardcoded because the cause varies: WinRT
// failing to initialize, the OS reporting the class unregistered, and the bridge being
// attached but unable to read properties yet are different problems with different
// fixes, and collapsing them into one message hides that.
type unavailableBridge struct {
	mu     sync.RWMutex
	reason string
}

// Current returns no session. There is nothing to report, and inventing a Stopped
// session with empty metadata would be indistinguishable from a real paused player.
func (b *unavailableBridge) Current() (MediaSession, error) {
	return MediaSession{Playback: StateUnknown, Source: "none"}, errNoSession
}

// Command refuses. Preferring to do nothing over pretending to control playback it
// cannot reach.
func (b *unavailableBridge) Command(TransportCommand) error {
	return errNoBridge
}

// Available reports whether media data can actually be served. Always false here: a
// bridge that cannot read a session is not available, even if one was constructed.
func (b *unavailableBridge) Available() bool { return false }

// Reason explains what is missing.
func (b *unavailableBridge) Reason() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.reason
}
