package gitaccess

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"fmt"
	"io"
)

// maxDeltaChain bounds the number of delta links materialized for a single
// object, protecting against pathological or malicious packs.
const maxDeltaChain = 128

const packMagic = "PACK"

type deltaKind int

const (
	noDelta deltaKind = iota
	ofsDelta
	refDelta
)

// PackEntryInfo is one record of the index that accompanies a packfile (the
// in-memory counterpart of Git's .idx). It locates an entry's compressed
// region inside the pack and states the object id the entry must materialize
// to. The id is required so reconstructed content can be digest checked.
type PackEntryInfo struct {
	Offset     int64
	Compressed int64
	ID         ObjectID
}

// packEntry keeps one pack entry's inflated payload together with the metadata
// needed to materialize it on demand.
type packEntry struct {
	typ        ObjectType // declared object type; for deltas the base type
	rawSize    int64      // inflated size from the entry header
	offset     int64      // absolute offset of the entry header
	delta      deltaKind
	baseOffset int64    // OFS_DELTA: absolute offset of the base entry
	baseID     ObjectID // REF_DELTA: id of the base object
	inflated   []byte
	wantID     ObjectID
}

// Pack is a parsed packfile. Entries are delta-reconstructed lazily, so digest
// checks happen only when an object is actually read through a capability.
type Pack struct {
	entries  []*packEntry
	byOffset map[int64]*packEntry
	byID     map[ObjectID]*packEntry

	// stepHook is consulted once per delta chain step. It lets tests drive
	// cancellation deterministically; production code leaves it nil.
	stepHook func() error
}

// sectionReader reads only the half-open region [off, end) of data. Bounding
// every zlib stream makes inflate independent of the buffering the zlib reader
// performs past the logical end of a stream.
type sectionReader struct {
	data []byte
	off  int64
	end  int64
}

func (s *sectionReader) Read(p []byte) (int, error) {
	if s.off >= s.end {
		return 0, io.EOF
	}
	n := copy(p, s.data[s.off:s.end])
	s.off += int64(n)
	return n, nil
}

// OpenPack parses and verifies the structural integrity of a packfile using
// its accompanying index: magic, version, entry count, entry framing, per
// entry zlib streams and the trailing SHA-1 checksum.
//
// info must be ordered by offset; the final entry's region ends exactly at the
// 20 byte checksum trailer. Content digests are checked lazily, when an entry
// is materialized through the database.
func OpenPack(data []byte, info []PackEntryInfo) (*Pack, error) {
	if len(data) < 12+OIDSize {
		return nil, fmt.Errorf("%w: pack too short", ErrBadPack)
	}
	if string(data[0:4]) != packMagic {
		return nil, fmt.Errorf("%w: bad magic", ErrBadPack)
	}
	if version := binary.BigEndian.Uint32(data[4:8]); version != 2 {
		return nil, fmt.Errorf("%w: unsupported pack version %d", ErrBadPack, version)
	}
	if count := binary.BigEndian.Uint32(data[8:12]); int(count) != len(info) {
		return nil, fmt.Errorf("%w: header count %d != index count %d", ErrBadPack, count, len(info))
	}

	trailer := data[len(data)-OIDSize:]
	if digest := sha1Sum(data[:len(data)-OIDSize]); !bytes.Equal(trailer, digest[:]) {
		return nil, fmt.Errorf("%w: pack trailer checksum mismatch", ErrBadPack)
	}

	p := &Pack{
		entries:  make([]*packEntry, 0, len(info)),
		byOffset: make(map[int64]*packEntry, len(info)),
		byID:     make(map[ObjectID]*packEntry, len(info)),
	}

	trailerStart := int64(len(data) - OIDSize)
	for i, rec := range info {
		regionEnd := trailerStart
		if i+1 < len(info) {
			regionEnd = info[i+1].Offset
		}
		if rec.Offset < 12 || rec.Compressed <= 0 || rec.Offset+rec.Compressed > regionEnd {
			return nil, fmt.Errorf("%w: illegal index region for entry at %d", ErrBadPack, rec.Offset)
		}

		entry, err := readPackEntry(data, rec)
		if err != nil {
			return nil, err
		}
		if rec.Offset+rec.Compressed != regionEnd {
			return nil, fmt.Errorf("%w: entry at %d leaves gap or overruns before offset %d", ErrBadPack, rec.Offset, regionEnd)
		}
		p.entries = append(p.entries, entry)
		p.byOffset[entry.offset] = entry
		if !entry.wantID.IsZero() {
			p.byID[entry.wantID] = entry
		}
	}
	return p, nil
}

