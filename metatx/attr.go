// Package metatx provides a transactional metadata pipeline for the publish
// host. Content moved from the upload area to the publish area must not
// silently inherit executable or trust attributes from its source; instead
// each attribute namespace is explicitly preserved, dropped, or re-signed,
// and content plus attributes are committed in a single transaction.
package metatx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// Attribute is a single namespaced metadata entry attached to content.
type Attribute struct {
	Namespace string
	Key       string
	Value     []byte
}

// id returns the identity used for duplicate detection and canonical order.
func (a Attribute) id() string { return a.Namespace + "\x00" + a.Key }

// ErrDuplicateAttribute is returned when the same namespace/key pair is
// encoded more than once.
var ErrDuplicateAttribute = errors.New("metatx: duplicate attribute")

// EncodeAttributes serializes attrs into a deterministic canonical form:
// entries are sorted by (namespace, key) and every field is length-prefixed.
// A duplicate namespace/key pair aborts the encoding with
// ErrDuplicateAttribute.
func EncodeAttributes(attrs []Attribute) ([]byte, error) {
	sorted := make([]Attribute, len(attrs))
	copy(sorted, attrs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].id() < sorted[j].id() })

	var buf bytes.Buffer
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(sorted)))
	buf.Write(count[:])
	for i, a := range sorted {
		if i > 0 && sorted[i-1].id() == a.id() {
			return nil, fmt.Errorf("%w: %s/%s", ErrDuplicateAttribute, a.Namespace, a.Key)
		}
		writeField(&buf, []byte(a.Namespace))
		writeField(&buf, []byte(a.Key))
		writeField(&buf, a.Value)
	}
	return buf.Bytes(), nil
}

func writeField(buf *bytes.Buffer, b []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b)))
	buf.Write(n[:])
	buf.Write(b)
}
