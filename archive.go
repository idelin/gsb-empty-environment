package dod

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"sort"
)

// Policy controls how time regression (t < previous t) is handled.
type Policy int

const (
	// RejectRegression returns ErrRegression on time going backwards.
	RejectRegression Policy = iota
	// AllowRegression closes the current run and starts a new epoch.
	AllowRegression
)

// Store is an append-only byte store. Appends never rewrite history.
type Store interface {
	io.ReaderAt
	Append(p []byte) (off int64, err error)
	Len() int64
}

// MemStore is an in-memory Store.
type MemStore struct {
	buf []byte
}

func (m *MemStore) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(m.buf)) {
		return 0, io.EOF
	}
	n := copy(p, m.buf[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *MemStore) Append(p []byte) (int64, error) {
	off := int64(len(m.buf))
	m.buf = append(m.buf, p...)
	return off, nil
}

func (m *MemStore) Len() int64 { return int64(len(m.buf)) }

// Bytes returns the raw archive contents (header + blocks).
func (m *MemStore) Bytes() []byte { return m.buf }

// IndexEntry locates one block for local decode. Superseded entries are
// retained so older versions of the index remain inspectable.
type IndexEntry struct {
	Offset     int64
	Length     int64
	Epoch      uint64
	T0, T1     int64
	Count      int
	Version    uint32
	Superseded bool
}

// Index maps time ranges to block offsets.
type Index struct {
	Entries []IndexEntry
}

// Live returns indices of non-superseded entries ordered by start time.
func (ix *Index) Live() []int {
	var out []int
	for i, e := range ix.Entries {
		if !e.Superseded {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(a, b int) bool { return ix.Entries[out[a]].T0 < ix.Entries[out[b]].T0 })
	return out
}

// SeekTime returns the position (within Live) of the first live block that
// may contain time t, or len(Live) if t is past the end.
func (ix *Index) SeekTime(t int64) int {
	live := ix.Live()
	return sort.Search(len(live), func(i int) bool { return ix.Entries[live[i]].T1 >= t })
}

// Encode serializes the index with explicit version, length and checksum.
func (ix *Index) Encode() []byte {
	p := []byte{FormatVersion}
	p = binary.AppendUvarint(p, uint64(len(ix.Entries)))
	for _, e := range ix.Entries {
		p = binary.AppendUvarint(p, uint64(e.Offset))
		p = binary.AppendUvarint(p, uint64(e.Length))
		p = binary.AppendUvarint(p, e.Epoch)
		p = binary.AppendVarint(p, e.T0)
		p = binary.AppendVarint(p, e.T1)
		p = binary.AppendUvarint(p, uint64(e.Count))
		p = binary.AppendUvarint(p, uint64(e.Version))
		var f byte
		if e.Superseded {
			f = 1
		}
		p = append(p, f)
	}
	out := binary.AppendUvarint(nil, uint64(len(p)+4))
	out = append(out, p...)
	var c [4]byte
	binary.LittleEndian.PutUint32(c[:], crc32.ChecksumIEEE(p))
	return append(out, c[:]...)
}

// DecodeIndex parses an index produced by Encode.
func DecodeIndex(b []byte) (*Index, error) {
	total, k := binary.Uvarint(b)
	if k <= 0 {
		return nil, ErrFormat
	}
	if total < 5 || total > uint64(len(b)-k) {
		return nil, ErrTruncated
	}
	payload := b[k : k+int(total)]
	body := payload[:len(payload)-4]
	if crc32.ChecksumIEEE(body) != binary.LittleEndian.Uint32(payload[len(payload)-4:]) {
		return nil, ErrChecksum
	}
	if body[0] != FormatVersion {
		return nil, ErrVersion
	}
	rest := body[1:]
	cnt, n := binary.Uvarint(rest)
	if n <= 0 {
		return nil, ErrFormat
	}
	rest = rest[n:]
	ix := &Index{}
	for i := uint64(0); i < cnt; i++ {
		var e IndexEntry
		var vv uint64
		var sv int64
		var kk int
		if vv, kk = binary.Uvarint(rest); kk <= 0 {
			return nil, ErrFormat
		}
		e.Offset, rest = int64(vv), rest[kk:]
		if vv, kk = binary.Uvarint(rest); kk <= 0 {
			return nil, ErrFormat
		}
		e.Length, rest = int64(vv), rest[kk:]
		if e.Epoch, kk = binary.Uvarint(rest); kk <= 0 {
			return nil, ErrFormat
		}
		rest = rest[kk:]
		if sv, kk = binary.Varint(rest); kk <= 0 {
			return nil, ErrFormat
		}
		e.T0, rest = sv, rest[kk:]
		if sv, kk = binary.Varint(rest); kk <= 0 {
			return nil, ErrFormat
		}
		e.T1, rest = sv, rest[kk:]
		if vv, kk = binary.Uvarint(rest); kk <= 0 {
			return nil, ErrFormat
		}
		e.Count, rest = int(vv), rest[kk:]
		if vv, kk = binary.Uvarint(rest); kk <= 0 {
			return nil, ErrFormat
		}
		e.Version, rest = uint32(vv), rest[kk:]
		if len(rest) < 1 {
			return nil, ErrTruncated
		}
		e.Superseded = rest[0] == 1
		rest = rest[1:]
		ix.Entries = append(ix.Entries, e)
	}
	return ix, nil
}

// Archive is an append-only delta-of-delta time series archive.
type Archive struct {
	st       Store
	policy   Policy
	maxBlock int
	maxVals  int
	ix       *Index
	epoch    uint64
	pt, pv   []int64
	lastT    int64
	hasLast  bool
	header   bool
}

// NewArchive creates an archive writing to st with the given regression policy.
func NewArchive(st Store, policy Policy) *Archive {
	return &Archive{st: st, policy: policy, maxBlock: 128, maxVals: 1 << 20, ix: &Index{}}
}

// SetMaxBlock bounds the block size considered by the optimizer.
func (a *Archive) SetMaxBlock(n int) { a.maxBlock = n }

// SetMaxValues bounds the number of values a single decode may produce.
func (a *Archive) SetMaxValues(n int) { a.maxVals = n }

// Index returns the archive index (includes superseded old versions).
func (a *Archive) Index() *Index { return a.ix }

// Add appends one point. Time regression is rejected or starts a new epoch
// depending on the policy.
func (a *Archive) Add(t, v int64) error {
	if a.hasLast && t < a.lastT {
		if a.policy == RejectRegression {
			return ErrRegression
		}
		if err := a.Flush(); err != nil {
			return err
		}
		a.epoch++
	}
	a.pt = append(a.pt, t)
	a.pv = append(a.pv, v)
	a.lastT, a.hasLast = t, true
	return nil
}

// Reset explicitly starts a new epoch at the next point.
func (a *Archive) Reset() error {
	if err := a.Flush(); err != nil {
		return err
	}
	a.epoch++
	a.hasLast = false
	return nil
}

// Flush encodes pending points into optimized blocks and appends them.
// Existing blocks are never rewritten.
func (a *Archive) Flush() error {
	if len(a.pt) == 0 {
		return nil
	}
	if !a.header {
		h := append([]byte{}, Magic...)
		h = append(h, FormatVersion)
		if _, err := a.st.Append(h); err != nil {
			return err
		}
		a.header = true
	}
	cuts := optimizeCuts(a.epoch, a.pt, a.pv, a.maxBlock)
	prev := 0
	for _, c := range cuts {
		blk, err := encodeBlock(a.epoch, a.pt[prev:c], a.pv[prev:c])
		if err != nil {
			return err
		}
		off, err := a.st.Append(blk)
		if err != nil {
			return err
		}
		a.ix.Entries = append(a.ix.Entries, IndexEntry{
			Offset: off, Length: int64(len(blk)), Epoch: a.epoch,
			T0: a.pt[prev], T1: a.pt[c-1], Count: c - prev,
		})
		prev = c
	}
	a.pt = a.pt[:0]
	a.pv = a.pv[:0]
	return nil
}

// Correct re-encodes the live block at position seq with replacement data
// (same epoch). New blocks are appended; the old index entry is kept but
// marked superseded, and new entries carry Version+1.
func (a *Archive) Correct(seq int, times, values []int64) error {
	if err := a.Flush(); err != nil {
		return err
	}
	live := a.ix.Live()
	if seq < 0 || seq >= len(live) {
		return ErrFormat
	}
	if len(times) == 0 || len(times) != len(values) {
		return ErrFormat
	}
	for k := 1; k < len(times); k++ {
		if times[k] < times[k-1] {
			return ErrRegression
		}
	}
	ei := live[seq]
	old := a.ix.Entries[ei]
	cuts := optimizeCuts(old.Epoch, times, values, a.maxBlock)
	type newEnt struct {
		blk []byte
		t0  int64
		t1  int64
		cnt int
	}
	var pending []newEnt
	prev := 0
	for _, c := range cuts {
		blk, err := encodeBlock(old.Epoch, times[prev:c], values[prev:c])
		if err != nil {
			return err
		}
		pending = append(pending, newEnt{blk, times[prev], times[c-1], c - prev})
		prev = c
	}
	a.ix.Entries[ei].Superseded = true
	for _, pe := range pending {
		off, err := a.st.Append(pe.blk)
		if err != nil {
			return err
		}
		a.ix.Entries = append(a.ix.Entries, IndexEntry{
			Offset: off, Length: int64(len(pe.blk)), Epoch: old.Epoch,
			T0: pe.t0, T1: pe.t1, Count: pe.cnt, Version: old.Version + 1,
		})
	}
	return nil
}

// VerifyHeader checks the archive magic and format version.
func VerifyHeader(st Store) error {
	buf := make([]byte, len(Magic)+1)
	if _, err := st.ReadAt(buf, 0); err != nil {
		return ErrTruncated
	}
	if !bytes.Equal(buf[:len(Magic)], Magic) {
		return ErrFormat
	}
	if buf[len(Magic)] != FormatVersion {
		return ErrVersion
	}
	return nil
}

func (a *Archive) readEntry(e IndexEntry) (*Block, error) {
	buf := make([]byte, e.Length)
	if _, err := a.st.ReadAt(buf, e.Offset); err != nil {
		return nil, ErrTruncated
	}
	blk, _, err := decodeBlock(buf, a.maxVals)
	return blk, err
}

// ReadBlock locally decodes the live block at position seq via the index.
func (a *Archive) ReadBlock(seq int) (*Block, error) {
	live := a.ix.Live()
	if seq < 0 || seq >= len(live) {
		return nil, ErrFormat
	}
	return a.readEntry(a.ix.Entries[live[seq]])
}

// SeekTime returns the live block position that may contain time t.
func (a *Archive) SeekTime(t int64) int { return a.ix.SeekTime(t) }

// ReadRange decodes all points with t0 <= t <= t1. maxOut bounds the number
// of returned points; ErrTooLarge is returned when it would be exceeded.
func (a *Archive) ReadRange(t0, t1 int64, maxOut int) ([]int64, []int64, error) {
	var ts, vs []int64
	live := a.ix.Live()
	for _, ei := range live {
		e := a.ix.Entries[ei]
		if e.T1 < t0 {
			continue
		}
		if e.T0 > t1 {
			break
		}
		blk, err := a.readEntry(e)
		if err != nil {
			return nil, nil, err
		}
		for k, t := range blk.Times {
			if t < t0 || t > t1 {
				continue
			}
			if len(ts) >= maxOut {
				return nil, nil, ErrTooLarge
			}
			ts = append(ts, t)
			vs = append(vs, blk.Values[k])
		}
	}
	return ts, vs, nil
}