func readPackEntry(data []byte, rec PackEntryInfo) (*packEntry, error) {
	r := bytes.NewReader(data)
	if _, err := r.Seek(rec.Offset, io.SeekStart); err != nil {
		return nil, err
	}

	first, err := r.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read entry header: %v", ErrBadPack, err)
	}
	kind := ObjectType((first >> 4) & 0x07)
	size := int64(first & 0x0f)
	shift := uint(4)
	for first&0x80 != 0 {
		b, err := r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("%w: truncated size varint: %v", ErrBadPack, err)
		}
		first = b
		size |= int64(b&0x7f) << shift
		shift += 7
	}

	entry := &packEntry{typ: kind, rawSize: size, offset: rec.Offset, wantID: rec.ID}
	switch kind {
	case ofsDeltaKind:
		entry.delta = ofsDelta
		distance, err := readOffsetVarint(r)
		if err != nil {
			return nil, err
		}
		entry.baseOffset = rec.Offset - distance
	case refDeltaKind:
		entry.delta = refDelta
		if _, err := io.ReadFull(r, entry.baseID[:]); err != nil {
			return nil, fmt.Errorf("%w: truncated REF_DELTA base id: %v", ErrBadPack, err)
		}
	case CommitObject, TreeObject, BlobObject, TagObject:
	default:
		return nil, fmt.Errorf("%w: unknown entry type %d", ErrBadPack, kind)
	}

	compStart, _ := r.Seek(0, io.SeekCurrent)
	compEnd := rec.Offset + rec.Compressed
	if compStart > compEnd {
		return nil, fmt.Errorf("%w: entry header overruns indexed region at %d", ErrBadPack, rec.Offset)
	}

	section := &sectionReader{data: data, off: compStart, end: compEnd}
	zr, err := zlib.NewReader(section)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot open zlib stream at %d: %v", ErrBadPack, rec.Offset, err)
	}
	inflated, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot inflate entry at %d: %v", ErrBadPack, rec.Offset, err)
	}
	if err := zr.Close(); err != nil {
		return nil, fmt.Errorf("%w: closing zlib stream at %d: %v", ErrBadPack, rec.Offset, err)
	}
	if section.off != compEnd {
		return nil, fmt.Errorf("%w: zlib stream at %d ended before region boundary", ErrBadPack, rec.Offset)
	}
	if int64(len(inflated)) != size {
		return nil, fmt.Errorf("%w: inflated size %d != header size %d at %d", ErrBadPack, len(inflated), size, rec.Offset)
	}

	entry.inflated = inflated
	return entry, nil
}

// readOffsetVarint implements the OFS_DELTA negative relative offset encoding.
func readOffsetVarint(r *bytes.Reader) (int64, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, fmt.Errorf("%w: truncated offset varint: %v", ErrBadPack, err)
	}
	value := int64(b & 0x7f)
	for b&0x80 != 0 {
		b, err = r.ReadByte()
		if err != nil {
			return 0, fmt.Errorf("%w: truncated offset varint: %v", ErrBadPack, err)
		}
		value = ((value + 1) << 7) | int64(b&0x7f)
	}
	return value, nil
}

// materialize reconstructs the entry's object. Deltas are resolved against the
// owning database; every reconstructed object is content-digest checked
// against its declared id before it is returned or cached.
//
// memo is scoped to a single materialization call tree, so a canceled or
// failed read never leaves partial state in a shared cache.
func (p *Pack) materialize(ctx context.Context, db *ObjectDatabase, entry *packEntry, depth int, memo map[int64]*Object) (*Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.stepHook != nil {
		if err := p.stepHook(); err != nil {
			return nil, err
		}
	}
	if obj, ok := memo[entry.offset]; ok {
		return obj, nil
	}
	if depth > maxDeltaChain {
		return nil, fmt.Errorf("%w", ErrDeltaChainTooDeep)
	}

	if entry.delta == noDelta {
		obj := &Object{Type: entry.typ, Content: entry.inflated}
		if err := verifyDigest(obj, entry.wantID); err != nil {
			return nil, err
		}
		memo[entry.offset] = obj
		return obj, nil
	}

	base, err := p.resolveBase(ctx, db, entry, depth, memo)
	if err != nil {
		return nil, err
	}
	content, err := applyDelta(base.Content, entry.inflated)
	if err != nil {
		return nil, err
	}
	obj := &Object{Type: base.Type, Content: content}
	if err := verifyDigest(obj, entry.wantID); err != nil {
		return nil, err
	}
	memo[entry.offset] = obj
	return obj, nil
}

