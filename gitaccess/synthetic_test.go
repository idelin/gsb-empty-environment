package gitaccess

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"sort"
	"time"
)

// mustID parses a hex id or fails the test via panic-style helper.
func mustID(s string) ObjectID {
	id, err := ParseOID(s)
	if err != nil {
		panic(err)
	}
	return id
}

func blobObject(content string) (*Object, ObjectID) {
	obj := &Object{Type: BlobObject, Content: []byte(content)}
	return obj, hashObject(BlobObject, []byte(content))
}

// treeEntry is one synthetic tree entry. Entries are sorted by name on encode.
type treeEntry struct {
	mode string
	name string
	id   ObjectID
}

func treeObject(entries []treeEntry) (*Object, ObjectID) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	var buf bytes.Buffer
	for _, e := range entries {
		fmt.Fprintf(&buf, "%s %s", e.mode, e.name)
		buf.WriteByte(0)
		buf.Write(e.id[:])
	}
	obj := &Object{Type: TreeObject, Content: buf.Bytes()}
	return obj, hashObject(TreeObject, buf.Bytes())
}

type commitSpec struct {
	tree    ObjectID
	parents []ObjectID
	message string
}

func commitObject(c commitSpec) (*Object, ObjectID) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "tree %s\n", c.tree)
	for _, parent := range c.parents {
		fmt.Fprintf(&buf, "parent %s\n", parent)
	}
	fmt.Fprint(&buf, "author Synthetic <synthetic@example.invalid> 1700000000 +0000\n")
	fmt.Fprint(&buf, "committer Synthetic <synthetic@example.invalid> 1700000000 +0000\n\n")
	fmt.Fprint(&buf, c.message)
	obj := &Object{Type: CommitObject, Content: buf.Bytes()}
	return obj, hashObject(CommitObject, buf.Bytes())
}

func tagObject(target ObjectID, targetType, name string) (*Object, ObjectID) {
	content := fmt.Sprintf("object %s\ntype %s\ntag %s\ntagger Synthetic <synthetic@example.invalid> %d +0000\n\nrelease %s\n",
		target, targetType, name, time.Unix(1700000000, 0).Unix(), name)
	obj := &Object{Type: TagObject, Content: []byte(content)}
	return obj, hashObject(TagObject, obj.Content)
}

// addLoose stores an object in the database and returns its id.
func addLoose(db *ObjectDatabase, obj *Object) ObjectID {
	id, err := db.AddLoose(obj)
	if err != nil {
		panic(err)
	}
	return id
}

// packBuilder emits a real version 2 packfile plus the index records expected
// by OpenPack. It supports plain objects, OFS deltas and REF deltas.
type packBuilder struct {
	buf     bytes.Buffer
	index   []PackEntryInfo
	entries map[ObjectID]int64
}

func newPackBuilder() *packBuilder {
	pb := &packBuilder{entries: make(map[ObjectID]int64)}
	pb.buf.WriteString("PACK")
	binary.Write(&pb.buf, binary.BigEndian, uint32(2))
	pb.buf.Write([]byte{0, 0, 0, 0}) // entry count, patched in finish
	return pb
}

func (pb *packBuilder) AddObject(obj *Object) ObjectID {
	id := hashObject(obj.Type, obj.Content)
	offset := int64(pb.buf.Len())
	pb.writeEntryHeader(obj.Type, len(obj.Content))
	pb.writeZlib(obj.Content)
	pb.index = append(pb.index, PackEntryInfo{
		Offset:     offset,
		Compressed: int64(pb.buf.Len()) - offset,
		ID:         id,
	})
	pb.entries[id] = offset
	return id
}

// AddOfsDelta stores target as an OFS delta against base, which must already
// have been added to the same pack.
func (pb *packBuilder) AddOfsDelta(base, target *Object) ObjectID {
	if target.Type != base.Type {
		panic("delta base and target types must match")
	}
	baseOffset, ok := pb.entries[hashObject(base.Type, base.Content)]
	if !ok {
		panic("ofs delta base missing from pack")
	}
	id := hashObject(target.Type, target.Content)
	delta := buildDelta(base.Content, target.Content)

	offset := int64(pb.buf.Len())
	pb.writeEntryHeader(ofsDeltaKind, len(delta))
	pb.writeOffsetVarint(offset - baseOffset)
	pb.writeZlib(delta)
	pb.index = append(pb.index, PackEntryInfo{
		Offset:     offset,
		Compressed: int64(pb.buf.Len()) - offset,
		ID:         id,
	})
	pb.entries[id] = offset
	return id
}

