package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrMalformedExpression is returned when a key expression cannot be parsed.
var ErrMalformedExpression = errors.New("netsec: malformed key expression")

// ErrNotConcreteKey is returned when a publish key contains wildcards.
var ErrNotConcreteKey = errors.New("netsec: publish key is not concrete")

type segmentKind uint8

const (
	segmentPattern segmentKind = iota
	segmentStar
	segmentDoubleStar
)

type chunkPart struct {
	text     []byte
	wildcard bool
}

type segment struct {
	kind  segmentKind
	parts []chunkPart
}

type expression struct {
	segments []segment
	literals map[byte]struct{}
	nfa      *nfa
}

func parseExpression(raw string) (*expression, error) {
	if raw == "" {
		return nil, fmt.Errorf("%w %q: expression is empty", ErrMalformedExpression, raw)
	}

	chunks := strings.Split(raw, "/")
	segments := make([]segment, 0, len(chunks))
	literals := make(map[byte]struct{})

	for i, chunk := range chunks {
		if chunk == "" {
			return nil, fmt.Errorf("%w %q: segment %d is empty", ErrMalformedExpression, raw, i+1)
		}

		switch chunk {
		case "*":
			segments = append(segments, segment{kind: segmentStar})
			continue
		case "**":
			segments = append(segments, segment{kind: segmentDoubleStar})
			continue
		}

		parts, chunkLiterals, allWildcards, err := parseChunk(chunk)
		if err != nil {
			return nil, fmt.Errorf("%w %q: segment %d: %w", ErrMalformedExpression, raw, i+1, err)
		}
		if allWildcards {
			segments = append(segments, segment{kind: segmentStar})
			continue
		}
		segments = append(segments, segment{kind: segmentPattern, parts: parts})
		for b := range chunkLiterals {
			literals[b] = struct{}{}
		}
	}

	ex := &expression{
		segments: segments,
		literals: literals,
	}
	ex.nfa = compileExpression(ex)
	return ex, nil
}

func parseChunk(chunk string) ([]chunkPart, map[byte]struct{}, bool, error) {
	var parts []chunkPart
	literals := make(map[byte]struct{})
	var text []byte

	flushText := func() {
		if len(text) == 0 {
			return
		}
		copied := append([]byte(nil), text...)
		parts = append(parts, chunkPart{text: copied})
		for _, b := range copied {
			literals[b] = struct{}{}
		}
		text = text[:0]
	}

	for i := 0; i < len(chunk); i++ {
		switch chunk[i] {
		case '$':
			if i+1 < len(chunk) && chunk[i+1] == '*' {
				flushText()
				parts = append(parts, chunkPart{wildcard: true})
				i++
				continue
			}
			text = append(text, '$')
		case '*':
			return nil, nil, false, errors.New("'*' must be the whole segment; use '$*' inside a segment")
		default:
			text = append(text, chunk[i])
		}
	}
	flushText()

	if len(parts) == 0 {
		return nil, nil, false, errors.New("segment has no content")
	}

	allWildcards := true
	for _, part := range parts {
		if !part.wildcard {
			allWildcards = false
			break
		}
	}
	if allWildcards {
		// A pure sequence of $* wildcards can match an empty chunk, but Zenoh
		// keys have non-empty chunks. Its set of valid keys is therefore *.
		return nil, literals, true, nil
	}

	return parts, literals, false, nil
}

func parseConcreteKey(raw string) (*expression, error) {
	if raw == "" {
		return nil, fmt.Errorf("%w %q: key is empty", ErrNotConcreteKey, raw)
	}
	if strings.HasPrefix(raw, "/") || strings.HasSuffix(raw, "/") || strings.Contains(raw, "//") {
		return nil, fmt.Errorf("%w %q: key contains an empty segment", ErrNotConcreteKey, raw)
	}
	if strings.ContainsAny(raw, "*") {
		return nil, fmt.Errorf("%w %q: key contains a wildcard", ErrNotConcreteKey, raw)
	}

	return parseExpression(raw)
}

type edgeKind uint8

const (
	edgeEpsilon edgeKind = iota
	edgeLiteral
	edgeNonSlash
)

type edge struct {
	to   int
	kind edgeKind
	ch   byte
}

type nfa struct {
	start  int
	accept int
	edges  [][]edge
}

type nfaBuilder struct {
	stateCount int
	edges      map[int][]edge
}

