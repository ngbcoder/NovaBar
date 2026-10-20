package tiling

import (
	"math"

	"golang.org/x/sys/windows"
)

// Direction is a direction on the screen.
type Direction string

const (
	// DirectionLeft is towards the left edge.
	DirectionLeft Direction = "left"
	// DirectionDown is towards the bottom edge.
	DirectionDown Direction = "down"
	// DirectionUp is towards the top edge.
	DirectionUp Direction = "up"
	// DirectionRight is towards the right edge.
	DirectionRight Direction = "right"
)

// ApplyZone lays the windows out as zones, with the first one taking a chosen cell.
//
// The first subject is the window that takes the cell: the caller puts it there, so
// the cell the user picked belongs to the window that was focused, rather than to
// whichever window happened to be first in the list.
//
// The zone applies to the display the first window is on. The other displays are
// laid out normally, because a grid over one display says nothing about another.
func (e *Engine) ApplyZone(subjects []WindowSubject, cell int) bool {
	if len(subjects) == 0 {
		return false
	}

	monitors := e.GetMonitors()
	if len(monitors) == 0 {
		return false
	}

	monitor := e.monitorIndexFor(subjects[0].HWnd, monitors)
	if monitor < 0 {
		return false
	}

	placements := e.decide(subjects, &zoneRequest{monitor: monitor, cell: cell})
	if len(placements) == 0 {
		return false
	}

	toMove := make([]Placement, 0, len(placements))
	for _, p := range placements {
		if p.Action == ActionTile {
			toMove = append(toMove, p)
		}
	}
	if len(toMove) > 0 {
		e.applyPlacements(toMove)
	}
	e.applyFloating(placements)

	return true
}

// FocusInDirection finds the window next to the given one in a direction.
//
// The neighbours are the windows that overlap on the perpendicular axis and lie in
// the direction asked for, and the nearest of them wins. A window far away in the
// same direction but on the other side of the screen is not a neighbour, and
// neither is a window that does not overlap at all: without the overlap test,
// pressing "up" from a window in the bottom-left corner would jump to a window in
// the top-right, which is not what anyone means by up.
//
// The window is returned rather than focused: bringing a window to the front is a
// separate step, and one the shell may not be allowed to take.
func (e *Engine) FocusInDirection(subjects []WindowSubject, from windows.HWND, dir Direction) (windows.HWND, bool) {
	rects, handles := e.tiledRects(subjects)
	return neighbourInDirection(rects, handles, from, dir)
}

// MoveInDirection swaps the focused window with its neighbour in a direction, and
// re-tiles the set.
//
// The swap is in the order the windows are laid out in, so the result is the layout
// you would draw by hand: the focused window takes its neighbour's place and
// everything else moves to fill the gap.
func (e *Engine) MoveInDirection(subjects []WindowSubject, focused windows.HWND, dir Direction) bool {
	rects, handles := e.tiledRects(subjects)

	target, ok := neighbourInDirection(rects, handles, focused, dir)
	if !ok {
		return false
	}

	focusIdx := indexOf(handles, focused)
	targetIdx := indexOf(handles, target)
	if focusIdx < 0 || targetIdx < 0 {
		return false
	}

	subjects[focusIdx], subjects[targetIdx] = subjects[targetIdx], subjects[focusIdx]
	e.TileSubjects(subjects)
	return true
}

// tiledRects returns the rectangles the given subjects would be given, and the
// windows they belong to.
//
// Only the tiled windows count. A floating window is not part of the layout, so it
// is not a neighbour of anything and moving a window past it means nothing.
func (e *Engine) tiledRects(subjects []WindowSubject) ([]Rect, []windows.HWND) {
	placements := e.Decide(subjects)

	rects := make([]Rect, 0, len(placements))
	handles := make([]windows.HWND, 0, len(placements))
	for _, p := range placements {
		if p.Action != ActionTile {
			continue
		}
		rects = append(rects, p.Rect)
		handles = append(handles, p.HWnd)
	}
	return rects, handles
}

// neighbourInDirection finds the nearest window in a direction.
func neighbourInDirection(rects []Rect, handles []windows.HWND, from windows.HWND, dir Direction) (windows.HWND, bool) {
	focusIdx := indexOf(handles, from)
	if focusIdx < 0 {
		return 0, false
	}
	focus := rects[focusIdx]

	best := -1
	bestDistance := int64(math.MaxInt64)

	for i, r := range rects {
		if i == focusIdx {
			continue
		}
		if !overlapsPerpendicular(focus, r, dir) {
			continue
		}

		distance := int64(0)
		switch dir {
		case DirectionLeft:
			if r.X+r.Width > focus.X {
				continue
			}
			distance = int64(focus.X - (r.X + r.Width))
		case DirectionRight:
			if r.X < focus.X+focus.Width {
				continue
			}
			distance = int64(r.X - (focus.X + focus.Width))
		case DirectionUp:
			if r.Y+r.Height > focus.Y {
				continue
			}
			distance = int64(focus.Y - (r.Y + r.Height))
		case DirectionDown:
			if r.Y < focus.Y+focus.Height {
				continue
			}
			distance = int64(r.Y - (focus.Y + focus.Height))
		default:
			continue
		}

		if distance < bestDistance {
			bestDistance = distance
			best = i
		}
	}

	if best < 0 {
		return 0, false
	}
	return handles[best], true
}

// overlapsPerpendicular reports whether two rectangles overlap on the axis
// perpendicular to a direction.
func overlapsPerpendicular(a, b Rect, dir Direction) bool {
	switch dir {
	case DirectionLeft, DirectionRight:
		return a.Y < b.Y+b.Height && b.Y < a.Y+a.Height
	case DirectionUp, DirectionDown:
		return a.X < b.X+b.Width && b.X < a.X+a.Width
	}
	return false
}

// indexOf finds a window in a list.
func indexOf(handles []windows.HWND, hwnd windows.HWND) int {
	for i, h := range handles {
		if h == hwnd {
			return i
		}
	}
	return -1
}
