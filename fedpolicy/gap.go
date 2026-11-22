package fedpolicy

import "sort"

// findGap returns a target path under prefix with no old preimage across any
// channel feeding the prefix's authority, together with one old trajectory
// reaching the image boundary. ok is false when the whole prefix language is
// covered.
//
// The candidate prefix itself is checked first (shortest possible gap). If
// it has a preimage, the search extends it by exactly one output symbol at a
// time over the finite output alphabet. Repeating until the subset
// construction either goes empty or repeats a configuration decides the
// question for the entire unbounded prefix language: once a subset repeats,
// every further extension stays inside the image language.
func findGap(prefix NamePrefix, mapper *Mapper) ([]string, Trace, Name, bool) {
	channels := mapper.ChannelsForNew(prefix.Header())
	nfas := make([]*outputNFA, len(channels))
	symbolSet := map[string]bool{}
	for i, ch := range channels {
		nfas[i] = ch.Transducer.toOutputNFA()
		for _, s := range nfas[i].allSymbols {
			symbolSet[s] = true
		}
	}
	alphabet := make([]string, 0, len(symbolSet))
	for s := range symbolSet {
		alphabet = append(alphabet, s)
	}
	sort.Strings(alphabet)

	// Phase 1: spell the fixed prefix deterministically. A non-accepting
	// subset at the end means the prefix word itself already has no preimage.
	startStates := map[int]bool{}
	for _, nfa := range nfas {
		for q := range nfa.epsilonClose(map[int]bool{nfa.start: true}) {
			startStates[q] = true
		}
	}
	states := startStates
	for _, sym := range prefix.Path {
		states = stepAll(nfas, states, sym)
	}
	if !anyAccept(states, nfas) {
		trace, boundaryOld := recoverBoundary(channels, prefix.Path)
		return clonePath(prefix.Path), trace, boundaryOld, true
	}

	// Phase 2: explore one-symbol extensions while configurations stay fresh.
	type extNode struct {
		states map[int]bool
		word   []string
	}
	visited := map[string]bool{subsetKey(states): true}
	frontier := []extNode{{states: states, word: clonePath(prefix.Path)}}
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		for _, sym := range alphabet {
			nextStates := stepAll(nfas, cur.states, sym)
			nextWord := append(append([]string{}, cur.word...), sym)
			if !anyAccept(nextStates, nfas) {
				trace, boundaryOld := recoverBoundary(channels, nextWord)
				return nextWord, trace, boundaryOld, true
			}
			key := subsetKey(nextStates)
			if visited[key] {
				continue
			}
			visited[key] = true
			frontier = append(frontier, extNode{states: nextStates, word: nextWord})
		}
	}
	return nil, nil, Name{}, false
}

// stepAll advances every feeding NFA one symbol and closes epsilon moves.
func stepAll(nfas []*outputNFA, states map[int]bool, sym string) map[int]bool {
	nextStates := map[int]bool{}
	for _, nfa := range nfas {
		stepped := map[int]bool{}
		for q := range states {
			for _, r := range nfa.bySymbol[sym][q] {
				stepped[r] = true
			}
		}
		for q := range nfa.epsilonClose(stepped) {
			nextStates[q] = true
		}
	}
	return nextStates
}

// subsetKey serializes an NFA subset for cycle detection.
func subsetKey(states map[int]bool) string {
	values := make([]int, 0, len(states))
	for q := range states {
		values = append(values, q)
	}
	sort.Ints(values)
	key := make([]byte, 0, len(values)*2)
	for _, q := range values {
		key = append(key, byte(q), ',')
	}
	return string(key)
}

// syntheticGapWitness builds evidence for a candidate whose authority has no
// mapping channel at all. The shortest target identity is the prefix itself,
// which already has no preimage.
func syntheticGapWitness(c Candidate) Witness {
	return Witness{
		Target: Name{
			Issuer: c.Prefix.Issuer,
			Tenant: c.Prefix.Tenant,
			Path:   clonePath(c.Prefix.Path),
		},
		MatchedRule: -1,
		Effect:      Deny,
	}
}

// anyAccept reports whether any NFA in the product subset is accepting.
func anyAccept(states map[int]bool, nfas []*outputNFA) bool {
	for _, nfa := range nfas {
		for q := range states {
			if nfa.final[q] {
				return true
			}
		}
	}
	return false
}

// recoverBoundary finds one concrete old principal whose transduction
// produces the longest prefix of the gap word, giving a real mapping
// trajectory right up to the point the image language runs out.
func recoverBoundary(channels []*Channel, gap []string) (Trace, Name) {
	for length := len(gap); length >= 0; length-- {
		prefix := gap[:length]
		for _, ch := range channels {
			if oldPath, trace, ok := shortestPreimage(ch.Transducer, prefix); ok {
				return trace, Name{
					Issuer: ch.Old.Issuer,
					Tenant: ch.Old.Tenant,
					Path:   oldPath,
				}
			}
		}
	}
	return nil, Name{}
}