func newNFABuilder() *nfaBuilder {
	return &nfaBuilder{
		edges: make(map[int][]edge),
	}
}

func (b *nfaBuilder) newState() int {
	state := b.stateCount
	b.stateCount++
	return state
}

func (b *nfaBuilder) add(from, to int, kind edgeKind, ch byte) {
	b.edges[from] = append(b.edges[from], edge{to: to, kind: kind, ch: ch})
}

func (b *nfaBuilder) epsilon(from, to int) {
	b.add(from, to, edgeEpsilon, 0)
}

func (b *nfaBuilder) literal(from, to int, ch byte) {
	b.add(from, to, edgeLiteral, ch)
}

func (b *nfaBuilder) nonSlash(from, to int) {
	b.add(from, to, edgeNonSlash, 0)
}

func (b *nfaBuilder) finish(start, accept int) *nfa {
	edges := make([][]edge, b.stateCount)
	for from, out := range b.edges {
		edges[from] = out
	}
	return &nfa{
		start:  start,
		accept: accept,
		edges:  edges,
	}
}

func compileExpression(ex *expression) *nfa {
	b := newNFABuilder()

	// Keep track of whether at least one chunk has been consumed. A slash is
	// inserted before a chunk only when there is already a preceding chunk.
	states := make([]int, 2*(len(ex.segments)+1))
	for i := range states {
		states[i] = b.newState()
	}
	state := func(index int, hasChunk bool) int {
		if hasChunk {
			return states[2*index+1]
		}
		return states[2*index]
	}

	for i, seg := range ex.segments {
		switch seg.kind {
		case segmentDoubleStar:
			fromEmpty := state(i, false)
			fromChunk := state(i, true)
			toEmpty := state(i+1, false)
			toChunk := state(i+1, true)

			b.epsilon(fromEmpty, toEmpty)
			b.epsilon(fromChunk, toChunk)

			// Zero chunks are already covered by the epsilon edges above.
			addAnyChunk(b, fromEmpty, false, fromChunk)
			addAnyChunk(b, fromChunk, true, fromChunk)

		case segmentStar:
			for _, hasChunk := range []bool{false, true} {
				from := state(i, hasChunk)
				to := state(i+1, true)
				addAnyChunk(b, from, hasChunk, to)
			}

		case segmentPattern:
			for _, hasChunk := range []bool{false, true} {
				from := state(i, hasChunk)
				to := state(i+1, true)
				addPatternChunk(b, from, hasChunk, seg.parts, to)
			}
		}
	}

	start := state(0, false)
	accept := state(len(ex.segments), true)
	return b.finish(start, accept)
}

func addAnyChunk(b *nfaBuilder, from int, slashPrefix bool, to int) {
	current := from
	if slashPrefix {
		slashState := b.newState()
		b.literal(current, slashState, '/')
		current = slashState
	}

	first := b.newState()
	repeat := b.newState()
	b.nonSlash(current, first)
	b.nonSlash(repeat, repeat)
	b.epsilon(first, repeat)
	b.epsilon(repeat, to)
}

func addPatternChunk(b *nfaBuilder, from int, slashPrefix bool, parts []chunkPart, to int) {
	current := from
	if slashPrefix {
		slashState := b.newState()
		b.literal(current, slashState, '/')
		current = slashState
	}

	for _, part := range parts {
		if part.wildcard {
			wildcardState := b.newState()
			b.epsilon(current, wildcardState)
			b.nonSlash(wildcardState, wildcardState)
			current = wildcardState
			continue
		}
		for _, ch := range part.text {
			next := b.newState()
			b.literal(current, next, ch)
			current = next
		}
	}
	b.epsilon(current, to)
}

func alphabetFor(expressions ...*expression) []byte {
	chars := map[byte]struct{}{'/': {}}
	for _, ex := range expressions {
		for b := range ex.literals {
			chars[b] = struct{}{}
		}
	}

	// Every non-slash byte not mentioned literally behaves identically under
	// the wildcard edges, so one representative is sufficient.
	for value := 0; value < 256; value++ {
		if byte(value) == '/' {
			continue
		}
		if _, ok := chars[byte(value)]; !ok {
			chars[byte(value)] = struct{}{}
			break
		}
	}

	result := make([]byte, 0, len(chars))
	for b := range chars {
		result = append(result, b)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
