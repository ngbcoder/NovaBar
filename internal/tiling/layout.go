package tiling

// Rect defines a 2D bounding box with integer coordinates.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Gaps defines outer and inner margin spacing between windows.
type Gaps struct {
	Outer int `json:"outer"`
	Inner int `json:"inner"`
}

// LayoutBSP recursively splits rectangles along alternating or best-fit axes.
func LayoutBSP(bounds Rect, count int, gaps Gaps) []Rect {
	if count <= 0 {
		return nil
	}
	if count == 1 {
		return []Rect{applyOuterGaps(bounds, gaps.Outer)}
	}

	effective := applyOuterGaps(bounds, gaps.Outer)
	rects := make([]Rect, 0, count)
	bspSplit(effective, count, gaps.Inner, true, &rects)
	return rects
}

func bspSplit(area Rect, remaining int, innerGap int, splitVertical bool, out *[]Rect) {
	if remaining == 1 {
		*out = append(*out, area)
		return
	}

	leftCount := remaining / 2
	rightCount := remaining - leftCount

	if splitVertical {
		availableW := area.Width - innerGap
		if availableW < 0 {
			availableW = 0
		}
		w1 := (availableW * leftCount) / remaining
		w2 := availableW - w1

		r1 := Rect{X: area.X, Y: area.Y, Width: w1, Height: area.Height}
		r2 := Rect{X: area.X + w1 + innerGap, Y: area.Y, Width: w2, Height: area.Height}

		bspSplit(r1, leftCount, innerGap, !splitVertical, out)
		bspSplit(r2, rightCount, innerGap, !splitVertical, out)
	} else {
		availableH := area.Height - innerGap
		if availableH < 0 {
			availableH = 0
		}
		h1 := (availableH * leftCount) / remaining
		h2 := availableH - h1

		r1 := Rect{X: area.X, Y: area.Y, Width: area.Width, Height: h1}
		r2 := Rect{X: area.X, Y: area.Y + h1 + innerGap, Width: area.Width, Height: h2}

		bspSplit(r1, leftCount, innerGap, !splitVertical, out)
		bspSplit(r2, rightCount, innerGap, !splitVertical, out)
	}
}

// LayoutColumns splits the work area into equal vertical columns.
func LayoutColumns(bounds Rect, count int, gaps Gaps) []Rect {
	if count <= 0 {
		return nil
	}
	effective := applyOuterGaps(bounds, gaps.Outer)
	if count == 1 {
		return []Rect{effective}
	}

	totalInner := (count - 1) * gaps.Inner
	availableWidth := effective.Width - totalInner
	if availableWidth < 0 {
		availableWidth = 0
	}

	baseWidth := availableWidth / count
	remainder := availableWidth % count

	rects := make([]Rect, count)
	currentX := effective.X

	for i := 0; i < count; i++ {
		w := baseWidth
		if i < remainder {
			w++
		}
		rects[i] = Rect{
			X:      currentX,
			Y:      effective.Y,
			Width:  w,
			Height: effective.Height,
		}
		currentX += w + gaps.Inner
	}

	return rects
}

// LayoutRows splits the work area into equal horizontal rows.
func LayoutRows(bounds Rect, count int, gaps Gaps) []Rect {
	if count <= 0 {
		return nil
	}
	effective := applyOuterGaps(bounds, gaps.Outer)
	if count == 1 {
		return []Rect{effective}
	}

	totalInner := (count - 1) * gaps.Inner
	availableHeight := effective.Height - totalInner
	if availableHeight < 0 {
		availableHeight = 0
	}

	baseHeight := availableHeight / count
	remainder := availableHeight % count

	rects := make([]Rect, count)
	currentY := effective.Y

	for i := 0; i < count; i++ {
		h := baseHeight
		if i < remainder {
			h++
		}
		rects[i] = Rect{
			X:      effective.X,
			Y:      currentY,
			Width:  effective.Width,
			Height: h,
		}
		currentY += h + gaps.Inner
	}

	return rects
}

