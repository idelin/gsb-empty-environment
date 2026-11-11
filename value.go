package jsonpathstream

import "encoding/json"

// Object is a JSON object that preserves the order of its members.
// Numbers are always float64, matching the default decoding used by both the
// streaming engine and the whole-document reference evaluator.
type Object []Field

// Field is one member of an Object.
type Field struct {
	Key   string
	Value any
}

func (o Object) get(key string) (any, bool) {
	for _, f := range o {
		if f.Key == key {
			return f.Value, true
		}
	}
	return nil, false
}

// DecodeAll reads and buffers an entire JSON document into an ordered value.
// It is the reference implementation against which the streaming engine is
// compared in the tests.
func DecodeAll(d *json.Decoder) (any, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	return buildValue(d, tok)
}

func buildValue(d *json.Decoder, tok json.Token) (any, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case json.Delim(0x7B): // {
			var obj Object
			for d.More() {
				keyTok, err := d.Token()
				if err != nil {
					return nil, err
				}
				val, err := DecodeAll(d)
				if err != nil {
					return nil, err
				}
				obj = append(obj, Field{Key: keyTok.(string), Value: val})
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		case json.Delim(0x5B): // [
			var arr []any
			for d.More() {
				val, err := DecodeAll(d)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		default:
			return nil, &json.UnmarshalTypeError{Value: "delim"}
		}
	default:
		return tok, nil // string, float64, bool, nil
	}
}

type intSet map[int]struct{}

func (s intSet) add(v int)      { s[v] = struct{}{} }
func (s intSet) has(v int) bool { _, ok := s[v]; return ok }

func cloneIntSet(s intSet) intSet {
	c := make(intSet, len(s))
	for k := range s {
		c[k] = struct{}{}
	}
	return c
}

// EvaluateAll evaluates a compiled path against a fully buffered document and
// returns every match in document order. Duplicate nodes that several path
// states can reach (via recursive descent) are emitted once.
func EvaluateAll(p *Path, root any) []Match {
	em := &collector{seen: map[string]struct{}{}}
	walk(p, root, intSet{0: {}}, "", em)
	return em.out
}

type emitter interface{ emit(path string, value any) }

type collector struct {
	out  []Match
	seen map[string]struct{}
}

func (c *collector) emit(path string, value any) {
	if _, ok := c.seen[path]; ok {
		return
	}
	c.seen[path] = struct{}{}
	c.out = append(c.out, Match{Path: path, Value: value})
}

// Match is one value selected by a query.
// Path is the canonical JSONPath of the node (e.g. $.store.book[0].title);
// Value is the matched node using the ordered value model.
type Match struct {
	Path  string
	Value any
}

func walk(p *Path, node any, states intSet, canonical string, em emitter) {
	if len(states) == 0 {
		return
	}
	states = filterStates(p, node, states)
	final := len(p.segments)
	if states.has(final) {
		em.emit(canonical, node)
	}
	switch v := node.(type) {
	case Object:
		for _, f := range v {
			childPath := joinPath(canonical, memberSuffix(f.Key))
			walk(p, f.Value, memberStates(p, states, f.Key), childPath, em)
		}
	case []any:
		for i, child := range v {
			childPath := joinPath(canonical, indexSuffix(i))
			walk(p, child, arrayStates(p, states, i, len(v)), childPath, em)
		}
	}
}

// memberStates maps the states active on an object to the states active on a
// named member. Filters are handled at node entry (see filterStates), so here
// a filter segment only contributes its recursive carry.
// memberStates maps the states active on an object to the states active on
// a named member. Filter and length-dependent segments are carried through
// unchanged: filters run at node entry via filterStates, while negative
// indices/slices never occur on an object and simply do not match.
func memberStates(p *Path, states intSet, key string) intSet {
	out := intSet{}
	for pos := range states {
		seg, ok := segmentAt(p, pos)
		if !ok {
			continue
		}
		if seg.recurse {
			out.add(pos) // recursive descent stays active on every descendant
		}
		switch seg.kind {
		case segMember:
			if seg.name == key {
				out.add(pos + 1)
			}
		case segWildcard:
			out.add(pos + 1)
		case segFilter:
			out.add(pos) // evaluated at the child node entry
		default:
			// index/slice do not match object members
		}
	}
	return out
}

