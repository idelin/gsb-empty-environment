package pdfscan

import (
	"bytes"
	"strings"
)

// recoverWithoutXref is the fail-soft path for documents whose startxref
// chain cannot be parsed. It carves every indirect object and recovers the
// catalog from the last trailer dictionary's /Root entry.
func recoverWithoutXref(data []byte, cause error) (*xrefInfo, error) {
	carved := carveObjects(data)
	if len(carved) == 0 {
		return nil, cause
	}

	entries := make(map[int]xrefEntry, len(carved))
	for num := range carved {
		entries[num] = xrefEntry{typ: 1}
	}

	root := findCarvedRoot(data, carved)
	if root == nil {
		return nil, cause
	}
	return &xrefInfo{
		entries: entries,
		root:    root,
	}, nil
}

func findCarvedRoot(data []byte, carved map[int]*Value) *Value {
	// Prefer an explicit trailer dictionary (last occurrence wins).
	for off := bytes.LastIndex(data, []byte("trailer")); off >= 0; {
		p := newParser(data[off+len("trailer"):])
		if tv, err := p.parseValue(); err == nil && tv.Kind == KindDict {
			if r := tv.Dict.Get("Root"); r != nil && r.IsRef() {
				if _, ok := carved[r.RefNum]; ok {
					return r
				}
			}
		}
		break
	}
	// Otherwise look for a catalog object directly (last definition wins).
	var rootNum int
	for num, v := range carved {
		d := dictOf(v)
		if d == nil {
			continue
		}
		if name, ok := d.Get("Type").AsName(); ok && strings.EqualFold(name, "Catalog") {
			if num > rootNum {
				rootNum = num
			}
		}
	}
	if rootNum > 0 {
		return &Value{Kind: KindRef, RefNum: rootNum}
	}
	return nil
}

func dictOf(v *Value) *Dict {
	switch v.Kind {
	case KindDict:
		return v.Dict
	case KindStream:
		return v.Stream.Dict
	}
	return nil
}
