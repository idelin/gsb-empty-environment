package fedpolicy

import "sort"

// TraceStep records one transducer transition used while mapping an old
// principal. An empty Out means the input segment was dropped (an epsilon
// output), which is exactly how many old names collapse onto one new name.
type TraceStep struct {
	From int
	In   string
	Out  string
	To   int
}

// Trace is the sequence of transducer transitions behind one mapping.
type Trace []TraceStep

// Image is one transduction result: the resulting target path plus the full
// transition trace that produced it.
type Image struct {
	Out   []string
	Trace Trace
}

// Arc is one finite-state transducer transition. Every arc consumes one
// input path segment (In is always non-empty); Out may be empty to emit
// epsilon, i.e. to drop the segment. Restricting epsilon moves to the output
// tape keeps input processing deterministic-by-segment and gives every trace
// one step per old path segment, which witnesses can show verbatim.
type Arc struct {
	From int
	In   string
	Out  string
	To   int
}

// FST is a nondeterministic finite-state transducer over path segments.
// Multiple arcs may share (From, In) to model that one old name can reach
// several target names; cycles model paths of unbounded length.
type FST struct {
	Start int
	Final map[int]bool
	Arcs  []Arc

	// arcsByState groups arcs for the inner search loops.
	arcsByState map[int][]Arc
}

// NewFST builds an empty transducer rooted at start.
func NewFST(start int) *FST {
	return &FST{Start: start, Final: map[int]bool{}, arcsByState: map[int][]Arc{}}
}

// AddArc registers a transition and validates the input contract.
func (t *FST) AddArc(a Arc) {
	if a.In == "" {
		panic("fedpolicy: transducer arcs must consume an input segment")
	}
	t.Arcs = append(t.Arcs, a)
	t.arcsByState[a.From] = append(t.arcsByState[a.From], a)
}

// MarkFinal marks a state as accepting.
func (t *FST) MarkFinal(state int) { t.Final[state] = true }

type imageNode struct {
	state int
	pos   int
	out   []string
	trace Trace
}

// Images enumerates every transduction of input. Because every arc consumes
// an input symbol, the search is finite for a finite input and returns one
// Image per distinct accepting path.
func (t *FST) Images(input []string) []Image {
	root := imageNode{state: t.Start}
	frontier := []imageNode{root}
	var results []Image
	for len(frontier) > 0 {
		node := frontier[0]
		frontier = frontier[1:]
		if node.pos == len(input) {
			if t.Final[node.state] {
				results = append(results, Image{Out: clonePath(node.out), Trace: append(Trace(nil), node.trace...)})
			}
			continue
		}
		symbol := input[node.pos]
		for _, arc := range t.arcsByState[node.state] {
			if arc.In != symbol {
				continue
			}
			next := imageNode{state: arc.To, pos: node.pos + 1, out: node.out, trace: node.trace}
			if arc.Out != "" {
				next.out = append(append([]string{}, node.out...), arc.Out)
			}
			next.trace = append(append(Trace(nil), node.trace...), TraceStep{
				From: arc.From, In: arc.In, Out: arc.Out, To: arc.To,
			})
			frontier = append(frontier, next)
		}
	}
	return results
}

// outputNFA turns the transducer into an NFA over its output alphabet by
// treating empty-Out arcs as epsilon moves and ignoring consumed inputs. It
// is the automaton whose language is the set of target paths with at least
// one old preimage.
type outputNFA struct {
	start      int
	final      map[int]bool
	epsilon    map[int][]int // arcs with Out == ""
	bySymbol   map[string]map[int][]int
	allSymbols []string
}

