//go:build windows

package media

import (
	"errors"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/ngbcoder/NovaBar/internal/winrt"
)

// The GSMTC interfaces, by identifier.
//
// These identifiers were read from the operating system's own metadata,
// C:\Windows\System32\WinMetadata\Windows.Media.winmd, rather than recalled from
// documentation. An identifier that is wrong fails as E_NOINTERFACE, which reads
// plausibly enough to send the next hour down the wrong path; the same mistake as
// the wrong class name, one level down.
//
// A test in this package re-reads the metadata and fails if any of these drift.
var (
	// iidStatics is IGlobalSystemMediaTransportControlsSessionManagerStatics, the
	// only way in: the manager has no activatable instance, only this static.
	iidStatics = ole.NewGUID("2050c4ee-11a0-57de-aed7-c97c70338245")

	// iidManager is IGlobalSystemMediaTransportControlsSessionManager.
	iidManager = ole.NewGUID("cace8eac-e86e-504a-ab31-5ff8ff1bce49")

	// iidSession is IGlobalSystemMediaTransportControlsSession.
	iidSession = ole.NewGUID("7148c835-9b14-5ae2-ab85-dc9b1c14e1a8")

	// iidMediaProperties is IGlobalSystemMediaTransportControlsSessionMediaProperties.
	iidMediaProperties = ole.NewGUID("68856cf6-adb4-54b2-ac16-05837907acb6")

	// iidPlaybackInfo is IGlobalSystemMediaTransportControlsSessionPlaybackInfo.
	iidPlaybackInfo = ole.NewGUID("94b4b6cf-e8ba-51ad-87a7-c10ade106127")

	// iidPlaybackControls is IGlobalSystemMediaTransportControlsSessionPlaybackControls.
	iidPlaybackControls = ole.NewGUID("6501a3e6-bc7a-503a-bb1b-68f158f3fb03")

	// iidTimeline is IGlobalSystemMediaTransportControlsSessionTimelineProperties.
	iidTimeline = ole.NewGUID("ede34136-6f25-588d-8ecf-ea5b6735aaa5")

	// iidAsyncInfo is Windows.Foundation.IAsyncInfo, which carries the status of
	// an asynchronous operation.
	iidAsyncInfo = ole.NewGUID("00000036-0000-0000-c000-000000000046")
)

// Vtable slots.
//
// The ABI puts IUnknown in slots 0 to 2 and IInspectable in slots 3 to 5, so an
// interface's own first method is slot 6. The numbers below are the method's
// position in the interface's metadata plus six, which a test in this package
// re-derives from the metadata rather than trusting this list.
const (
	// IGlobalSystemMediaTransportControlsSessionManagerStatics.
	slotStaticsRequestAsync = 6

	// IGlobalSystemMediaTransportControlsSessionManager.
	slotManagerGetCurrentSession = 6
	slotManagerGetSessions       = 7

	// IGlobalSystemMediaTransportControlsSession.
	slotSessionGetSourceAppUserModelID = 6
	slotSessionTryGetMediaProperties   = 7
	slotSessionGetTimelineProperties   = 8
	slotSessionGetPlaybackInfo         = 9
	slotSessionTryPlay                 = 10
	slotSessionTryPause                = 11
	slotSessionTryStop                 = 12
	slotSessionTrySkipNext             = 16
	slotSessionTrySkipPrevious         = 17
	slotSessionTryTogglePlayPause      = 20

	// IGlobalSystemMediaTransportControlsSessionMediaProperties.
	slotMediaPropertiesGetTitle      = 6
	slotMediaPropertiesGetArtist     = 9
	slotMediaPropertiesGetAlbumTitle = 10

	// IGlobalSystemMediaTransportControlsSessionTimelineProperties.
	slotTimelineGetEndTime  = 7
	slotTimelineGetPosition = 10

	// IGlobalSystemMediaTransportControlsSessionPlaybackInfo.
	slotPlaybackInfoGetControls       = 6
	slotPlaybackInfoGetPlaybackStatus = 7

	// IGlobalSystemMediaTransportControlsSessionPlaybackControls.
	slotControlsGetIsPlayEnabled     = 6
	slotControlsGetIsPauseEnabled    = 7
	slotControlsGetIsStopEnabled     = 8
	slotControlsGetIsNextEnabled     = 12
	slotControlsGetIsPreviousEnabled = 13

	// IAsyncOperation.
	slotAsyncGetResults = 8

	// IAsyncInfo.
	slotAsyncInfoGetStatus = 7
)

