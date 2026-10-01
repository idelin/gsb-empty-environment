package graphser

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// formatVersion is the only wire format version currently defined.
const formatVersion = 1

// maxExactInt is the largest magnitude of an integer exactly representable
// as a float64.
const maxExactInt = 1 << 53

type document struct {
	Version int            `json:"version"`
	Count   int            `json:"count"`
	Objects []objectRecord `json:"objects"`
	Root    any            `json:"root"`
}

type objectRecord struct {
	ID     int            `json:"id"`
	Fields map[string]any `json:"fields"`
}

type encoder struct {
	ids   map[*Object]int
	order []*Object
}

// Marshal serializes the graph reachable from root into a deterministic
// JSON document. Values outside the supported domain are rejected.
func Marshal(root any) ([]byte, error) {
	e := &encoder{ids: make(map[*Object]int)}
	if err := e.assign(root, 0); err != nil {
		return nil, err
	}
	encRoot, err := e.encodeNode(root)
	if err != nil {
		return nil, err
	}
	recs := make([]objectRecord, len(e.order))
	for i, o := range e.order {
		fields := make(map[string]any, len(o.Fields))
		for k, fv := range o.Fields {
			enc, err := e.encodeNode(fv)
			if err != nil {
				return nil, err
			}
			fields[k] = enc
		}
		recs[i] = objectRecord{ID: i + 1, Fields: fields}
	}
	doc := document{Version: formatVersion, Count: len(recs), Objects: recs, Root: encRoot}
	return json.Marshal(doc)
}

// assign walks the graph, validating every value and giving each distinct
// *Object a stable ID in first-encounter order. It doubles as the cycle
// guard: an already-numbered object is not revisited.
func (e *encoder) assign(v any, depth int) error {
	if depth > MaxDepth {
		return fmt.Errorf("graphser: nesting deeper than %d levels", MaxDepth)
	}
	switch t := v.(type) {
	case nil, bool:
		return nil
	case string:
		if len(t) > MaxStringLen {
			return fmt.Errorf("graphser: string of %d bytes exceeds limit of %d", len(t), MaxStringLen)
		}
		return nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return fmt.Errorf("graphser: non-finite number %v", t)
		}
		return nil
	case int:
		return checkIntRange(int64(t))
	case int64:
		return checkIntRange(t)
	case uint64:
		if t > maxExactInt {
			return fmt.Errorf("graphser: integer %d out of exact float64 range", t)
		}
		return nil
	case []any:
		if len(t) > MaxListLen {
			return fmt.Errorf("graphser: list of %d items exceeds limit of %d", len(t), MaxListLen)
		}
		for _, item := range t {
			if err := e.assign(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	case *Object:
		if t == nil {
			return fmt.Errorf("graphser: nil *Object")
		}
		if _, ok := e.ids[t]; ok {
			return nil
		}
		if len(e.ids) >= MaxObjects {
			return fmt.Errorf("graphser: more than %d objects", MaxObjects)
		}
		e.ids[t] = len(e.ids) + 1
		e.order = append(e.order, t)
		if len(t.Fields) > MaxFields {
			return fmt.Errorf("graphser: object with %d fields exceeds limit of %d", len(t.Fields), MaxFields)
		}
		for _, k := range sortedKeys(t.Fields) {
			if err := e.assign(t.Fields[k], depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("graphser: unsupported type %T", v)
	}
}

func (e *encoder) encodeNode(v any) (any, error) {
	switch t := v.(type) {
	case nil, bool, string, float64:
		return t, nil
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case uint64:
		return float64(t), nil
	case []any:
		items := make([]any, len(t))
		for i, item := range t {
			enc, err := e.encodeNode(item)
			if err != nil {
				return nil, err
			}
			items[i] = enc
		}
		return map[string]any{"list": items}, nil
	case *Object:
		return map[string]any{"ref": e.ids[t]}, nil
	default:
		return nil, fmt.Errorf("graphser: unsupported type %T", v)
	}
}

func checkIntRange(v int64) error {
	if v > maxExactInt || v < -maxExactInt {
		return fmt.Errorf("graphser: integer %d out of exact float64 range", v)
	}
	return nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
