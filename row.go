package taskpkg

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// newRow builds a Row from a full ref chain.
func newRow(refs []Ref) Row {
	return Row{
		Refs: refs,
		Key:  rowKey(refs),
		Hash: rowHash(refs),
		Path: rowPath(refs),
	}
}

// rowKey identifies a row by field names and stable element IDs. Unknown
// elements key on their field, missing markers on field plus tuple
// position, and elements without an ID fall back to their array index.
func rowKey(refs []Ref) string {
	var b strings.Builder
	for _, r := range refs {
		b.WriteString(r.Field)
		switch r.Kind {
		case RefUnknown:
			b.WriteString("?unknown")
		case RefMissing:
			fmt.Fprintf(&b, "!missing@%d", r.Index)
		default:
			if r.ID != "" {
				b.WriteString("#")
				b.WriteString(r.ID)
			} else {
				fmt.Fprintf(&b, "@%d", r.Index)
			}
		}
		b.WriteByte('/')
	}
	return b.String()
}

// rowHash fingerprints the key plus the values along the path.
func rowHash(refs []Ref) string {
	h := sha1.New()
	h.Write([]byte(rowKey(refs)))
	for _, r := range refs {
		enc, err := json.Marshal(r.Value)
		if err != nil {
			enc = []byte(fmt.Sprintf("%#v", r.Value))
		}
		h.Write(enc)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// rowPath renders the full element source path of a row.
func rowPath(refs []Ref) string {
	segs := make([]string, len(refs))
	for i, r := range refs {
		switch r.Kind {
		case RefUnknown:
			segs[i] = r.Field + "(?unknown)"
		case RefMissing:
			segs[i] = r.Field + "(!missing)"
		default:
			if r.ID != "" {
				segs[i] = fmt.Sprintf("%s[%d](#%s)", r.Field, r.Index, r.ID)
			} else {
				segs[i] = fmt.Sprintf("%s[%d]", r.Field, r.Index)
			}
		}
	}
	return strings.Join(segs, "/")
}
