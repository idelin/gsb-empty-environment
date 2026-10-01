// Package taggedunion implements a tagged-union binary codec.
//
// Wire format (all integers big-endian):
//
//	batch: [count u32][item]...
//	item:  [tag u16][version u16][payloadLen u32][payload][crc32 IEEE of tag..payload]
//
// Supported serialization scope (deliberately narrow):
//   - field kinds: uint8, int32, string (u16 length prefix, max 65535 bytes)
//   - payload <= MaxPayload, batch items <= MaxItems
//   - decoding never executes host code; lengths are validated against the
//     remaining input BEFORE any allocation.
package taggedunion

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

const (
	headerSize = 8 // tag(2) + version(2) + payloadLen(4)
	crcSize    = 4

	// MaxPayload bounds any single payload; checked before allocation.
	MaxPayload = 1 << 20
	// MaxItems bounds batch element counts; checked before allocation.
	MaxItems = 1 << 16
)

// Error locates a failure by branch path and absolute byte offset.
type Error struct {
	Path   string
	Offset int
	Msg    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s (offset %d): %s", e.Path, e.Offset, e.Msg)
}

// Value is a decoded item. Understood items carry Fields; opaque items
// (unknown tag, or known tag with unknown version) keep Raw verbatim and
// are never reinterpreted through a coincidentally matching schema.
type Value struct {
	Tag     uint16
	Version uint16
	Opaque  bool
	Fields  map[string]any
	Raw     []byte // full original item bytes, set iff Opaque
}

// Payload returns the raw payload bytes of an opaque value.
func (v *Value) Payload() []byte {
	if !v.Opaque {
		return nil
	}
	return v.Raw[headerSize : len(v.Raw)-crcSize]
}

type migKey struct{ tag, from, to uint16 }

// Registry holds per-branch schemas and explicit version migrations.
type Registry struct {
	schemas    map[uint16]map[uint16]*Schema
	migrations map[migKey]func(map[string]any) (map[string]any, error)
}

func NewRegistry() *Registry {
	return &Registry{
		schemas:    make(map[uint16]map[uint16]*Schema),
		migrations: make(map[migKey]func(map[string]any) (map[string]any, error)),
	}
}

func (r *Registry) Register(s *Schema) {
	if r.schemas[s.Tag] == nil {
		r.schemas[s.Tag] = make(map[uint16]*Schema)
	}
	r.schemas[s.Tag][s.Version] = s
}

// RegisterMigration installs the explicit evolution mapping from->to.
func (r *Registry) RegisterMigration(tag, from, to uint16, fn func(map[string]any) (map[string]any, error)) {
	r.migrations[migKey{tag, from, to}] = fn
}

func (r *Registry) schema(tag, version uint16) *Schema {
	if byVer, ok := r.schemas[tag]; ok {
		return byVer[version]
	}
	return nil
}

// Migrate converts an understood value to another version. It refuses to
// guess: without an explicit registered mapping it returns an error, and
// opaque values can never be migrated.
func (r *Registry) Migrate(v *Value, to uint16) (*Value, error) {
	if v.Opaque {
		return nil, &Error{
			Path:   fmt.Sprintf("tag %d v%d", v.Tag, v.Version),
			Offset: 0,
			Msg:    "cannot migrate opaque value: branch not understood",
		}
	}
	if v.Version == to {
		fields := make(map[string]any, len(v.Fields))
		for k, val := range v.Fields {
			fields[k] = val
		}
		return &Value{Tag: v.Tag, Version: to, Fields: fields}, nil
	}
	fn, ok := r.migrations[migKey{v.Tag, v.Version, to}]
	if !ok {
		return nil, &Error{
			Path:   fmt.Sprintf("tag %d v%d->v%d", v.Tag, v.Version, to),
			Offset: 0,
			Msg:    "no explicit migration registered; refusing to guess",
		}
	}
	fields, err := fn(v.Fields)
	if err != nil {
		return nil, &Error{Path: fmt.Sprintf("tag %d v%d->v%d", v.Tag, v.Version, to), Offset: 0, Msg: err.Error()}
	}
	return &Value{Tag: v.Tag, Version: to, Fields: fields}, nil
}

// Encode serializes one value. Opaque values are emitted byte-for-byte
// from Raw; understood values are encoded through their registered schema.
func (r *Registry) Encode(v *Value) ([]byte, error) {
	if v.Opaque {
		out := make([]byte, len(v.Raw))
		copy(out, v.Raw)
		return out, nil
	}
	sch := r.schema(v.Tag, v.Version)
	if sch == nil {
		return nil, &Error{
			Path:   fmt.Sprintf("tag %d v%d", v.Tag, v.Version),
			Offset: 0,
			Msg:    "no schema registered for encoding; refusing to guess",
		}
	}
	payload, err := sch.encodeFields(v.Fields)
	if err != nil {
		return nil, err
	}
	return frame(v.Tag, v.Version, payload), nil
}

