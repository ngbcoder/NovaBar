// Command mediaprobe reports what the media bridge can see.
//
// It exists because the interesting failures in the media stack are environmental:
// the bridge attaches on one machine and not another, and a session may or may not be
// playing. A tool that prints the state, and the reason for it, settles those
// questions without a debugger.
//
// Usage:
//
//	mediaprobe [command]
//
// A command is one of play, pause, toggle, stop, next or previous.
package main

import (
	"fmt"
	"os"

	"github.com/ngbcoder/NovaBar/internal/media"
)

func main() {
	controller := media.NewController()

	fmt.Printf("attached: %v\n", controller.Attached())
	if reason := controller.Reason(); reason != "" {
		fmt.Printf("reason:   %s\n", reason)
	}

	session := controller.GetCurrentSession()
	fmt.Printf("session:  source=%q app=%q title=%q artist=%q album=%q\n",
		session.Source, session.AppID, session.Title, session.Artist, session.Album)
	fmt.Printf("state:    playback=%q position=%.1fs duration=%.1fs\n",
		session.Playback, session.Position, session.Duration)
	fmt.Printf("caps:     play=%v pause=%v next=%v previous=%v\n",
		session.CanPlay, session.CanPause, session.CanNext, session.CanPrevious)

	if len(os.Args) < 2 {
		return
	}

	cmd := media.TransportCommand(os.Args[1])
	if err := controller.Command(cmd); err != nil {
		fmt.Printf("command:  %s failed: %v\n", cmd, err)
		return
	}
	fmt.Printf("command:  %s accepted\n", cmd)

	after := controller.GetCurrentSession()
	fmt.Printf("after:    playback=%q position=%.1fs\n", after.Playback, after.Position)
}
