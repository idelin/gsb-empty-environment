package displaycommit

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

func suppliedCommitmentID(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case Hash:
		return x.String()
	case *Hash:
		if x == nil {
			return ""
		}
		return x.String()
	case [32]byte:
		return Hash(x).String()
	case []byte:
		if len(x) == 32 {
			return Hash(x).String()
		}
		return "bytes:" + hex.EncodeToString(x)
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

func sortedFields(fields []Field) []Field {
	out := append([]Field(nil), fields...)
	sort.SliceStable(out, func(i, j int) bool {
		ni, nj := out[i].number(), out[j].number()
		if ni != nj {
			return ni < nj
		}
		oi, oj := out[i].ordinal(), out[j].ordinal()
		if oi != oj {
			// Singular (-1) precedes indexed occurrences.
			if oi < 0 {
				return true
			}
			if oj < 0 {
				return false
			}
			return oi < oj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func appendLengthString(b *strings.Builder, s string) {
	fmt.Fprintf(b, "%d:%s;", len(s), s)
}

func appendCanonicalValue(b *strings.Builder, v Value) {
	switch v.effectiveKind() {
	case KindNull, "":
		b.WriteString("null;")
	case KindInt:
		n := "0"
		if v.Int != nil {
			n = v.Int.String()
		}
		b.WriteString("int:")
		appendLengthString(b, n)
	case KindBool:
		if v.Bool {
			b.WriteString("bool:1;")
		} else {
			b.WriteString("bool:0;")
		}
	case KindText:
		b.WriteString("text:")
		appendLengthString(b, v.Text)
	case KindAddress:
		b.WriteString("addr:")
		appendLengthString(b, v.Address.canonical())
	case KindBytes:
		b.WriteString("bytes:")
		appendLengthString(b, hex.EncodeToString(v.Bytes))
	case KindList:
		fmt.Fprintf(b, "list:%d{", len(v.Elements))
		for _, e := range v.Elements {
			appendCanonicalValue(b, e)
		}
		b.WriteString("};")
	case KindMessage:
		b.WriteString("msg:{")
		for _, f := range sortedFields(v.Fields) {
			appendCanonicalField(b, f)
		}
		b.WriteString("};")
	default:
		b.WriteString("unknown:")
		appendLengthString(b, fmt.Sprint(v.Kind))
	}
}

func appendCanonicalField(b *strings.Builder, f Field) {
	fmt.Fprintf(b, "f%d#%d/w%s=", f.number(), f.ordinal(), NormalizeWire(f.wire()))
	if f.Raw != nil {
		b.WriteString("raw:")
		appendLengthString(b, hex.EncodeToString(f.Raw))
		return
	}
	if id := suppliedCommitmentID(f.commitmentValue()); id != "" {
		b.WriteString("cid:")
		appendLengthString(b, id)
		return
	}
	appendCanonicalValue(b, f.Value)
	b.WriteString(";")
}

func CanonicalMessage(m Message) []byte {
	m = m.Normalized()
	var b strings.Builder
	b.WriteString("type:")
	appendLengthString(&b, m.typeName())
	b.WriteString("root:{")
	for _, f := range sortedFields(m.Fields) {
		appendCanonicalField(&b, f)
	}
	b.WriteString("}")
	return []byte(b.String())
}

func (m Message) ComputeRoot() Hash {
	nm := m.Normalized()
	return DomainHash("displaycommit/message-root/v1", CanonicalMessage(nm))
}

// RawRoot returns the supplied raw root when present, otherwise computes the
// canonical normalized-tree root.
func (m Message) RawRoot() Hash {
	return m.rootOrComputed()
}

// Resolved is a scalar or structured node reached by a path.
type Resolved struct {
	Path  Path
	Field Field
	Value Value
}

func scalarCommitment(f Field, v Value) string {
	if id := suppliedCommitmentID(f.commitmentValue()); id != "" {
		return id
	}
	if len(f.Raw) > 0 {
		return CommitRaw(f.wire(), f.Raw).String()
	}
	var b strings.Builder
	appendCanonicalValue(&b, v)
	return DomainHash("displaycommit/leaf/v1", []byte(NormalizeWire(f.wire())), []byte(b.String())).String()
}

// Leaves enumerates scalar leaves in canonical field/ordinal order.
func (m Message) Leaves() []Leaf {
	m = m.Normalized()
	var out []Leaf
	walkFields(m.Fields, nil, &out)
	return out
}

func walkFields(fields []Field, prefix Path, out *[]Leaf) {
	for _, f := range sortedFields(fields) {
		n := f.number()
		ord := f.ordinal()
		base := append(append(Path(nil), prefix...), PathStep{Field: n, Index: ord})
		v := f.Value
		switch v.Kind {
		case KindMessage:
			walkFields(v.Fields, base, out)
		case KindList:
			walkList(n, v.Elements, base, out)
		default:
			*out = append(*out, Leaf{
				FieldNumber:     n,
				WireType:        NormalizeWire(f.wire()),
				ValueCommitment: scalarCommitment(f, v),
				Ordinal:         ord,
				Path:            append(Path(nil), base...),
			})
		}
	}
}

func walkList(fieldNumber int, elements []Value, prefix Path, out *[]Leaf) {
	for i, ev := range elements {
		p := append(Path(nil), prefix...)
		// Replace the field occurrence ordinal with the list index for this
		// dimension; nested lists append another same-field index step.
		if len(p) > 0 && p[len(p)-1].Field == fieldNumber && p[len(p)-1].Index < 0 {
			p[len(p)-1].Index = i
		} else {
			p = append(p, PathStep{Field: fieldNumber, Index: i})
		}
		switch ev.Kind {
		case KindMessage:
			walkFields(ev.Fields, p, out)
		case KindList:
			walkList(fieldNumber, ev.Elements, p, out)
		default:
			f := Field{Number: fieldNumber, Wire: ev.Kind, Value: ev}
			*out = append(*out, Leaf{
				FieldNumber:     fieldNumber,
				WireType:        NormalizeWire(ev.Kind),
				ValueCommitment: scalarCommitment(f, ev),
				Ordinal:         i,
				Path:            p,
			})
		}
	}
}

// LeafMap returns path-keyed leaf commitments.
func (m Message) LeafMap() map[string]Leaf {
	leaves := m.Leaves()
	out := make(map[string]Leaf, len(leaves))
	for _, l := range leaves {
		out[l.Path.Key()] = l
	}
	return out
}

// Resolve finds all values matching a path. A singular terminal step can
// match every ordinal when the path was generated without an ordinal.
func (m Message) Resolve(path any) ([]Resolved, error) {
	p, err := NormalizePath(path)
	if err != nil {
		return nil, err
	}
	m = m.Normalized()
	return resolveFields(m.Fields, p), nil
}

func resolveFields(fields []Field, path Path) []Resolved {
	if len(path) == 0 {
		return nil
	}
	step := path[0]
	var out []Resolved
	for _, f := range fields {
		if f.number() != step.Field {
			continue
		}
		ord := f.ordinal()
		if step.Index >= 0 && ord >= 0 && ord != step.Index {
			continue
		}
		base := Path{{Field: step.Field, Index: ord}}
		if ord < 0 && step.Index >= 0 {
			base[0].Index = step.Index
		}

		if f.Value.Kind == KindList {
			out = append(out, resolveList(step.Field, f.Value.Elements, path, nil)...)
			continue
		}
		if len(path) == 1 {
			out = append(out, Resolved{Path: base, Field: f, Value: f.Value})
			continue
		}
		if f.Value.Kind == KindMessage {
			for _, r := range resolveFields(f.Value.Fields, path[1:]) {
				r.Path = append(append(Path(nil), base...), r.Path...)
				out = append(out, r)
			}
		}
	}
	return out
}

func resolveList(fieldNumber int, elements []Value, path Path, prefix Path) []Resolved {
	if len(path) == 0 {
		return nil
	}
	idx := path[0].Index
	if idx < 0 {
		var out []Resolved
		for i := range elements {
			wild := append(Path(nil), path...)
			wild[0].Index = i
			out = append(out, resolveList(fieldNumber, elements, wild, prefix)...)
		}
		return out
	}
	if idx < 0 || idx >= len(elements) {
		return nil
	}
	ev := elements[idx]
	p := append(Path(nil), prefix...)
	p = append(p, PathStep{Field: fieldNumber, Index: idx})
	if len(path) == 1 {
		return []Resolved{{Path: p, Value: ev}}
	}
	switch ev.Kind {
	case KindMessage:
		var out []Resolved
		for _, r := range resolveFields(ev.Fields, path[1:]) {
			r.Path = append(append(Path(nil), p...), r.Path...)
			out = append(out, r)
		}
		return out
	case KindList:
		return resolveList(fieldNumber, ev.Elements, path[1:], p)
	default:
		return nil
	}
}
