// Package jsonproj implements a columnar projector for semi-structured JSON.
//
// A Projector is configured with a set of leaf paths (e.g. "user.address.city").
// Project streams the input JSON exactly once with encoding/json's token-level
// Decoder, materializing only the requested leaf values into independent
// virtual columns. Subtrees that cannot contain any requested path are skipped
// at the token level and are never deserialized into Go values.
package jsonproj

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Column is a virtual column: all values found at one leaf path, in document
// order.
type Column struct {
	Path   string
	Values []any
}

// Projector extracts a fixed set of leaf paths into columns.
type Projector struct {
	paths []string
	set   map[string]struct{}
}

// NewProjector builds a Projector for the given leaf paths.
func NewProjector(paths ...string) *Projector {
	p := &Projector{
		paths: append([]string(nil), paths...),
		set:   make(map[string]struct{}, len(paths)),
	}
	for _, path := range paths {
		p.set[path] = struct{}{}
	}
	return p
}

// Paths returns the leaf paths this Projector extracts.
func (p *Projector) Paths() []string {
	return append([]string(nil), p.paths...)
}

// Project reads the JSON document from r and returns one Column per requested
// path, keyed by path. Only requested leaves are deserialized; everything else
// is skipped as raw tokens.
func (p *Projector) Project(r io.Reader) (map[string]*Column, error) {
	cols := make(map[string]*Column, len(p.paths))
	for _, path := range p.paths {
		cols[path] = &Column{Path: path}
	}
	dec := json.NewDecoder(r)
	if err := p.walkValue(dec, nil, cols); err != nil {
		return nil, err
	}
	return cols, nil
}

const (
	actSkip = iota
	actDescend
	actCapture
)

// action decides what to do with the value at path: capture it, descend into
// it, or skip it entirely.
func (p *Projector) action(path []string) int {
	key := joinPath(path)
	if _, ok := p.set[key]; ok {
		return actCapture
	}
	for want := range p.set {
		if len(want) > len(key) && strings.HasPrefix(want, key) {
			// key must end on a segment boundary of want.
			if key == "" || want[len(key)] == '.' || want[len(key)] == '[' {
				return actDescend
			}
		}
	}
	return actSkip
}

func (p *Projector) walkValue(dec *json.Decoder, path []string, cols map[string]*Column) error {
	switch p.action(path) {
	case actCapture:
		var v any
		if err := dec.Decode(&v); err != nil {
			return err
		}
		col := cols[joinPath(path)]
		col.Values = append(col.Values, v)
		return nil
	case actSkip:
		return skipValue(dec)
	}

	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		// Scalar, but requested paths go deeper: nothing to capture here.
		return nil
	}
	switch delim {
	case '{':
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := kt.(string)
			if !ok {
				return fmt.Errorf("jsonproj: expected object key, got %v", kt)
			}
			if err := p.walkValue(dec, append(path, name), cols); err != nil {
				return err
			}
		}
		_, err := dec.Token() // closing '}'
		return err
	case '[':
		for i := 0; dec.More(); i++ {
			if err := p.walkValue(dec, append(path, fmt.Sprintf("[%d]", i)), cols); err != nil {
				return err
			}
		}
		_, err := dec.Token() // closing ']'
		return err
	}
	return nil
}

// skipValue consumes exactly one JSON value from the token stream without
// materializing it.
func skipValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil // scalar
	}
	if delim != '{' && delim != '[' {
		return fmt.Errorf("jsonproj: unexpected delimiter %q", delim)
	}
	depth := 1
	for depth > 0 {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}

// InferSchema streams the document once and returns the paths of all leaf
// values (scalars, plus empty objects/arrays) in document order. Array
// elements are indexed, e.g. "items[0].sku".
func InferSchema(r io.Reader) ([]string, error) {
	dec := json.NewDecoder(r)
	var paths []string
	if err := inferValue(dec, nil, &paths); err != nil {
		return nil, err
	}
	return paths, nil
}

func inferValue(dec *json.Decoder, path []string, paths *[]string) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		*paths = append(*paths, joinPath(path))
		return nil
	}
	switch delim {
	case '{':
		empty := true
		for dec.More() {
			empty = false
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := kt.(string)
			if !ok {
				return fmt.Errorf("jsonproj: expected object key, got %v", kt)
			}
			if err := inferValue(dec, append(path, name), paths); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
		if empty {
			*paths = append(*paths, joinPath(path))
		}
	case '[':
		empty := true
		for i := 0; dec.More(); i++ {
			empty = false
			if err := inferValue(dec, append(path, fmt.Sprintf("[%d]", i)), paths); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
		if empty {
			*paths = append(*paths, joinPath(path))
		}
	}
	return nil
}

// joinPath renders path segments using dotted object keys and bracketed array
// indices: ["user","address","city"] -> "user.address.city",
// ["items","[0]","sku"] -> "items[0].sku".
func joinPath(path []string) string {
	var b strings.Builder
	for i, seg := range path {
		if strings.HasPrefix(seg, "[") {
			b.WriteString(seg)
		} else {
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(seg)
		}
	}
	return b.String()
}
