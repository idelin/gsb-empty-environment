package enumkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Field binds one enum registry to a location pattern inside a JSON
// document. Path segments are dot-separated; "*" matches exactly one
// segment (object key or array index). Example: "spec.items.*.mode".
type Field struct {
	Path string
	Reg  *Registry
}

// splitPath normalizes "spec.items[2].mode" into {"spec","items","2","mode"}.
func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	var out []string
	for _, seg := range strings.Split(path, ".") {
		for {
			open := strings.IndexByte(seg, '[')
			if open < 0 {
				if seg != "" {
					out = append(out, seg)
				}
				break
			}
			if open > 0 {
				out = append(out, seg[:open])
			}
			close := strings.IndexByte(seg, ']')
			if close < 0 {
				out = append(out, seg[open+1:])
				break
			}
			out = append(out, seg[open+1:close])
			seg = seg[close+1:]
		}
	}
	return out
}

func matchPath(pattern, concrete []string) bool {
	if len(pattern) != len(concrete) {
		return false
	}
	for i := range pattern {
		if pattern[i] == "*" {
			continue
		}
		if pattern[i] != concrete[i] {
			return false
		}
	}
	return true
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// DecodeJSON walks doc, decodes every string field matching a Field
// pattern at the given version, and returns values keyed by their
// original concrete field path (e.g. "spec.items[1].mode"). Failures are
// returned as PathError values carrying the original field path.
func DecodeJSON(doc []byte, version string, fields []Field) (map[string]Value, []error) {
	var root any
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, []error{fmt.Errorf("enumkit: invalid JSON document: %w", err)}
	}
	values := map[string]Value{}
	var errs []error
	walkJSON(root, "", func(path string, s string) {
		for _, f := range fields {
			if !matchPath(splitPath(f.Path), splitPath(path)) {
				continue
			}
			v, err := f.Reg.Decode(version, s)
			if err != nil {
				errs = append(errs, &PathError{Path: path, Err: err})
				continue
			}
			values[path] = v
		}
	})
	return values, errs
}

// MigrateJSON rewrites every matching enum field from its source
// version spelling to the canonical name at the target version, in the
// merged alias graph of the registry. Unknown values pass through
// unchanged. Ambiguous tokens are reported as PathError wrapping
// AmbiguityError (with the shortest conflict chain) and left untouched.
func MigrateJSON(doc []byte, from, to string, fields []Field) ([]byte, []error) {
	var root any
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, []error{fmt.Errorf("enumkit: invalid JSON document: %w", err)}
	}
	graphs := map[*Registry]*Graph{}
	var errs []error
	walkJSON(root, "", func(path string, s string) {
		for _, f := range fields {
			if !matchPath(splitPath(f.Path), splitPath(path)) {
				continue
			}
			g, ok := graphs[f.Reg]
			if !ok {
				var err error
				g, err = f.Reg.Merge()
				if err != nil {
					errs = append(errs, &PathError{Path: path, Err: err})
					return
				}
				graphs[f.Reg] = g
			}
			v, err := g.Migrate(from, to, s)
			if err != nil {
				errs = append(errs, &PathError{Path: path, Err: err})
				continue
			}
			setJSONPath(root, splitPath(path), g.CanonicalAt(to, v))
		}
	})
	if len(errs) > 0 {
		return nil, errs
	}
	out, err := json.Marshal(root)
	if err != nil {
		return nil, []error{fmt.Errorf("enumkit: failed to marshal document: %w", err)}
	}
	return out, nil
}

// walkJSON visits every string leaf with its concrete path.
func walkJSON(node any, path string, visit func(path, s string)) {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			walkJSON(v, joinPath(path, k), visit)
		}
	case []any:
		for i, v := range n {
			walkJSON(v, path+"["+strconv.Itoa(i)+"]", visit)
		}
	case string:
		visit(path, n)
	}
}

// setJSONPath replaces the value at a concrete path.
func setJSONPath(root any, segs []string, val string) {
	node := root
	for i, seg := range segs {
		last := i == len(segs)-1
		switch n := node.(type) {
		case map[string]any:
			if last {
				n[seg] = val
				return
			}
			node = n[seg]
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(n) {
				return
			}
			if last {
				n[idx] = val
				return
			}
			node = n[idx]
		default:
			return
		}
	}
}