// arrayStates maps the states active on an array to the states active on the
// element at index idx. length < 0 means the length is not yet known
// (streaming); length-dependent segments then produce no online states and are
// resolved by capturing the array instead.
func arrayStates(p *Path, states intSet, idx, length int) intSet {
	out := intSet{}
	for pos := range states {
		seg, ok := segmentAt(p, pos)
		if !ok {
			continue
		}
		if seg.recurse {
			out.add(pos)
		}
		switch seg.kind {
		case segWildcard:
			out.add(pos + 1)
		case segIndex:
			if seg.needsLength {
				if length >= 0 && resolveIndex(seg.index, length) == idx {
					out.add(pos + 1)
				}
			} else if seg.index == idx {
				out.add(pos + 1)
			}
		case segFilter:
			out.add(pos) // evaluated at the element node entry
		case segSlice:
			if seg.needsLength {
				if length >= 0 && sliceMatches(seg, idx, length) {
					out.add(pos + 1)
				}
			} else if sliceMatches(seg, idx, length) {
				out.add(pos + 1)
			}
		}
	}
	return out
}

// filterStates applies the filter segments reachable at a node. The returned
// set contains the carried states (unchanged) plus any pos+1 states whose
// filter accepts node. Used when walking a fully buffered (captured) subtree.
func filterStates(p *Path, node any, states intSet) intSet {
	out := cloneIntSet(states)
	for pos := range states {
		seg, ok := segmentAt(p, pos)
		if !ok || seg.kind != segFilter {
			continue
		}
		if evalFilter(seg.filter, node) {
			out.add(pos + 1)
		}
	}
	return out
}

func segmentAt(p *Path, pos int) (segment, bool) {
	if pos < 0 || pos >= len(p.segments) {
		return segment{}, false
	}
	return p.segments[pos], true
}

func resolveIndex(i, length int) int {
	if i < 0 {
		i += length
	}
	return i
}

// sliceMatches reports whether index idx of an array with the given length is
// selected by the slice. length may be unknown (-1) only when the slice has no
// negative bounds; in that case unbounded semantics are applied.
// sliceMatches reports whether index idx of an array is selected by the
// slice. length < 0 means the length is not yet known (streaming); in that
// mode only non-negative bounds are allowed and an unbounded upper end means
// "every later index".
func sliceMatches(seg segment, idx, length int) bool {
	streaming := length < 0
	lo := 0
	hi := length
	if seg.start != nil {
		lo = normalizeBound(*seg.start, length)
	}
	if seg.end != nil {
		hi = normalizeBound(*seg.end, length)
	} else if streaming {
		hi = idx + 1 // unbounded: accept the index subject to the lower bound
	}
	if lo < 0 {
		lo = 0
	}
	if !streaming && hi > length {
		hi = length
	}
	if idx < lo || idx >= hi {
		return false
	}
	step := seg.step
	if step == 0 {
		step = 1
	}
	return (idx-lo)%step == 0
}

func normalizeBound(b, length int) int {
	if b < 0 {
		return b + length
	}
	return b
}

// needsCapture reports whether any reachable next segment cannot be resolved
// while streaming and therefore requires the current node subtree to be
// buffered: a filter (its referenced fields may appear later), a negative
// index/slice (length unknown), or a segment that ends the query on a
// container (the whole container value must be materialized).
func needsCapture(p *Path, states intSet, isContainer bool) bool {
	if isContainer && states.has(len(p.segments)) {
		return true
	}
	for pos := range states {
		if pos >= len(p.segments) {
			continue
		}
		seg := p.segments[pos]
		switch seg.kind {
		case segFilter:
			return true
		case segIndex, segSlice:
			if seg.needsLength {
				return true
			}
		}
	}
	return false
}
