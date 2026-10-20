package tiling

import "math"

// The zone grid.
//
// A zone is one cell of a grid laid over the work area. The window the user picks
// takes that cell and the rest of the windows fill what is left, which is what the
// snap layouts on Windows do and what a tiling shell has always meant by resizing a
// window: not a free rectangle the user drags, but a share of the screen that the
// layout then respects.

// ZoneGridShape returns the shape of a zone grid holding a number of cells.
//
// The shapes are the ones people already have in their heads: halves, thirds,
// quarters. A grid of two cells is two halves, four is the four quarters, and the
// odd numbers are a single row, because a row of thirds is a thing people use and a
// column of thirds is not.
func ZoneGridShape(cells int) (cols int, rows int) {
	switch cells {
	case 1:
		return 1, 1
	case 2:
		return 2, 1
	case 3:
		return 3, 1
	case 4:
		return 2, 2
	case 6:
		return 3, 2
	case 9:
		return 3, 3
	case 12:
		return 4, 3
	case 16:
		return 4, 4
	case 20:
		return 5, 4
	}

	cols = int(math.Ceil(math.Sqrt(float64(cells))))
	if cols < 1 {
		cols = 1
	}
	rows = (cells + cols - 1) / cols
	return cols, rows
}

// ZoneCells returns the number of cells in a zone grid for a number of windows.
//
// The grid holds the windows, so a picker full of empty cells would be a picker
// full of choices that lead nowhere. The count is rounded up to a shape that
// divides evenly.
func ZoneCells(windows int) int {
	if windows <= 1 {
		return 1
	}
	for _, cells := range []int{2, 3, 4, 6, 9, 12, 16, 20} {
		if windows <= cells {
			return cells
		}
	}

	cols, rows := ZoneGridShape(windows)
	return cols * rows
}

// ZoneCellRects returns the cells of a zone grid, in reading order.
//
// The order is what a cell index means: the cell in the top left is zero, and the
// layout and the picker have to agree on it.
func ZoneCellRects(bounds Rect, count int, gaps Gaps) []Rect {
	cells := ZoneCells(count)
	cols, rows := ZoneGridShape(cells)
	if rows*cols < count {
		// The shape cannot hold them all, which only happens when the count is
		// above the grid's size, so the grid grows a row.
		rows = (count + cols - 1) / cols
	}

	effective := applyOuterGaps(bounds, gaps.Outer)

	tracksX := splitExtent(effective.X, effective.Width, cols, gaps.Inner)
	tracksY := splitExtent(effective.Y, effective.Height, rows, gaps.Inner)

	out := make([]Rect, 0, cols*rows)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			out = append(out, Rect{
				X:      tracksX[c].X,
				Y:      tracksY[r].X,
				Width:  tracksX[c].Width,
				Height: tracksY[r].Width,
			})
		}
	}
	return out
}

// LayoutZones lays the windows out into a zone grid, with the first window taking a
// chosen cell and the rest filling the remaining cells in order.
//
// The reserved cell is where the window the user picked goes. The caller puts that
// window first, so the picked cell is always the one the pointer chose, whichever
// window happened to be focused.
//
// The grid is divided evenly, so a zone is a share of the screen rather than a
// rectangle the user dragged: a tiling shell that lets a window be resized by hand
// has two sources of truth for where its windows are, and the layout always wins.
func LayoutZones(bounds Rect, count int, gaps Gaps, reserved int) []Rect {
	if count <= 0 {
		return nil
	}

	cellsRects := ZoneCellRects(bounds, count, gaps)
	if len(cellsRects) == 0 {
		return nil
	}

	if reserved < 0 || reserved >= len(cellsRects) {
		reserved = 0
	}

	out := make([]Rect, 0, count)
	out = append(out, cellsRects[reserved])
	for i, r := range cellsRects {
		if i == reserved {
			continue
		}
		if len(out) >= count {
			break
		}
		out = append(out, r)
	}
	return out
}