// LayoutMonocle maximizes each window within the outer gaps.
func LayoutMonocle(bounds Rect, count int, gaps Gaps) []Rect {
	if count <= 0 {
		return nil
	}
	effective := applyOuterGaps(bounds, gaps.Outer)
	rects := make([]Rect, count)
	for i := 0; i < count; i++ {
		rects[i] = effective
	}
	return rects
}

// LayoutGrid arranges windows into a near-square grid, choosing the column count from
// the aspect ratio of bounds so cells come out roughly square.
//
// Reading order is left-to-right then top-to-bottom.
func LayoutGrid(bounds Rect, count int, gaps Gaps) []Rect {
	if count <= 0 {
		return nil
	}
	effective := applyOuterGaps(bounds, gaps.Outer)
	return LayoutGridCols(effective, count, gaps, autoColumns(effective, count))
}

// LayoutGridCols arranges windows into a grid with an explicit column count.
//
// cols <= 0 means auto-select. cols above the window count is clamped so no empty
// column is emitted. bounds is used as-is; outer gaps are the caller's business, so
// pass an already-inset rectangle.
func LayoutGridCols(bounds Rect, count int, gaps Gaps, cols int) []Rect {
	if count <= 0 {
		return nil
	}

	if cols <= 0 {
		cols = autoColumns(bounds, count)
	}
	if cols > count {
		cols = count
	}
	if cols < 1 {
		cols = 1
	}
	rows := (count + cols - 1) / cols

	// splitExtent tracks one axis only: the returned Rect carries the track origin in
	// X and the track size in Width, regardless of which screen axis is being divided.
	tracksX := splitExtent(bounds.X, bounds.Width, cols, gaps.Inner)
	tracksY := splitExtent(bounds.Y, bounds.Height, rows, gaps.Inner)

	rects := make([]Rect, 0, count)
	for i := 0; i < count; i++ {
		r := i / cols
		c := i % cols
		rects = append(rects, Rect{
			X:      tracksX[c].X,
			Y:      tracksY[r].X,
			Width:  tracksX[c].Width,
			Height: tracksY[r].Width,
		})
	}
	return rects
}

// autoColumns picks a column count that keeps grid cells close to square.
func autoColumns(bounds Rect, count int) int {
	if count <= 1 || bounds.Width <= 0 || bounds.Height <= 0 {
		if count < 1 {
			return 1
		}
		return count
	}

	best := 1
	bestDeviation := -1.0
	for cols := 1; cols <= count; cols++ {
		rows := float64(count) / float64(cols)
		cellW := float64(bounds.Width) / float64(cols)
		cellH := float64(bounds.Height) / rows
		deviation := cellW/cellH - 1.0
		if deviation < 0 {
			deviation = -deviation
		}
		if bestDeviation < 0 || deviation < bestDeviation {
			best = cols
			bestDeviation = deviation
		}
	}
	return best
}

// splitExtent divides one axis into n tracks of near-equal size separated by gap,
// returning the origin and size of each track.
func splitExtent(origin, extent, n, gap int) []Rect {
	if n <= 0 {
		return nil
	}
	totalGap := (n - 1) * gap
	available := extent - totalGap
	if available < 0 {
		available = 0
	}

	base := available / n
	remainder := available % n

	tracks := make([]Rect, n)
	cursor := origin
	for i := 0; i < n; i++ {
		size := base
		if i < remainder {
			size++
		}
		tracks[i] = Rect{X: cursor, Y: 0, Width: size, Height: 0}
		cursor += size + gap
	}
	return tracks
}

func applyOuterGaps(r Rect, outer int) Rect {
	x := r.X + outer
	y := r.Y + outer
	w := r.Width - (outer * 2)
	h := r.Height - (outer * 2)
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return Rect{X: x, Y: y, Width: w, Height: h}
}