// shortestPreimage returns the shortest input path and one trace that
// transduces to the requested output, or ok=false when none exists. It is
// itself a bounded BFS over (transducer state, output position), so it
// terminates even for cyclic transducers.
func shortestPreimage(t *FST, output []string) ([]string, Trace, bool) {
	type pnode struct {
		state   int
		pos     int
		trace   Trace
		oldPath []string
	}
	type pkey struct {
		state int
		pos   int
	}
	root := pnode{state: t.Start}
	visited := map[pkey]bool{{t.Start, 0}: true}
	frontier := []pnode{root}

	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		if cur.pos == len(output) && t.Final[cur.state] {
			return append([]string(nil), cur.oldPath...), append(Trace(nil), cur.trace...), true
		}
		for _, arc := range t.arcsByState[cur.state] {
			nextPos := cur.pos
			if arc.Out != "" {
				if cur.pos >= len(output) || arc.Out != output[cur.pos] {
					continue
				}
				nextPos++
			}
			next := pnode{
				state:   arc.To,
				pos:     nextPos,
				trace:   append(append(Trace(nil), cur.trace...), TraceStep{arc.From, arc.In, arc.Out, arc.To}),
				oldPath: append(append([]string{}, cur.oldPath...), arc.In),
			}
			k := pkey{next.state, next.pos}
			if visited[k] {
				continue
			}
			visited[k] = true
			frontier = append(frontier, next)
		}
	}
	return nil, nil, false
}

// reconstructTrace walks product parent pointers to recover the full
// transducer trace of an accepting product node. The old input path is kept
// explicitly on each node because the product state only stores a bounded
// tail sufficient for policy/label evaluation.
func reconstructTrace(node *productNode) Trace {
	var steps Trace
	for node.parent != nil {
		steps = append(steps, TraceStep{node.arc.From, node.arc.In, node.arc.Out, node.arc.To})
		node = node.parent
	}
	reverseSteps(steps)
	return steps
}

// traceOutput returns the output tape of a trace, skipping epsilon (dropped)
// steps.
func traceOutput(trace Trace) []string {
	var out []string
	for _, step := range trace {
		if step.Out != "" {
			out = append(out, step.Out)
		}
	}
	return out
}

func reverseSteps(s Trace) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// pushTail keeps at most maxDepth of the most recent input segments in a
// compact string, using NUL as a segment delimiter (path segments are
// non-empty abstract symbols). tailLen reports the number of segments held,
// which can be smaller than the real depth near the root.
func pushTail(tail string, tailLen int, segment string, maxDepth int) (string, int) {
	if maxDepth == 0 {
		return "", 0
	}
	next := tail + "\x00" + segment
	nextLen := tailLen + 1
	if nextLen <= maxDepth {
		return next, nextLen
	}
	// Drop the oldest segment, restoring the bounded window.
	drop := 0
	for drop < len(next) && next[drop] != 0 {
		drop++
	}
	if drop < len(next) {
		drop++ // skip the delimiter itself
	}
	return next[drop:], maxDepth
}

// tailPath decodes a bounded tail produced by pushTail.
func tailPath(tail string, tailLen int) []string {
	if tailLen == 0 || tail == "" {
		return nil
	}
	var path []string
	start := 0
	for i := 0; i <= len(tail); i++ {
		if i == len(tail) || tail[i] == 0 {
			path = append(path, tail[start:i])
			start = i + 1
		}
	}
	return path
}

// joinPath renders a path with NUL separators for stable map keys.
func joinPath(path []string) string {
	out := ""
	for i, seg := range path {
		if i > 0 {
			out += "\x00"
		}
		out += seg
	}
	return out
}

// pushString keeps at most window of the most recent segments in a compact
// NUL-delimited string. It is the string analogue of pushTail and bounds the
// "output past the candidate prefix" component of a product state.
func pushString(window, segment string, maxWindow int) string {
	if maxWindow == 0 {
		return ""
	}
	next := window + "\x00" + segment
	if countSegments(next) <= maxWindow {
		return next
	}
	drop := 0
	for drop < len(next) && next[drop] != 0 {
		drop++
	}
	if drop < len(next) {
		drop++
	}
	return next[drop:]
}

// countSegments counts NUL-delimited segments in a pushString window.
func countSegments(window string) int {
	if window == "" {
		return 0
	}
	count := 1
	for i := 0; i < len(window); i++ {
		if window[i] == 0 {
			count++
		}
	}
	return count
}