// AsyncStatus values, read from the Windows.Foundation metadata.
//
// The declaration order in the metadata is not the value order, so these are the
// values rather than the positions.
const (
	asyncStatusStarted   = 0
	asyncStatusCompleted = 1
	asyncStatusCanceled  = 2
	asyncStatusError     = 3
)

// PlaybackStatus values, read from the Windows.Media.Control metadata.
const (
	playbackStatusClosed   = 0
	playbackStatusOpened   = 1
	playbackStatusChanging = 2
	playbackStatusStopped  = 3
	playbackStatusPlaying  = 4
	playbackStatusPaused   = 5
)

// ticksPerSecond is the number of 100-nanosecond ticks in a second, which is the
// unit WinRT expresses durations in.
const ticksPerSecond = 10_000_000

// asyncTimeout bounds how long the bridge waits for an asynchronous operation.
//
// Nothing in the media stack normally takes this long. The bound exists so that a
// stalled operation cannot wedge the media thread for ever.
const asyncTimeout = 5 * time.Second

// asyncPollInterval is how often the status of an asynchronous operation is checked.
const asyncPollInterval = 5 * time.Millisecond

// errNoCurrentSession is returned when nothing is playing.
var errNoCurrentSession = errors.New("no media session is playing")

// gsmtcReader reads media sessions through the GSMTC interfaces.
//
// It holds live interface pointers, so it belongs to the thread whose apartment
// created them. The bridge owns one of these and only ever touches it from its own
// thread.
type gsmtcReader struct {
	manager winrt.Object
}

// newGSMTCReader activates the session manager and returns a reader for it.
//
// The class has no activatable instance, so the way in is its static interface: the
// factory is asked for the statics, the statics are asked for the manager, and the
// manager is what actually answers questions.
func newGSMTCReader() (*gsmtcReader, error) {
	statics, err := activateStatics()
	if err != nil {
		return nil, err
	}
	defer statics.Release()

	manager, err := requestManager(statics)
	if err != nil {
		return nil, err
	}

	return &gsmtcReader{manager: manager}, nil
}

// activateStatics asks the runtime for the session manager's static interface.
//
// The static interface is requested directly, and the fallback exists because the
// class is static-only: some runtimes hand back an activation factory rather than
// the statics object itself, and the statics can then be reached by a query.
func activateStatics() (winrt.Object, error) {
	factory, err := ole.RoGetActivationFactory(sessionManagerClass, iidStatics)
	if err == nil {
		return winrt.New(unsafe.Pointer(factory)), nil
	}

	unknown, unkErr := ole.RoGetActivationFactory(sessionManagerClass, iidUnknown)
	if unkErr != nil {
		// The first error is the interesting one: it says the class itself could
		// not be reached.
		return winrt.Object{}, err
	}

	base := winrt.New(unsafe.Pointer(unknown))
	statics, qiErr := base.QueryInterface(iidStatics)
	base.Release()
	if qiErr != nil {
		return winrt.Object{}, err
	}
	return statics, nil
}

