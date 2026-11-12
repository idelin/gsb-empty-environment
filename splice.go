// Package taskpkg provides sample-accurate splicing of multi-channel PCM
// material. For every supplied clip point it searches a window of candidate
// boundaries, keeps every fragment above its minimum length, avoids overlap of
// adjacent crossfade regions, and joins the chosen boundaries with
// equal-power sine/cosine crossfades. Every output frame carries a map back to
// the source input samples and their mix gains.
package taskpkg

import (
	"errors"
	"math"
)

// Cut describes one splice point in input frame coordinates.
type Cut struct {
	// At is the nominal clip point: the frame where the left fragment ends
	// and the right fragment begins, before boundary adjustment.
	At int
	// Fade is the crossfade length in frames. The crossfade region occupies
	// Fade source frames spread on both sides of the boundary; the emitted
	// region is ceil(Fade/2) output frames long (Fade == 0 is a hard cut).
	Fade int
	// Window is the half-width of the symmetric search window around At:
	// the solver may move the boundary at most Window frames either way.
	Window int
}

// Options controls global splice behaviour.
type Options struct {
	// MinSegmentLength is the minimum number of plain (non-crossfaded) frames
	// that every fragment must contain, including the head and the tail.
	MinSegmentLength int
}

// Boundary is the solver-selected location of one cut.
type Boundary struct {
	// CutIndex identifies the originating Cut in the input order.
	CutIndex int
	// At is the nominal clip point.
	At int
	// Boundary is the selected splice boundary in input frame coordinates.
	Boundary int
	// Offset equals Boundary - At.
	Offset int
	// Fade is the effective crossfade length in frames.
	Fade int
	// MaxJump is the largest per-channel absolute level step at Boundary,
	// i.e. max_c |pcm[c][Boundary] - pcm[c][Boundary-1]|.
	MaxJump float64
}

// SourceRef identifies one contributing input sample inside an output frame.
type SourceRef struct {
	// Frame is the input frame index.
	Frame int
	// Channel is the input channel index.
	Channel int
	// Gain is the per-sample mix gain: 1 on plain segments, a window value
	// inside a crossfade, 0 for an inactive side.
	Gain float64
}

// Mapping is the source-sample map of a single output frame.
type Mapping struct {
	// OutputFrame is the output frame index.
	OutputFrame int
	// Sources holds one entry per output channel; each entry lists the one
	// (plain segment) or two (crossfade) input samples producing that channel.
	Sources [][]SourceRef
}

// Result is the splice output.
type Result struct {
	// PCM is planar output data: PCM[channel][frame].
	PCM [][]float64
	// Boundaries are the solver decisions in cut order.
	Boundaries []Boundary
	// Mapping maps every output frame back to its source input samples.
	Mapping []Mapping
}

var (
	// ErrNoPCM is returned when the input carries no samples.
	ErrNoPCM = errors.New("taskpkg: empty PCM input")
	// ErrRaggedPCM is returned when channels do not share one length.
	ErrRaggedPCM = errors.New("taskpkg: channels have different lengths")
	// ErrInvalidCut is returned for out-of-range, negative or unsorted cuts.
	ErrInvalidCut = errors.New("taskpkg: invalid cut specification")
	// ErrInfeasible is returned when no boundary placement satisfies the
	// minimum-length and crossfade non-overlap constraints simultaneously.
	ErrInfeasible = errors.New("taskpkg: no feasible splice placement")
)

// Splice splices planar multi-channel PCM at the given clip points. All
// channels share one boundary per cut, so multichannel material stays
// sample-synchronised.
func Splice(pcm [][]float64, cuts []Cut, opts Options) (*Result, error) {
	frames, err := validate(pcm, cuts, opts)
	if err != nil {
		return nil, err
	}

	if len(cuts) == 0 {
		if opts.MinSegmentLength > frames {
			return nil, ErrInfeasible
		}
		return buildOutput(pcm, nil), nil
	}

	boundaries, err := solveBoundaries(pcm, cuts, opts.MinSegmentLength, frames)
	if err != nil {
		return nil, err
	}

	return buildOutput(pcm, boundaries), nil
}

