package tmplmatch

import "math"

// eps guards against zero-variance windows (flat regions), where NCC is
// undefined. Such windows score zero instead of producing NaN/Inf.
const nccEps = 1e-12

// nccEps2 is used under the square root of the energy product.
const nccEps2 = 1e-20

// templateStats holds the precomputed zero-mean template and its energy.
// For masked templates only valid pixels contribute.
type templateStats struct {
	tz      []float64 // zero-mean template values, 0 outside the mask
	mean    float64
	energy  float64 // sum of tz^2 over valid pixels
	offsets []int   // valid pixel offsets (nil when fully valid)
}

func buildTemplateStats(t *GrayImage) templateStats {
	n := t.Width * t.Height
	ts := templateStats{tz: make([]float64, n)}
	sum := 0.0
	if t.Mask != nil {
		ts.offsets = make([]int, 0, t.ValidCount)
		for i := 0; i < n; i++ {
			if t.Mask[i] {
				sum += t.Pix[i]
				ts.offsets = append(ts.offsets, i)
			}
		}
		ts.mean = sum / float64(t.ValidCount)
		for _, i := range ts.offsets {
			d := t.Pix[i] - ts.mean
			ts.tz[i] = d
			ts.energy += d * d
		}
		return ts
	}
	for i := 0; i < n; i++ {
		sum += t.Pix[i]
	}
	ts.mean = sum / float64(n)
	for i := 0; i < n; i++ {
		d := t.Pix[i] - ts.mean
		ts.tz[i] = d
		ts.energy += d * d
	}
	return ts
}

// scoreAt computes masked or plain zero-mean NCC of t over img at top-left
// (x, y) by direct summation. It is the reference implementation used by the
// brute-force matcher and for candidate refinement on masked templates.
func scoreAt(img, t *GrayImage, ts templateStats, x, y int) float64 {
	tw, th := t.Width, t.Height
	if ts.offsets == nil {
		// Plain path: every pixel participates.
		n := tw * th
		sumI, sumI2, cross := 0.0, 0.0, 0.0
		base := y*img.Width + x
		for row := 0; row < th; row++ {
			ir := base + row*img.Width
			tr := row * tw
			for col := 0; col < tw; col++ {
				iv := img.Pix[ir+col]
				sumI += iv
				sumI2 += iv * iv
				cross += ts.tz[tr+col] * iv
			}
		}
		energyI := sumI2 - sumI*sumI/float64(n)
		return normalizeNCC(cross, ts.energy, energyI)
	}
	// Masked path: statistics use only the valid template pixels.
	n := float64(t.ValidCount)
	sumI, sumI2, cross := 0.0, 0.0, 0.0
	for _, off := range ts.offsets {
		px := x + off%tw
		py := y + off/tw
		iv := img.Pix[py*img.Width+px]
		sumI += iv
		sumI2 += iv * iv
		cross += ts.tz[off] * iv
	}
	energyI := sumI2 - sumI*sumI/n
	return normalizeNCC(cross, ts.energy, energyI)
}

func normalizeNCC(cross, energyT, energyI float64) float64 {
	if energyT <= nccEps || energyI <= nccEps {
		return 0
	}
	s := cross / math.Sqrt(energyT*energyI+nccEps2)
	if s > 1 {
		s = 1
	} else if s < -1 {
		s = -1
	}
	return s
}

// scanIntegral evaluates NCC at every legal top-left position using integral
// images for the image window statistics (O(1) per position) plus a sliding
// dot product for the cross term. Only positions scoring at least threshold
// are returned, at most keep strongest. It supports fully valid templates
// only; masked templates must use scanBrute.
func scanIntegral(img *GrayImage, ii *IntegralImage, t *GrayImage, ts templateStats, step int, threshold float64, keep int) []Candidate {
	tw, th := t.Width, t.Height
	maxX := img.Width - tw
	maxY := img.Height - th
	if maxX < 0 || maxY < 0 {
		return nil
	}
	n := float64(tw * th)
	top := newTopK(keep)
	for y := 0; y <= maxY; y += step {
		for x := 0; x <= maxX; x += step {
			sumI := ii.rectSum(ii.S1, x, y, tw, th)
			sumI2 := ii.rectSum(ii.S2, x, y, tw, th)
			energyI := sumI2 - sumI*sumI/n
			if energyI <= nccEps {
				continue
			}
			cross := 0.0
			base := y*img.Width + x
			for row := 0; row < th; row++ {
				ir := base + row*img.Width
				tr := row * tw
				for col := 0; col < tw; col++ {
					cross += ts.tz[tr+col] * img.Pix[ir+col]
				}
			}
			s := normalizeNCC(cross, ts.energy, energyI)
			if s >= threshold {
				top.push(Candidate{X: x, Y: y, Score: s})
			}
		}
	}
	return top.snapshot()
}

// scanBrute evaluates NCC at every legal position by direct summation. It
// supports transparent masks and serves as the full-resolution reference.
// Only positions scoring at least threshold are returned, at most keep
// strongest (keep <= 0 means unlimited).
func scanBrute(img, t *GrayImage, ts templateStats, step int, threshold float64, keep int) []Candidate {
	tw, th := t.Width, t.Height
	maxX := img.Width - tw
	maxY := img.Height - th
	if maxX < 0 || maxY < 0 {
		return nil
	}
	top := newTopK(keep)
	for y := 0; y <= maxY; y += step {
		for x := 0; x <= maxX; x += step {
			s := scoreAt(img, t, ts, x, y)
			if s >= threshold {
				c := Candidate{X: x, Y: y, Score: s}
				top.push(c)
			}
		}
	}
	return top.snapshot()
}

// topK is a fixed-capacity binary min-heap retaining the strongest
// candidates seen. It is implemented without interface boxing so pushing is
// a cheap O(log keep) value operation on a preallocated array. A limit <= 0
// selects an uncapped append mode used by tests.
type topK struct {
	items []Candidate
	limit int
}

func newTopK(limit int) *topK {
	if limit <= 0 {
		return &topK{limit: 0}
	}
	return &topK{items: make([]Candidate, 0, limit), limit: limit}
}

func (h *topK) push(c Candidate) {
	if h.limit <= 0 {
		h.items = append(h.items, c)
		return
	}
	if len(h.items) < h.limit {
		h.items = append(h.items, c)
		// Sift up: items[0] is the minimum of the retained set.
		i := len(h.items) - 1
		for i > 0 {
			parent := (i - 1) / 2
			if h.items[parent].Score <= h.items[i].Score {
				break
			}
			h.items[parent], h.items[i] = h.items[i], h.items[parent]
			i = parent
		}
		return
	}
	if c.Score <= h.items[0].Score {
		return
	}
	// Replace the weakest retained candidate and sift down.
	h.items[0] = c
	i := 0
	for {
		left, right, smallest := 2*i+1, 2*i+2, i
		if left < h.limit && h.items[left].Score < h.items[smallest].Score {
			smallest = left
		}
		if right < h.limit && h.items[right].Score < h.items[smallest].Score {
			smallest = right
		}
		if smallest == i {
			break
		}
		h.items[i], h.items[smallest] = h.items[smallest], h.items[i]
		i = smallest
	}
}

// snapshot returns retained candidates sorted strongest first.
func (h *topK) snapshot() []Candidate {
	out := make([]Candidate, len(h.items))
	copy(out, h.items)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Score > out[j-1].Score; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