// requestManager asks the statics for the session manager.
//
// RequestAsync returns an asynchronous operation; the manager is its result.
func requestManager(statics winrt.Object) (winrt.Object, error) {
	var asyncPtr unsafe.Pointer
	hr := statics.Call(slotStaticsRequestAsync, uintptr(unsafe.Pointer(&asyncPtr)))
	if hr.Failed() {
		return winrt.Object{}, hr
	}
	if asyncPtr == nil {
		return winrt.Object{}, winrt.EUnexpected
	}

	async := winrt.New(asyncPtr)
	defer async.Release()

	managerPtr, err := awaitResult(async)
	if err != nil {
		return winrt.Object{}, err
	}
	if managerPtr == nil {
		return winrt.Object{}, winrt.EUnexpected
	}

	manager := winrt.New(managerPtr)
	iface, err := manager.QueryInterface(iidManager)
	manager.Release()
	if err != nil {
		return winrt.Object{}, err
	}
	return iface, nil
}

// awaitResult waits for an asynchronous operation and returns its result pointer.
//
// The operation's status is polled rather than subscribed to. A callback would have
// to be delivered on a specific thread, and the apartment is per-thread, so a
// callback that arrived on a thread without one would fault.
func awaitResult(op winrt.Object) (unsafe.Pointer, error) {
	if err := waitForCompletion(op); err != nil {
		return nil, err
	}

	var result unsafe.Pointer
	hr := op.Call(slotAsyncGetResults, uintptr(unsafe.Pointer(&result)))
	if hr.Failed() {
		return nil, hr
	}
	return result, nil
}

// waitForCompletion polls an operation until it finishes.
func waitForCompletion(op winrt.Object) error {
	info, err := op.QueryInterface(iidAsyncInfo)
	if err != nil {
		return err
	}
	defer info.Release()

	deadline := time.Now().Add(asyncTimeout)
	for {
		var status int32
		hr := info.Call(slotAsyncInfoGetStatus, uintptr(unsafe.Pointer(&status)))
		if hr.Failed() {
			return hr
		}

		switch status {
		case asyncStatusCompleted:
			return nil
		case asyncStatusError:
			return errors.New("the media session request failed")
		case asyncStatusCanceled:
			return errors.New("the media session request was cancelled")
		}

		if time.Now().After(deadline) {
			return errors.New("the media session request did not finish in time")
		}
		time.Sleep(asyncPollInterval)
	}
}

// awaitBool waits for an asynchronous operation that yields a boolean.
//
// The transport commands answer with one, and whether it succeeded is the answer
// to "did the player accept this".
func awaitBool(op winrt.Object) (bool, error) {
	if err := waitForCompletion(op); err != nil {
		return false, err
	}

	var result byte
	hr := op.Call(slotAsyncGetResults, uintptr(unsafe.Pointer(&result)))
	if hr.Failed() {
		return false, hr
	}
	return result != 0, nil
}

// currentSession reads the session that is playing now.
//
// A nil session means nothing is playing, which is not an error: it is the normal
// state of a machine with no media running.
func (r *gsmtcReader) currentSession() (MediaSession, error) {
	var sessionPtr unsafe.Pointer
	hr := r.manager.Call(slotManagerGetCurrentSession, uintptr(unsafe.Pointer(&sessionPtr)))
	if hr.Failed() {
		return MediaSession{}, hr
	}
	if sessionPtr == nil {
		return MediaSession{}, errNoCurrentSession
	}

	base := winrt.New(sessionPtr)
	defer base.Release()

	session, err := base.QueryInterface(iidSession)
	if err != nil {
		return MediaSession{}, err
	}
	defer session.Release()

	out := MediaSession{Source: "gsmtc", Playback: StateUnknown}

	if appID, err := readString(session, slotSessionGetSourceAppUserModelID); err == nil {
		out.AppID = appID
	}

	// Media properties are optional. A player may publish a session without
	// metadata, and a failure here must not cost the transport controls.
	if props, err := r.mediaProperties(session); err == nil {
		out.Title = props.Title
		out.Artist = props.Artist
		out.Album = props.Album
	}

	if timeline, err := r.timeline(session); err == nil {
		out.Position = timeline.Position
		out.Duration = timeline.Duration
	}

	if info, err := r.playbackInfo(session); err == nil {
		out.Playback = info.Status
		out.CanPlay = info.CanPlay
		out.CanPause = info.CanPause
		out.CanNext = info.CanNext
		out.CanPrevious = info.CanPrevious
	}

	return out, nil
}

