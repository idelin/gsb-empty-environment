package avroocf

// minEncodedSize returns a lower bound on the byte length of one encoded value
// of the schema. Recursive references contribute zero, which keeps the bound
// conservative while still exposing grossly inflated block counts.
func minEncodedSize(schema *Schema) int64 {
	return minSize(schema, make(map[*Schema]bool))
}

func minSize(schema *Schema, seen map[*Schema]bool) int64 {
	if schema == nil || seen[schema] {
		return 0
	}
	seen[schema] = true
	switch schema.Kind {
	case KindNull:
		return 0
	case KindBoolean, KindInt, KindLong:
		return 1
	case KindFloat:
		return 4
	case KindDouble:
		return 8
	case KindBytes, KindString:
		return 1
	case KindFixed:
		return int64(schema.Size)
	case KindEnum:
		return 1
	case KindRecord:
		var total int64
		for index := range schema.Fields {
			total += minSize(schema.Fields[index].Schema, seen)
		}
		return total
	case KindArray, KindMap:
		return 1
	case KindUnion:
		return 1
	default:
		return 0
	}
}