func frame(tag, version uint16, payload []byte) []byte {
	item := make([]byte, headerSize, headerSize+len(payload)+crcSize)
	binary.BigEndian.PutUint16(item[0:], tag)
	binary.BigEndian.PutUint16(item[2:], version)
	binary.BigEndian.PutUint32(item[4:], uint32(len(payload)))
	item = append(item, payload...)
	crc := crc32.ChecksumIEEE(item)
	var tail [crcSize]byte
	binary.BigEndian.PutUint32(tail[:], crc)
	return append(item, tail[:]...)
}

// DecodeItem decodes one item starting at off; path identifies the branch
// in error reports. Returns the value and the offset just past the item.
func (r *Registry) DecodeItem(buf []byte, off int, path string) (*Value, int, error) {
	if off < 0 || len(buf)-off < headerSize+crcSize {
		return nil, 0, &Error{path, off, "truncated item header"}
	}
	tag := binary.BigEndian.Uint16(buf[off:])
	version := binary.BigEndian.Uint16(buf[off+2:])
	payloadLen := binary.BigEndian.Uint32(buf[off+4:])
	// Validate length BEFORE any allocation.
	if payloadLen > MaxPayload {
		return nil, 0, &Error{path, off + 4, fmt.Sprintf("declared payload length %d exceeds limit %d", payloadLen, MaxPayload)}
	}
	total := headerSize + int(payloadLen) + crcSize
	if len(buf)-off < total {
		return nil, 0, &Error{path, off + 4, fmt.Sprintf("declared payload length %d exceeds %d remaining bytes", payloadLen, len(buf)-off-headerSize-crcSize)}
	}
	item := buf[off : off+total]
	want := binary.BigEndian.Uint32(item[len(item)-crcSize:])
	got := crc32.ChecksumIEEE(item[:len(item)-crcSize])
	if want != got {
		return nil, 0, &Error{path, off + total - crcSize, "checksum mismatch"}
	}
	payload := item[headerSize : len(item)-crcSize]
	sch := r.schema(tag, version)
	if sch == nil {
		// Unknown tag or unknown version: passthrough verbatim, keeping
		// version and checksum. Never reinterpret through an older schema
		// whose fields might coincidentally line up.
		raw := make([]byte, total)
		copy(raw, item)
		return &Value{Tag: tag, Version: version, Opaque: true, Raw: raw}, off + total, nil
	}
	fields, err := sch.decodeFields(payload, path, off+headerSize)
	if err != nil {
		return nil, 0, err
	}
	return &Value{Tag: tag, Version: version, Fields: fields}, off + total, nil
}

// EncodeBatch serializes a batch. Any item error aborts the whole encode.
func (r *Registry) EncodeBatch(vals []*Value) ([]byte, error) {
	if len(vals) > MaxItems {
		return nil, &Error{"batch", 0, fmt.Sprintf("item count %d exceeds limit %d", len(vals), MaxItems)}
	}
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, uint32(len(vals)))
	for i, v := range vals {
		item, err := r.Encode(v)
		if err != nil {
			return nil, &Error{fmt.Sprintf("batch[%d]", i), 0, err.Error()}
		}
		out = append(out, item...)
	}
	return out, nil
}

// DecodeBatch validates the whole batch atomically: either every item
// decodes or the call fails with the branch path and byte offset of the
// first offending item. No partial results are returned.
func (r *Registry) DecodeBatch(buf []byte) ([]*Value, error) {
	if len(buf) < 4 {
		return nil, &Error{"batch", 0, "truncated batch header"}
	}
	n := binary.BigEndian.Uint32(buf)
	// Validate count BEFORE allocation.
	if n > MaxItems {
		return nil, &Error{"batch", 0, fmt.Sprintf("declared item count %d exceeds limit %d", n, MaxItems)}
	}
	out := make([]*Value, 0, n)
	off := 4
	for i := uint32(0); i < n; i++ {
		v, next, err := r.DecodeItem(buf, off, fmt.Sprintf("batch[%d]", i))
		if err != nil {
			return nil, err
		}
		off = next
		out = append(out, v)
	}
	if off != len(buf) {
		return nil, &Error{"batch", off, fmt.Sprintf("%d trailing bytes after last item", len(buf)-off)}
	}
	return out, nil
}
