package media

// Bridge is the platform seam for media session access.
//
// The pure-Go constraint makes GSMTC awkward: GlobalSystemMediaTransportControlsSessionManager
// is a WinRT class, and reaching it without go-ole means hand-driving IActivationFactory
// and the IInspectable ABI. That is a real piece of work and it is deliberately isolated
// behind this interface so it can be added without reshaping the controller, and so the
// shell remains usable when it is absent.
//
// On a machine where the bridge cannot be constructed, Controller reports Source "none"
// and transport commands fail with errNoBridge. It never fabricates metadata.
type Bridge interface {
	// Current returns the active media session.
	Current() (MediaSession, error)
	// Command applies a transport command.
	Command(cmd TransportCommand) error
	// Available reports whether a bridge implementation is present.
	Available() bool
	// Reason explains why a bridge is unavailable, or "" when one is.
	Reason() string
}
