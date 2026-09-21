package bitlayout

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// Record values use these Go types:
//
//	int (signed)   -> int64
//	int (unsigned) -> uint64
//	struct, union  -> map[string]any
//	array          -> []any
//	checksum       -> uint64 (decoded; ignored on encode, always computed)
//
// pad fields carry no value.

// UnmarshalRecord parses a JSON object into a record value with exact
// integer types (int64/uint64) matching the plan's fields.
func (p *Plan) UnmarshalRecord(data []byte) (map[string]any, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("record JSON: %w", err)
	}
	return p.convertScope(p.root, raw)
}

func (p *Plan) convertScope(n *node, raw map[string]json.RawMessage) (map[string]any, error) {
	out := map[string]any{}
	for _, f := range n.fields {
		switch f.kind {
		case kindInt:
			r, ok := raw[f.name]
			if !ok {
				return nil, fmt.Errorf("%s: missing field", f.path)
			}
			v, err := parseJSONInt(r, f.signed)
			if err != nil {
				return nil, fmt.Errorf("%s: %v", f.path, err)
			}
			out[f.name] = v
		case kindStruct:
			r, ok := raw[f.name]
			if !ok {
				return nil, fmt.Errorf("%s: missing field", f.path)
			}
			var sub map[string]json.RawMessage
			if err := json.Unmarshal(r, &sub); err != nil {
				return nil, fmt.Errorf("%s: expected object", f.path)
			}
			v, err := p.convertScope(f, sub)
			if err != nil {
				return nil, err
			}
			out[f.name] = v
		case kindArray:
			r, ok := raw[f.name]
			if !ok {
				return nil, fmt.Errorf("%s: missing field", f.path)
			}
			var elems []json.RawMessage
			if err := json.Unmarshal(r, &elems); err != nil {
				return nil, fmt.Errorf("%s: expected array", f.path)
			}
			arr := make([]any, len(elems))
			for i, e := range elems {
				v, err := p.convertValue(f.elem, e)
				if err != nil {
					return nil, fmt.Errorf("%s[%d]: %v", f.path, i, err)
				}
				arr[i] = v
			}
			out[f.name] = arr
		case kindUnion:
			r, ok := raw[f.name]
			if !ok {
				return nil, fmt.Errorf("%s: missing field", f.path)
			}
			var sub map[string]json.RawMessage
			if err := json.Unmarshal(r, &sub); err != nil {
				return nil, fmt.Errorf("%s: expected object", f.path)
			}
			// The case is chosen by the already-parsed tag field.
			tagv, ok := out[f.tagFrom.name]
			if !ok {
				return nil, fmt.Errorf("%s: tag field %q must appear before the union", f.path, f.tagFrom.name)
			}
			tag, err := toUint(tagv)
			if err != nil {
				return nil, fmt.Errorf("%s: bad tag value: %v", f.path, err)
			}
			cn, ok := f.cases[tag]
			if !ok {
				return nil, fmt.Errorf("%s: undefined tag value %d", f.path, tag)
			}
			v, err := p.convertScope(cn, sub)
			if err != nil {
				return nil, err
			}
			out[f.name] = v
		case kindPad:
			// no value
		case kindChecksum:
			// decoded from the wire on decode; computed on encode
		}
	}
	return out, nil
}

func (p *Plan) convertValue(n *node, raw json.RawMessage) (any, error) {
	switch n.kind {
	case kindInt:
		return parseJSONInt(raw, n.signed)
	case kindStruct:
		var sub map[string]json.RawMessage
		if err := json.Unmarshal(raw, &sub); err != nil {
			return nil, fmt.Errorf("expected object")
		}
		return p.convertScope(n, sub)
	case kindArray:
		var elems []json.RawMessage
		if err := json.Unmarshal(raw, &elems); err != nil {
			return nil, fmt.Errorf("expected array")
		}
		arr := make([]any, len(elems))
		for i, e := range elems {
			v, err := p.convertValue(n.elem, e)
			if err != nil {
				return nil, err
			}
			arr[i] = v
		}
		return arr, nil
	case kindUnion:
		return nil, fmt.Errorf("union elements inside arrays are not supported in JSON records")
	}
	return nil, fmt.Errorf("unsupported element kind")
}

func parseJSONInt(raw json.RawMessage, signed bool) (any, error) {
	s := string(raw)
	if signed {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("expected signed integer, got %s", s)
		}
		return v, nil
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("expected unsigned integer, got %s", s)
	}
	return v, nil
}

func toUint(v any) (uint64, error) {
	switch t := v.(type) {
	case uint64:
		return t, nil
	case int64:
		if t < 0 {
			return 0, fmt.Errorf("negative value %d", t)
		}
		return uint64(t), nil
	}
	return 0, fmt.Errorf("not an integer: %T", v)
}

// intRange returns the inclusive value range of an int field.
func intRange(bits int, signed bool) (min, max int64, umax uint64) {
	if signed {
		if bits == 64 {
			return math.MinInt64, math.MaxInt64, 0
		}
		return -(int64(1) << uint(bits-1)), int64(1)<<uint(bits-1) - 1, 0
	}
	if bits == 64 {
		return 0, 0, math.MaxUint64
	}
	return 0, 0, uint64(1)<<uint(bits) - 1
}
