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