// AddRefDelta stores target as a REF delta against an arbitrary base id, which
// need not live in this pack (this is how thin packs and broken bases are
// synthesized).
func (pb *packBuilder) AddRefDelta(baseID ObjectID, base, target *Object) ObjectID {
	if target.Type != base.Type {
		panic("delta base and target types must match")
	}
	id := hashObject(target.Type, target.Content)
	delta := buildDelta(base.Content, target.Content)

	offset := int64(pb.buf.Len())
	pb.writeEntryHeader(refDeltaKind, len(delta))
	pb.buf.Write(baseID[:])
	pb.writeZlib(delta)
	pb.index = append(pb.index, PackEntryInfo{
		Offset:     offset,
		Compressed: int64(pb.buf.Len()) - offset,
		ID:         id,
	})
	pb.entries[id] = offset
	return id
}

func (pb *packBuilder) finish() ([]byte, []PackEntryInfo) {
	data := pb.buf.Bytes()
	binary.BigEndian.PutUint32(data[8:12], uint32(len(pb.index)))
	trailer := sha1.Sum(data)
	data = append(data, trailer[:]...)
	return data, append([]PackEntryInfo(nil), pb.index...)
}

func (pb *packBuilder) writeEntryHeader(typ ObjectType, size int) {
	first := byte(typ) << 4
	if size < 16 {
		first |= byte(size)
		pb.buf.WriteByte(first)
		return
	}
	first |= byte(size & 0x0f)
	first |= 0x80
	pb.buf.WriteByte(first)
	rem := size >> 4
	for rem >= 0x80 {
		pb.buf.WriteByte(byte(rem&0x7f) | 0x80)
		rem >>= 7
	}
	pb.buf.WriteByte(byte(rem))
}

// writeOffsetVarint is the inverse of readOffsetVarint: the OFS_DELTA
// negative-relative-offset variable integer.
func (pb *packBuilder) writeOffsetVarint(distance int64) {
	var bytes_ []byte
	value := distance
	bytes_ = append(bytes_, byte(value&0x7f))
	for value >>= 7; value != 0; value >>= 7 {
		bytes_ = append(bytes_, byte((value-1)&0x7f))
	}
	for i := len(bytes_) - 1; i > 0; i-- {
		pb.buf.WriteByte(bytes_[i] | 0x80)
	}
	pb.buf.WriteByte(bytes_[0])
}

func (pb *packBuilder) writeZlib(content []byte) {
	zw := zlib.NewWriter(&pb.buf)
	if _, err := zw.Write(content); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
}

// buildDelta produces a valid Git delta that transforms base into target. It
// uses a single copy for the common prefix and an insert for the remainder,
// which is sufficient for realistic blob and tree deltas in these tests.
func buildDelta(base, target []byte) []byte {
	prefix := 0
	for prefix < len(base) && prefix < len(target) && base[prefix] == target[prefix] {
		prefix++
	}

	var buf bytes.Buffer
	writeLEB128(&buf, uint64(len(base)))
	writeLEB128(&buf, uint64(len(target)))
	if prefix > 0 {
		writeCopyOpcode(&buf, uint32(0), uint32(prefix))
	}
	if rest := target[prefix:]; len(rest) > 0 {
		writeInsertOpcode(&buf, rest)
	}
	return buf.Bytes()
}

func writeLEB128(buf *bytes.Buffer, value uint64) {
	for {
		b := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			buf.WriteByte(b | 0x80)
		} else {
			buf.WriteByte(b)
			return
		}
	}
}

func writeCopyOpcode(buf *bytes.Buffer, offset, size uint32) {
	var op byte = 0x80
	var operands []byte
	for bit := uint(0); bit < 4; bit++ {
		if b := byte((offset >> (8 * bit)) & 0xff); b != 0 {
			op |= 1 << bit
			operands = append(operands, b)
		}
	}
	for bit := uint(0); bit < 3; bit++ {
		if b := byte((size >> (8 * bit)) & 0xff); b != 0 {
			op |= 1 << (4 + bit)
			operands = append(operands, b)
		}
	}
	buf.WriteByte(op)
	buf.Write(operands)
}

func writeInsertOpcode(buf *bytes.Buffer, data []byte) {
	for len(data) > 127 {
		buf.WriteByte(127)
		buf.Write(data[:127])
		data = data[127:]
	}
	if len(data) > 0 {
		buf.WriteByte(byte(len(data)))
		buf.Write(data)
	}
}
