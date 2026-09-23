package errser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Unmarshal decodes a document produced by Marshal back into an error
// graph. Safety properties:
//   - Input size and every declared count are validated before any
//     allocation proportional to them.
//   - Decoding is pure data: no reflection, no interface dispatch on
//     attacker-controlled type names, no host code execution.
//   - Unknown type codes are replaced with GenericCode; the original
//     code is dropped entirely.
//   - Cycles and shared subgraphs are restored by ID linking, so the
//     round trip preserves identity without recursion.
func Unmarshal(data []byte, opts Options) (*Error, error) {
	if len(data) == 0 {
		return nil, errors.New("errser: empty input")
	}
	if opts.MaxBytes > 0 && len(data) > opts.MaxBytes {
		return nil, fmt.Errorf("%w: input %d bytes exceeds %d", ErrBudgetExceeded, len(data), opts.MaxBytes)
	}

	// Decode into plain data only. json.Decoder on a bounded reader
	// cannot instantiate host types; it builds maps/slices/scalars.
	var doc Document
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("errser: invalid document: %w", err)
	}
	if doc.Version != version {
		return nil, fmt.Errorf("errser: unsupported version %d", doc.Version)
	}

	// Validate declared counts before allocating graph memory.
	if len(doc.Nodes) == 0 {
		return nil, errors.New("errser: document has no nodes")
	}
	if opts.MaxNodes > 0 && len(doc.Nodes) > opts.MaxNodes {
		return nil, fmt.Errorf("%w: %d nodes exceeds %d", ErrBudgetExceeded, len(doc.Nodes), opts.MaxNodes)
	}
	if doc.Root < 0 || doc.Root >= len(doc.Nodes) {
		return nil, fmt.Errorf("errser: root id %d out of range", doc.Root)
	}
	for i := range doc.Nodes {
		n := &doc.Nodes[i]
		if n.ID < 0 || n.ID >= len(doc.Nodes) {
			return nil, fmt.Errorf("errser: node id %d out of range", n.ID)
		}
		if n.Cause < -1 || n.Cause >= len(doc.Nodes) {
			return nil, fmt.Errorf("errser: cause id %d out of range", n.Cause)
		}
		if len(n.Children) > len(doc.Nodes) {
			return nil, fmt.Errorf("%w: node %d declares %d children", ErrBudgetExceeded, n.ID, len(n.Children))
		}
		for _, c := range n.Children {
			if c < 0 || c >= len(doc.Nodes) {
				return nil, fmt.Errorf("errser: child id %d out of range", c)
			}
		}
		if len(n.Context) > opts.MaxContextKeys && opts.MaxContextKeys > 0 {
			return nil, fmt.Errorf("%w: node %d declares %d context keys", ErrBudgetExceeded, n.ID, len(n.Context))
		}
	}

	// Materialize nodes first, then link edges: cycles and shared
	// subgraphs resolve without recursion.
	errs := make([]*Error, len(doc.Nodes))
	for i := range doc.Nodes {
		n := &doc.Nodes[i]
		code := n.Code
		if !registeredCodes[code] {
			code = GenericCode
		}
		msg := n.Message
		if opts.MaxMessageLen > 0 && len(msg) > opts.MaxMessageLen {
			msg, _ = truncate(msg, opts.MaxMessageLen)
		}
		e := &Error{code: code, msg: msg}
		if len(n.Context) > 0 {
			e.ctx = make(map[string]any, len(n.Context))
			for k, v := range n.Context {
				if opts.MaxContextValueLen > 0 && len(v) > opts.MaxContextValueLen {
					v, _ = truncate(v, opts.MaxContextValueLen)
				}
				e.ctx[k] = v
			}
		}
		errs[n.ID] = e
	}
	for i := range doc.Nodes {
		n := &doc.Nodes[i]
		if n.Cause >= 0 {
			errs[n.ID].cause = errs[n.Cause]
		}
		for _, c := range n.Children {
			errs[n.ID].children = append(errs[n.ID].children, errs[c])
		}
	}
	return errs[doc.Root], nil
}
