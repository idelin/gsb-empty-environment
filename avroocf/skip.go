package avroocf

import (
	"fmt"
	"io"
)

func skipValue(r io.Reader, schema *Schema, depth int, maxDepth int) error {
	switch schema.Kind {
	case KindNull:
		return nil
	case KindBoolean, KindInt, KindLong:
		_, err := readLong(r)
		return err
	case KindEnum:
		ordinal, err := readInt(r)
		if err != nil {
			return err
		}
		if ordinal < 0 || int(ordinal) >= len(schema.Symbols) {
			return fmt.Errorf("%w: ordinal %d for %d symbols",
				ErrEnumOutOfRange, ordinal, len(schema.Symbols))
		}
		return nil
	case KindFloat:
		_, err := readFull(r, 4)
		return err
	case KindDouble:
		_, err := readFull(r, 8)
		return err
	case KindBytes, KindString:
		value, err := readBytes(r)
		if err != nil {
			return err
		}
		_ = value
		return nil
	case KindFixed:
		_, err := readFull(r, schema.Size)
		return err
	case KindRecord:
		if err := enter(depth, maxDepth); err != nil {
			return err
		}
		for index := range schema.Fields {
			if err := skipValue(r, schema.Fields[index].Schema, depth+1, maxDepth); err != nil {
				return err
			}
		}
		return nil
	case KindArray:
		if err := enter(depth, maxDepth); err != nil {
			return err
		}
		return skipBlocks(r, schema.Items, depth, maxDepth, false)
	case KindMap:
		if err := enter(depth, maxDepth); err != nil {
			return err
		}
		return skipBlocks(r, schema.Values, depth, maxDepth, true)
	case KindUnion:
		if err := enter(depth, maxDepth); err != nil {
			return err
		}
		index, err := readLong(r)
		if err != nil {
			return err
		}
		if index < 0 || int(index) >= len(schema.Branches) {
			return fmt.Errorf("%w: branch %d of %d",
				ErrUnionOutOfRange, index, len(schema.Branches))
		}
		return skipValue(r, schema.Branches[index], depth+1, maxDepth)
	default:
		return fmt.Errorf("%w: cannot skip kind %s", ErrMalformed, schema.Kind)
	}
}

func skipBlocks(r io.Reader, element *Schema, depth int, maxDepth int, hasKeys bool) error {
	for {
		count, err := readLong(r)
		if err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		if count < 0 {
			blockSize, err := readLong(r)
			if err != nil {
				return err
			}
			if blockSize < 0 {
				return fmt.Errorf("%w: negative collection block size", ErrMalformed)
			}
			count = -count
		}
		for index := int64(0); index < count; index++ {
			if hasKeys {
				if _, err := readBytes(r); err != nil {
					return err
				}
			}
			if err := skipValue(r, element, depth+1, maxDepth); err != nil {
				return err
			}
		}
	}
}