func (t *FST) toOutputNFA() *outputNFA {
	nfa := &outputNFA{
		start:    t.Start,
		final:    t.Final,
		epsilon:  map[int][]int{},
		bySymbol: map[string]map[int][]int{},
	}
	seenSymbol := map[string]bool{}
	for _, arc := range t.Arcs {
		if arc.Out == "" {
			nfa.epsilon[arc.From] = append(nfa.epsilon[arc.From], arc.To)
			continue
		}
		if nfa.bySymbol[arc.Out] == nil {
			nfa.bySymbol[arc.Out] = map[int][]int{}
		}
		nfa.bySymbol[arc.Out][arc.From] = append(nfa.bySymbol[arc.Out][arc.From], arc.To)
		if !seenSymbol[arc.Out] {
			seenSymbol[arc.Out] = true
			nfa.allSymbols = append(nfa.allSymbols, arc.Out)
		}
	}
	sort.Strings(nfa.allSymbols)
	return nfa
}

// epsilonClose expands a set of NFA states along epsilon (empty output)
// arcs to a fixed point.
func (n *outputNFA) epsilonClose(states map[int]bool) map[int]bool {
	closed := map[int]bool{}
	stack := make([]int, 0, len(states))
	for q := range states {
		closed[q] = true
		stack = append(stack, q)
	}
	for len(stack) > 0 {
		q := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, r := range n.epsilon[q] {
			if !closed[r] {
				closed[r] = true
				stack = append(stack, r)
			}
		}
	}
	return closed
}

// HasPreimage reports whether target is in the output language, i.e. some
// input path transduces to exactly target. It is a subset-construction
// simulation of the output NFA and therefore decidable for every finite
// target word regardless of transducer cycles.
func (t *FST) HasPreimage(target []string) bool {
	nfa := t.toOutputNFA()
	current := nfa.epsilonClose(map[int]bool{nfa.start: true})
	for _, sym := range target {
		step := map[int]bool{}
		for q := range current {
			for _, r := range nfa.bySymbol[sym][q] {
				step[r] = true
			}
		}
		current = nfa.epsilonClose(step)
		if len(current) == 0 {
			return false
		}
	}
	for q := range current {
		if nfa.final[q] {
			return true
		}
	}
	return false
}

// Channel binds one old naming authority to one target authority through a
// transducer. Keeping the full (issuer, tenant) header on both sides is what
// keeps same-named tenants of different issuers apart.
type Channel struct {
	Old        HeaderKey
	New        HeaderKey
	Alphabet   []string
	Transducer *FST
}

// Mapper is the finite collection of name-mapping channels given as input.
type Mapper struct {
	Channels []*Channel

	byOld map[HeaderKey]*Channel
	byNew map[HeaderKey][]*Channel
}

// Build indexes the channels. Call once after assembling the mapper.
func (m *Mapper) Build() {
	m.byOld = map[HeaderKey]*Channel{}
	m.byNew = map[HeaderKey][]*Channel{}
	for _, ch := range m.Channels {
		m.byOld[ch.Old] = ch
		m.byNew[ch.New] = append(m.byNew[ch.New], ch)
	}
}

// ChannelForOld returns the channel rooted at oldHeader, or nil.
func (m *Mapper) ChannelForOld(oldHeader HeaderKey) *Channel { return m.byOld[oldHeader] }

// ChannelsForNew returns every channel emitting the given target authority.
// Multiple channels model the many-to-one case where several old principals
// feed the same new identity.
func (m *Mapper) ChannelsForNew(newHeader HeaderKey) []*Channel {
	channels := m.byNew[newHeader]
	out := make([]*Channel, len(channels))
	copy(out, channels)
	return out
}

// MappedImage pairs a transduction with the channel and old name that
// produced it, so callers can reconstruct full mapping trajectories.
type MappedImage struct {
	Channel *Channel
	Old     Name
	Image   Image
}

// Images maps one fully qualified old principal across its channel.
func (m *Mapper) Images(old Name) []MappedImage {
	ch := m.ChannelForOld(old.Header())
	if ch == nil {
		return nil
	}
	images := ch.Transducer.Images(old.Path)
	out := make([]MappedImage, 0, len(images))
	for _, img := range images {
		out = append(out, MappedImage{Channel: ch, Old: old, Image: img})
	}
	return out
}
