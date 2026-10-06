package layout

import (
	"fmt"
	"strings"
)

// Model lays t out byte by byte and returns one label per byte: the path of
// the field occupying that byte, "" for padding. Bytes shared by union
// members carry all occupants joined with "+". A zero-length array (or any
// zero-size member) occupies no bytes.
func Model(t *Type) ([]string, error) {
	l, err := LayoutOf(t)
	if err != nil {
		return nil, err
	}
	if l.Size == 0 {
		return nil, nil
	}
	buf := make([]string, l.Size)
	place(t, 0, nameOf(t), buf)
	return buf, nil
}

func nameOf(t *Type) string {
	if t.Name != "" {
		return t.Name
	}
	return fmt.Sprintf("kind%d", int(t.Kind))
}

func mustLayout(t *Type) *Layout {
	l, err := LayoutOf(t)
	if err != nil {
		panic(err) // unreachable: Model validated the whole tree first
	}
	return l
}

func place(t *Type, base int64, path string, buf []string) {
	switch t.Kind {
	case Int:
		for i := base; i < base+t.Size; i++ {
			buf[i] = merge(buf[i], path)
		}
	case Array:
		el := mustLayout(t.Elem)
		for i := int64(0); i < t.Count; i++ {
			place(t.Elem, base+i*el.Size, fmt.Sprintf("%s[%d]", path, i), buf)
		}
	case Struct:
		l := mustLayout(t)
		for i, f := range t.Fields {
			place(f.Type, base+l.Fields[i].Offset, path+"."+f.Name, buf)
		}
	case Union:
		for _, m := range t.Members {
			place(m.Type, base, path+"."+m.Name, buf)
		}
	}
}

func merge(existing, label string) string {
	if existing == "" {
		return label
	}
	return existing + "+" + label
}

// Describe returns a human-readable tree of every field's offset, size and
// alignment, e.g. for printing layouts.
func Describe(t *Type) (string, error) {
	if _, err := LayoutOf(t); err != nil {
		return "", err
	}
	var b strings.Builder
	describe(&b, t, 0, 0, nameOf(t))
	return b.String(), nil
}

func describe(b *strings.Builder, t *Type, depth int, offset int64, path string) {
	l := mustLayout(t)
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s%s: offset=%d size=%d align=%d\n", indent, path, offset, l.Size, l.Align)
	switch t.Kind {
	case Struct:
		for i, f := range t.Fields {
			describe(b, f.Type, depth+1, offset+l.Fields[i].Offset, path+"."+f.Name)
		}
	case Union:
		for _, m := range t.Members {
			describe(b, m.Type, depth+1, offset, path+"."+m.Name)
		}
	case Array:
		el := mustLayout(t.Elem)
		fmt.Fprintf(b, "%s  stride=%d count=%d\n", indent, el.Size, t.Count)
	}
}