// mediaProperties holds a session's metadata.
type mediaProperties struct {
	Title  string
	Artist string
	Album  string
}

// mediaProperties reads the session's title, artist and album.
func (r *gsmtcReader) mediaProperties(session winrt.Object) (mediaProperties, error) {
	var asyncPtr unsafe.Pointer
	hr := session.Call(slotSessionTryGetMediaProperties, uintptr(unsafe.Pointer(&asyncPtr)))
	if hr.Failed() {
		return mediaProperties{}, hr
	}
	if asyncPtr == nil {
		return mediaProperties{}, winrt.EUnexpected
	}

	async := winrt.New(asyncPtr)
	defer async.Release()

	propsPtr, err := awaitResult(async)
	if err != nil {
		return mediaProperties{}, err
	}
	if propsPtr == nil {
		return mediaProperties{}, winrt.EUnexpected
	}

	base := winrt.New(propsPtr)
	defer base.Release()

	props, err := base.QueryInterface(iidMediaProperties)
	if err != nil {
		return mediaProperties{}, err
	}
	defer props.Release()

	out := mediaProperties{}
	if title, err := readString(props, slotMediaPropertiesGetTitle); err == nil {
		out.Title = title
	}
	if artist, err := readString(props, slotMediaPropertiesGetArtist); err == nil {
		out.Artist = artist
	}
	if album, err := readString(props, slotMediaPropertiesGetAlbumTitle); err == nil {
		out.Album = album
	}
	return out, nil
}

// timeline holds a session's position and duration.
type timeline struct {
	Position float64
	Duration float64
}

// timeline reads the session's position and duration.
func (r *gsmtcReader) timeline(session winrt.Object) (timeline, error) {
	var propsPtr unsafe.Pointer
	hr := session.Call(slotSessionGetTimelineProperties, uintptr(unsafe.Pointer(&propsPtr)))
	if hr.Failed() {
		return timeline{}, hr
	}
	if propsPtr == nil {
		return timeline{}, winrt.EUnexpected
	}

	base := winrt.New(propsPtr)
	defer base.Release()

	props, err := base.QueryInterface(iidTimeline)
	if err != nil {
		return timeline{}, err
	}
	defer props.Release()

	out := timeline{}
	if end, err := readTimeSpan(props, slotTimelineGetEndTime); err == nil {
		out.Duration = end
	}
	if position, err := readTimeSpan(props, slotTimelineGetPosition); err == nil {
		out.Position = position
	}
	return out, nil
}

// playbackState holds a session's transport state.
type playbackState struct {
	Status      PlaybackState
	CanPlay     bool
	CanPause    bool
	CanNext     bool
	CanPrevious bool
}

// playbackInfo reads the session's transport state and capabilities.
func (r *gsmtcReader) playbackInfo(session winrt.Object) (playbackState, error) {
	var infoPtr unsafe.Pointer
	hr := session.Call(slotSessionGetPlaybackInfo, uintptr(unsafe.Pointer(&infoPtr)))
	if hr.Failed() {
		return playbackState{}, hr
	}
	if infoPtr == nil {
		return playbackState{}, winrt.EUnexpected
	}

	base := winrt.New(infoPtr)
	defer base.Release()

	info, err := base.QueryInterface(iidPlaybackInfo)
	if err != nil {
		return playbackState{}, err
	}
	defer info.Release()

	out := playbackState{Status: StateUnknown}

	var status int32
	if hr := info.Call(slotPlaybackInfoGetPlaybackStatus, uintptr(unsafe.Pointer(&status))); hr.Failed() {
		return playbackState{}, hr
	}
	out.Status = playbackStatusToState(status)

	var controlsPtr unsafe.Pointer
	hr = info.Call(slotPlaybackInfoGetControls, uintptr(unsafe.Pointer(&controlsPtr)))
	if hr.Failed() {
		return playbackState{}, hr
	}
	if controlsPtr == nil {
		return out, nil
	}

	controlsBase := winrt.New(controlsPtr)
	defer controlsBase.Release()

	controls, err := controlsBase.QueryInterface(iidPlaybackControls)
	if err != nil {
		return out, nil
	}
	defer controls.Release()

	out.CanPlay, _ = readBool(controls, slotControlsGetIsPlayEnabled)
	out.CanPause, _ = readBool(controls, slotControlsGetIsPauseEnabled)
	out.CanNext, _ = readBool(controls, slotControlsGetIsNextEnabled)
	out.CanPrevious, _ = readBool(controls, slotControlsGetIsPreviousEnabled)

	return out, nil
}

