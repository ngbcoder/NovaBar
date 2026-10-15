package media

// PlaybackState describes the active media playback state.
type PlaybackState string

const (
	StateClosed  PlaybackState = "closed"
	StateOpened  PlaybackState = "opened"
	StatePaused  PlaybackState = "paused"
	StatePlaying PlaybackState = "playing"
	StateStopped PlaybackState = "stopped"
)

// MediaSession represents current GSMTC media session metadata.
type MediaSession struct {
	SourceTitle string        `json:"source_title"`
	Title       string        `json:"title"`
	Artist      string        `json:"artist"`
	Album       string        `json:"album"`
	Playback    PlaybackState `json:"playback"`
	CanPlay     bool          `json:"can_play"`
	CanPause    bool          `json:"can_pause"`
	CanNext     bool          `json:"can_next"`
	CanPrev     bool          `json:"can_prev"`
}

// Controller coordinates media sessions and transport control.
type Controller struct {
	current MediaSession
}

// NewController creates a media session controller.
func NewController() *Controller {
	return &Controller{
		current: MediaSession{
			Playback: StateStopped,
		},
	}
}

// GetCurrentSession returns the active media metadata.
func (c *Controller) GetCurrentSession() MediaSession {
	return c.current
}
