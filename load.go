package netsec

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// LoadFunc is the configuration loader that runs only after a document passes
// the expansion budget. Keeping this as a function parameter lets callers plug
// in their existing yaml.v3-based loader and assert that it was not invoked for
// rejected input.
type LoadFunc func(data []byte, out interface{}) error

// Parse parses YAML into a yaml.v3 Node tree. Aliases remain pointers to their
// anchors; no expansion is performed.
func Parse(data []byte) (*yaml.Node, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root.Kind == 0 {
		return nil, fmt.Errorf("netsec: empty YAML document")
	}
	return &root, nil
}

// Preflight parses data, enforces limits, and returns the untouched Node tree
// and estimate so the caller can decode it with yaml.v3. Because the tree is
// not rewritten, yaml.v3 merge semantics and original Line/Column values are
// preserved.
func Preflight(data []byte, limits ...Limits) (*yaml.Node, Stats, error) {
	root, err := Parse(data)
	if err != nil {
		return nil, Stats{}, err
	}
	stats, err := Analyze(root, limits...)
	if err != nil {
		return root, stats, err
	}
	return root, stats, nil
}

// Decode parses data, checks the budget, and only then calls yaml.Unmarshal.
// The YAML parser necessarily runs to build the Node tree, but any expensive
// merge/alias materialization performed by the loader is deferred until the
// check succeeds.
func Decode(data []byte, out interface{}, limits ...Limits) error {
	return DecodeWith(data, out, yaml.Unmarshal, limits...)
}

// DecodeWith behaves like Decode but calls load instead of yaml.Unmarshal. It
// is the integration point for an application's existing loader and makes the
// "loader was not called" property directly testable.
func DecodeWith(data []byte, out interface{}, load LoadFunc, limits ...Limits) error {
	_, _, err := Preflight(data, limits...)
	if err != nil {
		return err
	}
	return load(data, out)
}

// Load validates before invoking load. The variadic arguments may include a
// LoadFunc (or an unnamed function with the same signature) and/or Limits in
// either order, which makes migration from different loader wrappers easy.
// With no loader it falls back to yaml.Unmarshal; with no limits it uses
// DefaultLimits.
func Load(data []byte, out interface{}, args ...interface{}) error {
	var load LoadFunc = yaml.Unmarshal
	var limits *Limits
	for _, arg := range args {
		switch value := arg.(type) {
		case LoadFunc:
			load = value
		case func([]byte, interface{}) error:
			load = value
		case Limits:
			limits = &value
		case *Limits:
			limits = value
		case nil:
			continue
		default:
			return fmt.Errorf("netsec: unsupported Load argument %T", arg)
		}
	}
	if limits != nil {
		return DecodeWith(data, out, load, *limits)
	}
	return DecodeWith(data, out, load)
}

// Load applies the budget before invoking load.
func (b *Budget) Load(data []byte, out interface{}, load LoadFunc) error {
	if b == nil {
		return fmt.Errorf("netsec: nil budget")
	}
	root, err := Parse(data)
	if err != nil {
		return err
	}
	if err := b.Check(root); err != nil {
		return err
	}
	return load(data, out)
}