func (p *Pack) resolveBase(ctx context.Context, db *ObjectDatabase, entry *packEntry, depth int, memo map[int64]*Object) (*Object, error) {
	switch entry.delta {
	case ofsDelta:
		baseEntry, ok := p.byOffset[entry.baseOffset]
		if !ok {
			return nil, fmt.Errorf("%w: OFS_DELTA base at offset %d", ErrDeltaBaseMissing, entry.baseOffset)
		}
		return p.materialize(ctx, db, baseEntry, depth+1, memo)
	case refDelta:
		if baseEntry, ok := p.byID[entry.baseID]; ok {
			return p.materialize(ctx, db, baseEntry, depth+1, memo)
		}
		// Thin pack: the base may live in another pack or as a loose object.
		base, err := db.load(ctx, entry.baseID, p)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrDeltaBaseMissing, entry.baseID, err)
		}
		return base, nil
	default:
		return nil, fmt.Errorf("%w: entry is not a delta", ErrBadPack)
	}
}

// verifyDigest recomputes the Git SHA-1 content digest and compares it with
// the id the object is claimed to have. No caller returns object content
// before this check succeeds.
func verifyDigest(obj *Object, want ObjectID) error {
	got := hashObject(obj.Type, obj.Content)
	if got != want {
		return fmt.Errorf("%w: got %s, want %s (%s)", ErrChecksumMismatch, got, want, obj.Type)
	}
	return nil
}

// applyDelta applies a Git delta instruction stream (base size and result size
// LEB128 prefixes, then copy/insert opcodes) to base and returns the result.
func applyDelta(base, delta []byte) ([]byte, error) {
	baseSize, n, err := readLEB128(delta)
	if err != nil {
		return nil, fmt.Errorf("%w: delta base size: %v", ErrBadPack, err)
	}
	if int64(len(base)) != baseSize {
		return nil, fmt.Errorf("%w: delta base size %d != actual %d", ErrBadPack, baseSize, len(base))
	}
	resultSize, m, err := readLEB128(delta[n:])
	if err != nil {
		return nil, fmt.Errorf("%w: delta result size: %v", ErrBadPack, err)
	}
	opcodes := delta[n+m:]

	result := make([]byte, 0, resultSize)
	for i := 0; i < len(opcodes); {
		op := opcodes[i]
		i++
		switch {
		case op&0x80 != 0: // copy from base
			var offset, size uint32
			for bit := uint(0); bit < 4; bit++ {
				if op&(1<<bit) != 0 {
					if i >= len(opcodes) {
						return nil, fmt.Errorf("%w: truncated copy opcode", ErrBadPack)
					}
					offset |= uint32(opcodes[i]) << (8 * bit)
					i++
				}
			}
			for bit := uint(0); bit < 3; bit++ {
				if op&(1<<(4+bit)) != 0 {
					if i >= len(opcodes) {
						return nil, fmt.Errorf("%w: truncated copy opcode", ErrBadPack)
					}
					size |= uint32(opcodes[i]) << (8 * bit)
					i++
				}
			}
			if size == 0 {
				size = 0x10000
			}
			if uint64(offset)+uint64(size) > uint64(len(base)) {
				return nil, fmt.Errorf("%w: copy range [%d,+%d) exceeds base of %d", ErrBadPack, offset, size, len(base))
			}
			result = append(result, base[offset:offset+size]...)
		case op != 0: // insert literal bytes
			if i+int(op) > len(opcodes) {
				return nil, fmt.Errorf("%w: truncated insert opcode", ErrBadPack)
			}
			result = append(result, opcodes[i:i+int(op)]...)
			i += int(op)
		default: // opcode 0 is reserved
			return nil, fmt.Errorf("%w: unexpected delta opcode 0", ErrBadPack)
		}
		if int64(len(result)) > resultSize {
			return nil, fmt.Errorf("%w: result overshoots declared size %d", ErrBadPack, resultSize)
		}
	}
	if int64(len(result)) != resultSize {
		return nil, fmt.Errorf("%w: result size %d != declared %d", ErrBadPack, resultSize, len(result))
	}
	return result, nil
}

// readLEB128 reads an unsigned LEB128 varint and returns the value and the
// number of bytes consumed.
func readLEB128(buf []byte) (int64, int, error) {
	var value uint64
	var shift uint
	for i, b := range buf {
		if i >= 10 {
			return 0, 0, fmt.Errorf("varint too long")
		}
		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return int64(value), i + 1, nil
		}
		shift += 7
	}
	return 0, 0, fmt.Errorf("truncated varint")
}
