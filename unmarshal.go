package graphser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// Unmarshal decodes a document produced by Marshal. Decoding is a pure
// data operation: the format carries no type names or code, and no host
// constructors are invoked.
//
// The document is fully validated before any graph node is allocated:
// version, declared object count, ID uniqueness, reference targets, and
// all length/depth limits are checked up front. Decoding then proceeds in
// two phases -- every *Object placeholder is created first, then fields
// and references are linked -- so shared references come back as the same
// pointer and cycles (including self-loops) are preserved.
func Unmarshal(data []byte) (any, error) {
	if len(data) > MaxInputBytes {
		return nil, fmt.Errorf("graphser: input of %d bytes exceeds limit of %d", len(data), MaxInputBytes)
	}
	var raw struct {
		Version int `json:"version"`
		Count   int `json:"count"`
		Objects []struct {
			ID     int             `json:"id"`
			Fields json.RawMessage `json:"fields"`
		} `json:"objects"`
		Root json.RawMessage `json:"root"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("graphser: invalid document: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("graphser: trailing data after document")
	}
	if raw.Version != formatVersion {
		return nil, fmt.Errorf("graphser: unsupported version %d", raw.Version)
	}
	if raw.Count != len(raw.Objects) {
		return nil, fmt.Errorf("graphser: declared object count %d does not match %d records", raw.Count, len(raw.Objects))
	}
	if len(raw.Objects) > MaxObjects {
		return nil, fmt.Errorf("graphser: %d object records exceeds limit of %d", len(raw.Objects), MaxObjects)
	}
	if raw.Root == nil {
		return nil, fmt.Errorf("graphser: missing root")
	}

	// Parse and validate everything before allocating the graph.
	type parsedRecord struct {
		id     int
		fields map[string]any
	}
	recs := make([]parsedRecord, len(raw.Objects))
	ids := make(map[int]struct{}, len(raw.Objects))
	for i, r := range raw.Objects {
		if r.ID < 1 {
			return nil, fmt.Errorf("graphser: invalid object id %d", r.ID)
		}
		if _, dup := ids[r.ID]; dup {
			return nil, fmt.Errorf("graphser: duplicate object id %d", r.ID)
		}
		ids[r.ID] = struct{}{}
		var fields map[string]any
		if r.Fields != nil {
			if err := json.Unmarshal(r.Fields, &fields); err != nil {
				return nil, fmt.Errorf("graphser: object %d: invalid fields: %w", r.ID, err)
			}
		}
		if len(fields) > MaxFields {
			return nil, fmt.Errorf("graphser: object %d with %d fields exceeds limit of %d", r.ID, len(fields), MaxFields)
		}
		recs[i] = parsedRecord{id: r.ID, fields: fields}
	}
	var rootNode any
	if err := json.Unmarshal(raw.Root, &rootNode); err != nil {
		return nil, fmt.Errorf("graphser: invalid root: %w", err)
	}
	if err := validateNode(rootNode, 0, ids); err != nil {
		return nil, fmt.Errorf("graphser: root: %w", err)
	}
	for _, rec := range recs {
		for k, v := range rec.fields {
			if err := validateNode(v, 1, ids); err != nil {
				return nil, fmt.Errorf("graphser: object %d field %q: %w", rec.id, k, err)
			}
		}
	}

	// Phase 1: allocate one placeholder per object record.
	objs := make(map[int]*Object, len(recs))
	for _, rec := range recs {
		objs[rec.id] = &Object{Fields: make(map[string]any, len(rec.fields))}
	}
	// Phase 2: link fields and references to the placeholders.
	for _, rec := range recs {
		o := objs[rec.id]
		for k, v := range rec.fields {
			o.Fields[k] = buildNode(v, objs)
		}
	}
	return buildNode(rootNode, objs), nil
}

// validateNode checks the shape of a raw decoded node, including length
// limits and reference targets, without allocating any graph nodes.
func validateNode(v any, depth int, ids map[int]struct{}) error {
	if depth > MaxDepth {
		return fmt.Errorf("graphser: nesting deeper than %d levels", MaxDepth)
	}
	switch t := v.(type) {
	case nil, bool, float64:
		return nil
	case string:
		if len(t) > MaxStringLen {
			return fmt.Errorf("graphser: string of %d bytes exceeds limit of %d", len(t), MaxStringLen)
		}
		return nil
	case map[string]any:
		if len(t) != 1 {
			return fmt.Errorf("graphser: invalid node object with %d keys", len(t))
		}
		if ref, ok := t["ref"]; ok {
			id, ok := ref.(float64)
			if !ok || id != math.Trunc(id) || id < 1 || id > math.MaxInt32 {
				return fmt.Errorf("graphser: invalid reference %v", ref)
			}
			if _, ok := ids[int(id)]; !ok {
				return fmt.Errorf("graphser: dangling reference to object %d", int(id))
			}
			return nil
		}
		if items, ok := t["list"]; ok {
			arr, ok := items.([]any)
			if !ok {
				return fmt.Errorf("graphser: list node is not an array")
			}
			if len(arr) > MaxListLen {
				return fmt.Errorf("graphser: list of %d items exceeds limit of %d", len(arr), MaxListLen)
			}
			for _, item := range arr {
				if err := validateNode(item, depth+1, ids); err != nil {
					return err
				}
			}
			return nil
		}
		return fmt.Errorf("graphser: invalid node object")
	default:
		return fmt.Errorf("graphser: invalid node of type %T", v)
	}
}

// buildNode materializes a validated node. Validation has already
// guaranteed that every ref resolves and every limit holds.
func buildNode(v any, objs map[int]*Object) any {
	if m, ok := v.(map[string]any); ok {
		if ref, ok := m["ref"]; ok {
			return objs[int(ref.(float64))]
		}
		items := m["list"].([]any)
		out := make([]any, len(items))
		for i, item := range items {
			out[i] = buildNode(item, objs)
		}
		return out
	}
	return v
}
