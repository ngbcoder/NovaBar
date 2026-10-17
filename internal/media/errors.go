package media

import "errors"

var (
	// errNoBridge means the Windows GSMTC session manager could not be reached.
	//
	// This is expected on Windows builds without the media session stack, and on
	// systems with no active media app. Callers surface it as "no media info"
	// rather than an error, because it is a normal state, not a fault.
	errNoBridge = errors.New("no Windows media session manager available")

	// errNoSession means the manager is reachable but nothing is playing.
	errNoSession = errors.New("no active media session")

	// errUnsupportedCommand means the current session cannot service the transport.
	errUnsupportedCommand = errors.New("current media session does not support this command")
)