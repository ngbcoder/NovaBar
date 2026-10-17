package media

import (
	"sync"
)

// PlaybackState describes the active media playback state.
type PlaybackState string

const (
	StateUnknown PlaybackState = "unknown"
	StateClosed  PlaybackState = "closed"
	StateOpened  PlaybackState = "opened"
	StatePaused  PlaybackState = "paused"
	StatePlaying PlaybackState = "playing"
	StateStopped PlaybackState = "stopped"
)

// TransportCommand is a media transport action.
type TransportCommand string

const (
	CommandPlay       TransportCommand = "play"
	CommandPause      TransportCommand = "pause"
	CommandTogglePlay TransportCommand = "toggle"
	CommandStop       TransportCommand = "stop"
	CommandNext       TransportCommand = "next"
	CommandPrevious   TransportCommand = "previous"
)

// MediaSession is the metadata for one media session.
type MediaSession struct {
	// AppID identifies the owning application, e.g. "Spotify.exe".
	AppID string `json:"app_id"`
	// Title, Artist and Album are metadata as reported by the session.
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album"`

	Playback PlaybackState `json:"playback"`

	// Timeline position and duration in seconds.
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`

	CanPlay     bool `json:"can_play"`
	CanPause    bool `json:"can_pause"`
	CanNext     bool `json:"can_next"`
	CanPrevious bool `json:"can_previous"`

	// Source is "gsmtc" when reported by a real Windows media session, or "none"
	// when the shell has nothing to report.
	Source string `json:"source"`
}

// Controller holds the current media session and applies transport commands.
//
// The transport itself is delegated to the Windows GSMTC session manager through the
// bridge in gsmtc_windows.go. When that bridge cannot be initialized, the controller
// reports Source "none" and refuses transport commands rather than pretending to
// control playback it cannot reach.
type Controller struct {
	mu      sync.RWMutex
	current MediaSession

	bridge Bridge
}

// NewController creates a media session controller and attempts to attach to the
// system GSMTC session manager.
func NewController() *Controller {
	c := &Controller{
		current: MediaSession{Playback: StateUnknown, Source: "none"},
	}

	if bridge, err := NewBridge(); err == nil {
		if session, err := bridge.Current(); err == nil {
			c.bridge = bridge
			c.current = session
		}
	}

	return c
}

// Attached reports whether a real GSMTC session manager is reachable.
func (c *Controller) Attached() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.bridge != nil
}

// GetCurrentSession returns the active media metadata.
func (c *Controller) GetCurrentSession() MediaSession {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

// Refresh re-reads the current session from the bridge.
func (c *Controller) Refresh() error {
	c.mu.RLock()
	bridge := c.bridge
	c.mu.RUnlock()

	if bridge == nil {
		return errNoBridge
	}

	session, err := bridge.Current()
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.current = session
	c.mu.Unlock()
	return nil
}

// Command applies a transport command to the current session.
func (c *Controller) Command(cmd TransportCommand) error {
	c.mu.RLock()
	bridge := c.bridge
	c.mu.RUnlock()

	if bridge == nil {
		return errNoBridge
	}

	if err := bridge.Command(cmd); err != nil {
		return err
	}

	// Reflect the change locally so the UI updates without waiting for the next
	// system media session change notification.
	c.mu.Lock()
	switch cmd {
	case CommandPlay, CommandTogglePlay:
		if cmd == CommandPlay {
			c.current.Playback = StatePlaying
		}
	case CommandPause:
		c.current.Playback = StatePaused
	case CommandStop:
		c.current.Playback = StateStopped
	}
	c.mu.Unlock()

	return nil
}

// SetSession installs a session directly. Used by the bridge when the system reports
// a change, and by tests.
func (c *Controller) SetSession(session MediaSession) {
	c.mu.Lock()
	c.current = session
	c.mu.Unlock()
}