// command applies a transport command to the current session.
func (r *gsmtcReader) command(cmd TransportCommand) error {
	var sessionPtr unsafe.Pointer
	hr := r.manager.Call(slotManagerGetCurrentSession, uintptr(unsafe.Pointer(&sessionPtr)))
	if hr.Failed() {
		return hr
	}
	if sessionPtr == nil {
		return errNoCurrentSession
	}

	base := winrt.New(sessionPtr)
	defer base.Release()

	session, err := base.QueryInterface(iidSession)
	if err != nil {
		return err
	}
	defer session.Release()

	var slot int
	switch cmd {
	case CommandPlay:
		slot = slotSessionTryPlay
	case CommandPause:
		slot = slotSessionTryPause
	case CommandStop:
		slot = slotSessionTryStop
	case CommandTogglePlay:
		slot = slotSessionTryTogglePlayPause
	case CommandNext:
		slot = slotSessionTrySkipNext
	case CommandPrevious:
		slot = slotSessionTrySkipPrevious
	default:
		return errors.New("unsupported transport command")
	}

	var asyncPtr unsafe.Pointer
	hr = session.Call(slot, uintptr(unsafe.Pointer(&asyncPtr)))
	if hr.Failed() {
		return hr
	}
	if asyncPtr == nil {
		return winrt.EUnexpected
	}

	async := winrt.New(asyncPtr)
	defer async.Release()

	accepted, err := awaitBool(async)
	if err != nil {
		return err
	}
	if !accepted {
		return errors.New("the player refused the transport command")
	}
	return nil
}

// playbackStatusToState maps the metadata's playback status onto the shell's states.
func playbackStatusToState(status int32) PlaybackState {
	switch status {
	case playbackStatusPlaying:
		return StatePlaying
	case playbackStatusPaused:
		return StatePaused
	case playbackStatusStopped:
		return StateStopped
	case playbackStatusOpened:
		return StateOpened
	case playbackStatusClosed:
		return StateClosed
	case playbackStatusChanging:
		return StateOpened
	}
	return StateUnknown
}

// readString reads a string property.
func readString(obj winrt.Object, slot int) (string, error) {
	var h ole.HString
	hr := obj.Call(slot, uintptr(unsafe.Pointer(&h)))
	if hr.Failed() {
		return "", hr
	}
	return winrt.ReadHString(h), nil
}

// readBool reads a boolean property.
func readBool(obj winrt.Object, slot int) (bool, error) {
	var value byte
	hr := obj.Call(slot, uintptr(unsafe.Pointer(&value)))
	if hr.Failed() {
		return false, hr
	}
	return value != 0, nil
}

// readTimeSpan reads a duration property and converts it to seconds.
func readTimeSpan(obj winrt.Object, slot int) (float64, error) {
	var ticks int64
	hr := obj.Call(slot, uintptr(unsafe.Pointer(&ticks)))
	if hr.Failed() {
		return 0, hr
	}
	return float64(ticks) / ticksPerSecond, nil
}
