package main

import "math"

type GeometryResult struct {
	Direction   string
	GapX        float64
	GapY        float64
	DeltaX      float64
	DeltaY      float64
	RawDistance float64
}

func centerX(r Rect) float64 { return r.X + r.Width/2 }
func centerY(r Rect) float64 { return r.Y + r.Height/2 }

func rangesOverlap(aStart, aEnd, bStart, bEnd float64) bool {
	return aStart < bEnd && bStart < aEnd
}

func axisGap(aStart, aEnd, bStart, bEnd float64) float64 {
	if rangesOverlap(aStart, aEnd, bStart, bEnd) {
		return 0
	}
	if bStart >= aEnd {
		return bStart - aEnd
	}
	return aStart - bEnd
}

func relativeGeometry(label, control Rect) GeometryResult {
	overlapX := rangesOverlap(label.X, label.X+label.Width, control.X, control.X+control.Width)
	overlapY := rangesOverlap(label.Y, label.Y+label.Height, control.Y, control.Y+control.Height)
	gapX := axisGap(label.X, label.X+label.Width, control.X, control.X+control.Width)
	gapY := axisGap(label.Y, label.Y+label.Height, control.Y, control.Y+control.Height)
	deltaX := centerX(control) - centerX(label)
	deltaY := centerY(control) - centerY(label)

	var direction string
	var raw float64
	switch {
	case overlapX && overlapY:
		direction = "overlap"
		raw = math.Hypot(deltaX, deltaY)
	case overlapX:
		if deltaY < 0 {
			direction = "above"
		} else {
			direction = "below"
		}
		raw = gapY + math.Abs(deltaX)
	case overlapY:
		if deltaX < 0 {
			direction = "left"
		} else {
			direction = "right"
		}
		raw = gapX + math.Abs(deltaY)
	default:
		if gapX >= gapY {
			if deltaX < 0 {
				direction = "left"
			} else {
				direction = "right"
			}
		} else {
			if deltaY < 0 {
				direction = "above"
			} else {
				direction = "below"
			}
		}
		raw = math.Hypot(gapX, gapY)
	}
	return GeometryResult{direction, gapX, gapY, deltaX, deltaY, raw}
}