func validate(pcm [][]float64, cuts []Cut, opts Options) (int, error) {
	if len(pcm) == 0 || len(pcm[0]) == 0 {
		return 0, ErrNoPCM
	}
	frames := len(pcm[0])
	for _, ch := range pcm[1:] {
		if len(ch) != frames {
			return 0, ErrRaggedPCM
		}
	}
	if opts.MinSegmentLength < 0 {
		return 0, ErrInvalidCut
	}
	prevAt := -1
	for _, cut := range cuts {
		if cut.At <= 0 || cut.At >= frames || cut.Fade < 0 || cut.Window < 0 {
			return 0, ErrInvalidCut
		}
		if cut.At <= prevAt {
			return 0, ErrInvalidCut
		}
		prevAt = cut.At
	}
	return frames, nil
}

// fadeHalf returns how many frames the crossfade of length fade occupies on
// each side of the boundary. Odd lengths round up, so 2*fadeHalf >= fade and
// the crossfade always emits fadeHalf output frames.
func fadeHalf(fade int) int {
	return (fade + 1) / 2
}

// maxJump is the largest per-channel absolute amplitude step at boundary.
// The virtual samples outside the buffer are zero, so boundary positions at
// the very edges measure the step against silence.
func maxJump(pcm [][]float64, boundary int) float64 {
	largest := 0.0
	for _, ch := range pcm {
		var left, right float64
		if boundary > 0 {
			left = ch[boundary-1]
		}
		if boundary < len(ch) {
			right = ch[boundary]
		}
		if d := math.Abs(right - left); d > largest {
			largest = d
		}
	}
	return largest
}

type stepCost struct {
	jump float64
	dist int
	off  int
}

// dpNode is one candidate boundary in one DP layer.
type dpNode struct {
	boundary int
	cost     stepCost
	prev     *dpNode
}

func lessCost(a, b stepCost) bool {
	if a.jump != b.jump {
		return a.jump < b.jump
	}
	if a.dist != b.dist {
		return a.dist < b.dist
	}
	return a.off < b.off
}

// chain returns the cost vector of a node from the first cut to its own.
func chain(n *dpNode) []stepCost {
	var costs []stepCost
	for ; n != nil; n = n.prev {
		costs = append(costs, n.cost)
	}
	for i, j := 0, len(costs)-1; i < j; i, j = i+1, j-1 {
		costs[i], costs[j] = costs[j], costs[i]
	}
	return costs
}

// better reports whether candidate dominates best under the global
// lexicographic order: the earliest cut decides first, each cut by maximum
// channel jump, then window movement distance, then signed offset.
func better(candidate, best *dpNode) bool {
	cs := chain(candidate)
	bs := chain(best)
	for k := range cs {
		if lessCost(cs[k], bs[k]) {
			return true
		}
		if lessCost(bs[k], cs[k]) {
			return false
		}
	}
	return false
}

func feasibleGap(leftBoundary, leftFade, rightBoundary, rightFade, minLength int) bool {
	// A region at boundary b with fade f occupies [b-fadeHalf(f),
	// b+fadeHalf(f)). Virtual regions at 0 and frames are empty, so head and
	// tail gaps are measured from their real edges.
	leftEdge := leftBoundary + fadeHalf(leftFade)
	rightEdge := rightBoundary - fadeHalf(rightFade)
	gap := rightEdge - leftEdge
	return gap >= minLength
}

