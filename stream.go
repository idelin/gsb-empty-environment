package jsonpathstream

import (
	"encoding/json"
	"io"
)

// Sink receives matched values as soon as they become available. The value
// types are float64, string, bool, nil, Object and []any. Match.Path is the
// canonical JSONPath of the matched node.
type Sink func(Match) error

// EvaluateReader streams r through the compiled path, invoking sink for every
// match in document order.
//
// The document is never buffered as a whole. The only materialized values are
// individual subtrees that a query cannot resolve online: nodes selected by a
// filter (its referenced fields may appear after the value to output) or by a
// length-dependent negative index/slice, plus container values selected in
// full. In each case at most that single subtree is held at a time.
func EvaluateReader(p *Path, r io.Reader, sink Sink) error {
	d := json.NewDecoder(r)
	tok, err := d.Token()
	if err != nil {
		return err
	}
	e := &streamEngine{p: p, d: d, sink: sink, seen: map[string]struct{}{}}
	return e.run(tok)
}

// EvaluateDecoder is EvaluateReader over an existing decoder.
func EvaluateDecoder(p *Path, d *json.Decoder, sink Sink) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	e := &streamEngine{p: p, d: d, sink: sink, seen: map[string]struct{}{}}
	return e.run(tok)
}

type frameKind int

const (
	frameObject frameKind = iota
	frameArray
)

type frame struct {
	kind   frameKind
	states intSet // states active on this container itself
	path   string // canonical path of this container
	idx    int    // next array element index
}

type streamEngine struct {
	p     *Path
	d     *json.Decoder
	sink  Sink
	seen  map[string]struct{}
	err   error
	stack []*frame
}

// streamEmitter routes buffered-subtree matches through the user sink while
// keeping the same node-level de-duplication as the reference evaluator.
type streamEmitter struct {
	e *streamEngine
}

func (s streamEmitter) emit(path string, value any) {
	if _, ok := s.e.seen[path]; ok {
		return
	}
	s.e.seen[path] = struct{}{}
	_ = s.e.sink(Match{Path: path, Value: value})
}

func (e *streamEngine) run(first json.Token) error {
	e.handleValue(first, intSet{0: {}}, "")
	if e.err != nil {
		return e.err
	}
	for len(e.stack) > 0 {
		tok, err := e.d.Token()
		if err != nil {
			return err
		}
		top := e.stack[len(e.stack)-1]
		if top.kind == frameObject {
			if delim, ok := tok.(json.Delim); ok && delim == json.Delim(0x7D) {
				e.closeFrame()
				continue
			}
			key, ok := tok.(string)
			if !ok {
				return &json.UnmarshalTypeError{Value: "non-string object key"}
			}
			valTok, err := e.d.Token()
			if err != nil {
				return err
			}
			childPath := joinPath(top.path, memberSuffix(key))
			states := memberStates(e.p, top.states, key)
			e.handleValue(valTok, states, childPath)
			if e.err != nil {
				return e.err
			}
			continue
		}
		// array frame
		if delim, ok := tok.(json.Delim); ok && delim == json.Delim(0x5D) {
			e.closeFrame()
			continue
		}
		idx := top.idx
		top.idx++
		childPath := joinPath(top.path, indexSuffix(idx))
		states := arrayStates(e.p, top.states, idx, -1)
		e.handleValue(tok, states, childPath)
		if e.err != nil {
			return e.err
		}
	}
	return nil
}

func (e *streamEngine) closeFrame() {
	e.stack = e.stack[:len(e.stack)-1]
}

func (e *streamEngine) handleValue(tok json.Token, states intSet, path string) {
	if len(states) == 0 {
		e.skipValue(tok)
		return
	}
	states = filterStates(e.p, e.scalarOf(tok), states)
	if delim, ok := tok.(json.Delim); ok {
		isArray := delim == json.Delim(0x5B)
		if needsCapture(e.p, states, true) {
			captured, err := e.buildValue(tok)
			if err != nil {
				e.err = err
				return
			}
			walk(e.p, captured, states, path, streamEmitter{e})
			return
		}
		kind := frameObject
		if isArray {
			kind = frameArray
		}
		e.stack = append(e.stack, &frame{kind: kind, states: cloneIntSet(states), path: path})
		return
	}
	// scalar
	if states.has(len(e.p.segments)) {
		e.emit(path, tok)
	}
}

func (e *streamEngine) scalarOf(tok json.Token) any {
	if _, ok := tok.(json.Delim); ok {
		return nil // filters on containers are resolved after capture
	}
	return tok
}

func (e *streamEngine) emit(path string, value any) {
	if _, ok := e.seen[path]; ok {
		return
	}
	e.seen[path] = struct{}{}
	if err := e.sink(Match{Path: path, Value: value}); err != nil {
		e.err = err
	}
}

// skipValue consumes and discards a value that no active state can reach.
func (e *streamEngine) skipValue(tok json.Token) {
	if _, ok := tok.(json.Delim); !ok {
		return
	}
	depth := 1
	for depth > 0 {
		t, err := e.d.Token()
		if err != nil {
			e.err = err
			return
		}
		if d, ok := t.(json.Delim); ok {
			if d == json.Delim(0x5B) || d == json.Delim(0x7B) {
				depth++
			} else {
				depth--
			}
		}
	}
}

// buildValue materializes the value introduced by opening delim tok.
func (e *streamEngine) buildValue(tok json.Token) (any, error) {
	return buildValue(e.d, tok)
}
