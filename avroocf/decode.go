package avroocf

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// UnionValue is the generic representation of a decoded union value.
type UnionValue struct {
	// Type is the qualified name for named branches, otherwise the Avro
	// primitive/container type name.
	Type string
	// Value uses the same Go representation as Decode for the chosen branch.
	Value any
}

// Decode reads one object from the current block. The caller must pair every
// successful NextBlock with exactly its returned number of Decode or Skip
// calls.
func (r *Reader) Decode() (any, error) {
	if r.block == nil {
		return nil, fmt.Errorf("%w: Decode called without NextBlock", ErrMalformed)
	}
	if r.objectRead >= r.blockObjects {
		return nil, fmt.Errorf("%w: no objects left in block", ErrMalformed)
	}
	value, err := decodeValue(r.block, r.schema, 1, r.config.MaxSchemaDepth)
	if err != nil {
		return nil, err
	}
	r.objectRead++
	return value, nil
}

// Skip discards one object without allocating it.
func (r *Reader) Skip() error {
	if r.block == nil {
		return fmt.Errorf("%w: Skip called without NextBlock", ErrMalformed)
	}
	if r.objectRead >= r.blockObjects {
		return fmt.Errorf("%w: no objects left in block", ErrMalformed)
	}
	if err := skipValue(r.block, r.schema, 1, r.config.MaxSchemaDepth); err != nil {
		return err
	}
	r.objectRead++
	return nil
}

func enter(depth int, maxDepth int) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: value nesting exceeds %d", ErrDepthLimit, maxDepth)
	}
	return nil
}

func decodeValue(r io.Reader, schema *Schema, depth int, maxDepth int) (any, error) {
	switch schema.Kind {
	case KindNull:
		return nil, nil
	case KindBoolean:
		return decodeBoolean(r)
	case KindInt:
		value, err := readInt(r)
		return int(value), err
	case KindLong:
		return readLong(r)
	case KindFloat:
		var buffer [4]byte
		if _, err := io.ReadFull(r, buffer[:]); err != nil {
			return nil, err
		}
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(buffer[:]))), nil
	case KindDouble:
		var buffer [8]byte
		if _, err := io.ReadFull(r, buffer[:]); err != nil {
			return nil, err
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(buffer[:])), nil
	case KindBytes:
		return readBytes(r)
	case KindString:
		return readString(r)
	case KindEnum:
		ordinal, err := readInt(r)
		if err != nil {
			return nil, err
		}
		if ordinal < 0 || int(ordinal) >= len(schema.Symbols) {
			return nil, fmt.Errorf("%w: ordinal %d for %d symbols",
				ErrEnumOutOfRange, ordinal, len(schema.Symbols))
		}
		return schema.Symbols[ordinal], nil
	case KindFixed:
		return readFull(r, schema.Size)
	case KindRecord:
		return decodeRecord(r, schema, depth, maxDepth)
	case KindArray:
		return decodeArray(r, schema, depth, maxDepth)
	case KindMap:
		return decodeMap(r, schema, depth, maxDepth)
	case KindUnion:
		return decodeUnion(r, schema, depth, maxDepth)
	default:
		return nil, fmt.Errorf("%w: cannot decode kind %s", ErrMalformed, schema.Kind)
	}
}

func decodeBoolean(r io.Reader) (bool, error) {
	var buffer [1]byte
	if _, err := io.ReadFull(r, buffer[:]); err != nil {
		return false, err
	}
	switch buffer[0] {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("%w: invalid boolean byte %d", ErrMalformed, buffer[0])
	}
}

func decodeRecord(r io.Reader, schema *Schema, depth int, maxDepth int) (map[string]any, error) {
	if err := enter(depth, maxDepth); err != nil {
		return nil, err
	}
	result := make(map[string]any, len(schema.Fields))
	for index := range schema.Fields {
		field := schema.Fields[index]
		value, err := decodeValue(r, field.Schema, depth+1, maxDepth)
		if err != nil {
			return nil, err
		}
		result[field.Name] = value
	}
	return result, nil
}

func decodeArray(r io.Reader, schema *Schema, depth int, maxDepth int) ([]any, error) {
	if err := enter(depth, maxDepth); err != nil {
		return nil, err
	}
	result := make([]any, 0)
	for {
		count, err := readLong(r)
		if err != nil {
			return nil, err
		}
		if count == 0 {
			return result, nil
		}
		if count < 0 {
			blockSize, err := readLong(r)
			if err != nil {
				return nil, err
			}
			if blockSize < 0 {
				return nil, fmt.Errorf("%w: negative array block size", ErrMalformed)
			}
			count = -count
		}
		for index := int64(0); index < count; index++ {
			value, err := decodeValue(r, schema.Items, depth+1, maxDepth)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
	}
}

func decodeMap(r io.Reader, schema *Schema, depth int, maxDepth int) (map[string]any, error) {
	if err := enter(depth, maxDepth); err != nil {
		return nil, err
	}
	result := make(map[string]any)
	for {
		count, err := readLong(r)
		if err != nil {
			return nil, err
		}
		if count == 0 {
			return result, nil
		}
		if count < 0 {
			blockSize, err := readLong(r)
			if err != nil {
				return nil, err
			}
			if blockSize < 0 {
				return nil, fmt.Errorf("%w: negative map block size", ErrMalformed)
			}
			count = -count
		}
		for index := int64(0); index < count; index++ {
			key, err := readString(r)
			if err != nil {
				return nil, err
			}
			value, err := decodeValue(r, schema.Values, depth+1, maxDepth)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
	}
}

func decodeUnion(r io.Reader, schema *Schema, depth int, maxDepth int) (UnionValue, error) {
	if err := enter(depth, maxDepth); err != nil {
		return UnionValue{}, err
	}
	index, err := readLong(r)
	if err != nil {
		return UnionValue{}, err
	}
	if index < 0 || int(index) >= len(schema.Branches) {
		return UnionValue{}, fmt.Errorf("%w: branch %d of %d",
			ErrUnionOutOfRange, index, len(schema.Branches))
	}
	branch := schema.Branches[index]
	value, err := decodeValue(r, branch, depth+1, maxDepth)
	if err != nil {
		return UnionValue{}, err
	}
	return UnionValue{Type: branchKey(branch), Value: value}, nil
}