func solveBoundaries(pcm [][]float64, cuts []Cut, minLength, frames int) ([]Boundary, error) {
	var prevLayer []*dpNode

	for i := range cuts {
		cut := cuts[i]
		lo := max(1, cut.At-cut.Window)
		hi := min(frames-1, cut.At+cut.Window)

		var layer []*dpNode
		for boundary := lo; boundary <= hi; boundary++ {
			// Feasibility against every retained predecessor (or the virtual
			// head boundary 0 before the first cut).
			if i == 0 {
				if !feasibleGap(0, 0, boundary, cut.Fade, minLength) {
					continue
				}
			} else {
				reachable := false
				for _, pred := range prevLayer {
					if feasibleGap(pred.boundary, cuts[i-1].Fade, boundary, cut.Fade, minLength) {
						reachable = true
						break
					}
				}
				if !reachable {
					continue
				}
			}

			node := &dpNode{
				boundary: boundary,
				cost: stepCost{
					jump: maxJump(pcm, boundary),
					dist: abs(boundary - cut.At),
					off:  boundary - cut.At,
				},
			}

			if i == 0 {
				layer = append(layer, node)
				continue
			}

			var winner *dpNode
			for _, pred := range prevLayer {
				if !feasibleGap(pred.boundary, cuts[i-1].Fade, boundary, cut.Fade, minLength) {
					continue
				}
				candidate := &dpNode{boundary: boundary, cost: node.cost, prev: pred}
				if winner == nil || better(candidate, winner) {
					winner = candidate
				}
			}
			if winner != nil {
				layer = append(layer, winner)
			}
		}

		if len(layer) == 0 {
			return nil, ErrInfeasible
		}
		prevLayer = layer
	}

	// Close the chain against the virtual tail boundary at frames.
	var terminal *dpNode
	for _, node := range prevLayer {
		last := cuts[len(cuts)-1]
		if !feasibleGap(node.boundary, last.Fade, frames, 0, minLength) {
			continue
		}
		if terminal == nil || better(node, terminal) {
			terminal = node
		}
	}
	if terminal == nil {
		return nil, ErrInfeasible
	}

	boundaries := make([]Boundary, len(cuts))
	for node, i := terminal, len(cuts)-1; node != nil; node, i = node.prev, i-1 {
		boundaries[i] = Boundary{
			CutIndex: i,
			At:       cuts[i].At,
			Boundary: node.boundary,
			Offset:   node.boundary - cuts[i].At,
			Fade:     cuts[i].Fade,
			MaxJump:  node.cost.jump,
		}
	}
	return boundaries, nil
}

// equalPowerGains returns the left (fade-out) and right (fade-in) window
// gains for position t of an n-frame crossfade. The windows are cosine and
// sine arcs of the same angle, so gLeft^2+gRight^2 == 1 everywhere.
func equalPowerGains(n, t int) (gLeft, gRight float64) {
	if n == 1 {
		g := math.Sqrt2 / 2
		return g, g
	}
	theta := math.Pi / 2 * float64(t) / float64(n-1)
	gLeft, gRight = math.Cos(theta), math.Sin(theta)
	// Snap trigonometric endpoints so plain-sample gains are exactly one.
	if t == 0 {
		return 1, 0
	}
	if t == n-1 {
		return 0, 1
	}
	return gLeft, gRight
}

func buildOutput(pcm [][]float64, boundaries []Boundary) *Result {
	channels := len(pcm)
	frames := len(pcm[0])

	halves := make([]int, len(boundaries))
	outLen := frames
	for i := range boundaries {
		halves[i] = fadeHalf(boundaries[i].Fade)
		outLen -= halves[i]
	}

	out := make([][]float64, channels)
	for c := range out {
		out[c] = make([]float64, outLen)
	}
	mapping := make([]Mapping, outLen)
	for f := range mapping {
		mapping[f].OutputFrame = f
		mapping[f].Sources = make([][]SourceRef, channels)
	}

	emitPlain := func(start, end, outAt int) int {
		for idx := start; idx < end; idx++ {
			for c := 0; c < channels; c++ {
				out[c][outAt] = pcm[c][idx]
				mapping[outAt].Sources[c] = []SourceRef{{Frame: idx, Channel: c, Gain: 1}}
			}
			outAt++
		}
		return outAt
	}

	emitFade := func(i, outAt int) int {
		boundary := boundaries[i].Boundary
		half := halves[i]
		for t := 0; t < half; t++ {
			gLeft, gRight := equalPowerGains(half, t)
			leftFrame := boundary - half + t
			rightFrame := boundary + t
			for c := 0; c < channels; c++ {
				out[c][outAt] = gLeft*pcm[c][leftFrame] + gRight*pcm[c][rightFrame]
				mapping[outAt].Sources[c] = []SourceRef{
					{Frame: leftFrame, Channel: c, Gain: gLeft},
					{Frame: rightFrame, Channel: c, Gain: gRight},
				}
			}
			outAt++
		}
		return outAt
	}

	outAt := 0
	prevBoundary := 0
	for i := range boundaries {
		plainEnd := boundaries[i].Boundary - halves[i]
		outAt = emitPlain(prevBoundary, plainEnd, outAt)
		outAt = emitFade(i, outAt)
		prevBoundary = boundaries[i].Boundary + halves[i]
	}
	outAt = emitPlain(prevBoundary, frames, outAt)

	return &Result{PCM: out, Boundaries: boundaries, Mapping: mapping}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
