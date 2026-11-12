package netsec

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"sync"
)

// Kind describes a committed record in the audit log.
type Kind uint8

const (
	// KindData is a record whose payload was fully written and committed.
	KindData Kind = 1
	// KindHole is an explicit hole covering a reservation that was cancelled,
	// failed, or was interrupted. Hole payload bytes are zero.
	KindHole Kind = 2
)

func (k Kind) String() string {
	switch k {
	case KindData:
		return "data"
	case KindHole:
		return "hole"
	default:
		return fmt.Sprintf("kind(%d)", uint8(k))
	}
}

// Errors returned by the append handle. They are intentionally stable so a
// service can distinguish a closed log, an exhausted reservation, and a
// non-durable commit.
var (
	ErrClosed        = errors.New("netsec: log is closed")
	ErrResolved      = errors.New("netsec: reservation is already resolved")
	ErrOverflow      = errors.New("netsec: write exceeds reservation")
	ErrIncomplete    = errors.New("netsec: reservation payload is incomplete")
	ErrInvalidLength = errors.New("netsec: invalid record length")
	ErrCorrupt       = errors.New("netsec: corrupt audit record")
	ErrShortWrite    = errors.New("netsec: short write")
)

const (
	recordMagic   = "NETSEC01"
	recordVersion = byte(1)

	trailerMagic   = "NETSEND1"
	headerSize     = 32
	trailerSize    = 16
	recordOverhead = headerSize + trailerSize

	// maxPayload guards against overflow and against a corrupt header asking
	// recovery to materialise an absurd hole.
	maxPayload = int64(1) << 40

	ioChunk = 32 << 10
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// Store is the byte-level volume used by a Log. Implementations must be safe
// for concurrent WriteAt calls on disjoint ranges and must not truncate or
// reorder data. The Log allocates every offset itself: callers of a Log never
// choose a write position, and a Log only writes inside a reservation it
// allocated.
//
// Store is exported so tests and services can wrap a volume for fault
// injection. A Log does not expose its Store, so a service cannot use it to
// seek or overwrite committed bytes.
type Store interface {
	WriteAt(p []byte, off int64) (int, error)
	ReadAt(p []byte, off int64) (int, error)
	Size() (int64, error)
	Sync() error
	Close() error
}

type fileStore struct {
	f *os.File
}

func (s *fileStore) WriteAt(p []byte, off int64) (int, error) { return s.f.WriteAt(p, off) }
func (s *fileStore) ReadAt(p []byte, off int64) (int, error)  { return s.f.ReadAt(p, off) }
func (s *fileStore) Sync() error                              { return s.f.Sync() }
func (s *fileStore) Close() error                             { return s.f.Close() }

func (s *fileStore) Size() (int64, error) {
	fi, err := s.f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// Record describes one committed record. Offset and DataOffset are assigned
// by the server and are read-only observations; they are never accepted as a
// write position from a caller.
type Record struct {
	Offset     int64
	DataOffset int64
	Length     int64
	Kind       Kind
	// Data is populated by Snapshot. It is nil for holes and for the Record
	// returned by Reservation.Record before the record is read back.
	Data []byte
}

// End returns the offset immediately after the record's framing and payload.
func (r Record) End() int64 { return r.Offset + recordOverhead + r.Length }

type recordMeta struct {
	start      int64
	payloadLen int64
	kind       Kind
}

func (m recordMeta) record() Record {
	return Record{
		Offset:     m.start,
		DataOffset: m.start + headerSize,
		Length:     m.payloadLen,
		Kind:       m.kind,
	}
}

type resolvedSpan struct {
	start      int64
	end        int64
	payloadLen int64
	kind       Kind
}

// Log is an append-only audit handle. Every append reserves a disjoint span
// from the log's allocator; callers can only move forward through the
// reservation they were given.
//
// A record becomes visible only after it is committed, and only records in
// the contiguous committed prefix are returned by Snapshot. A cancelled or
// failed reservation is never returned to the allocator: it is sealed as an
// explicit KindHole record. Later appends therefore start after the hole.
type Log struct {
	store    Store
	ownStore bool

	mu         sync.Mutex
	notify     chan struct{}
	allocEnd   int64
	visibleEnd int64
	closed     bool
	records    []recordMeta
	resolved   map[int64]resolvedSpan
	active     map[*Reservation]struct{}
}

// Open opens (or creates) path and recovers any interrupted tail. The file is
// opened read-write because recovery may need to inspect and seal an
// uncommitted tail, but the resulting Log never exposes a writable handle to
// its caller.
func Open(path string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	l, err := newLog(&fileStore{f: f}, true)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return l, nil
}

// OpenStore opens a Log over an existing Store and performs the same recovery
// as Open. The caller retains ownership of s; Log.Close does not close it.
func OpenStore(s Store) (*Log, error) {
	if s == nil {
		return nil, errors.New("netsec: nil store")
	}
	return newLog(s, false)
}

func newLog(s Store, own bool) (*Log, error) {
	records, end, err := recoverStore(s)
	if err != nil {
		return nil, err
	}
	l := &Log{
		store:      s,
		ownStore:   own,
		allocEnd:   end,
		visibleEnd: end,
		records:    records,
		resolved:   make(map[int64]resolvedSpan),
		active:     make(map[*Reservation]struct{}),
		notify:     make(chan struct{}),
	}
	return l, nil
}

// Reserve atomically allocates a payload of n bytes. The returned reservation
// owns one contiguous span; its offset cannot be changed. A reservation must
// end in exactly one of Commit or Cancel. Reservations that are neither
// committed nor sealed when the process stops are sealed as holes on the next
// OpenStore.
func (l *Log) Reserve(n int64) (*Reservation, error) {
	if n < 0 || n > maxPayload {
		return nil, ErrInvalidLength
	}
	total := recordOverhead + n
	if total < recordOverhead {
		return nil, ErrInvalidLength
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, ErrClosed
	}
	if l.allocEnd > math.MaxInt64-total {
		return nil, ErrInvalidLength
	}

	start := l.allocEnd
	hdr := encodeHeader(start, n)
	if _, err := writeFullAt(l.store, hdr, start); err != nil {
		// The allocator has not advanced, so no reservation was published.
		// A partially written header is uncommitted tail and will be sealed
		// by recovery if the process stops here.
		return nil, fmt.Errorf("netsec: reserve header: %w", err)
	}
	l.allocEnd = start + total

	r := &Reservation{
		log:        l,
		start:      start,
		dataStart:  start + headerSize,
		payloadLen: n,
		crc:        crc32.Update(0, crcTable, []byte{byte(KindData)}),
		state:      reservationActive,
	}
	l.active[r] = struct{}{}
	return r, nil
}

// Append reserves one record containing the supplied segments and commits it.
// Segments are written in order without copying them into one buffer. A
// cancellation or write fault stops the append and seals the whole
// reservation as a hole; a partially written record is never exposed.
func (l *Log) Append(ctx context.Context, segments ...[]byte) (Record, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	var total int64
	for _, seg := range segments {
		if int64(len(seg)) > maxPayload-total {
			return Record{}, ErrInvalidLength
		}
		total += int64(len(seg))
	}

	r, err := l.Reserve(total)
	if err != nil {
		return Record{}, err
	}
	for _, seg := range segments {
		if _, err := r.WriteContext(ctx, seg); err != nil {
			if cancelErr := r.Cancel(); cancelErr != nil {
				return Record{}, errors.Join(err, cancelErr)
			}
			return Record{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		if cancelErr := r.Cancel(); cancelErr != nil {
			return Record{}, errors.Join(err, cancelErr)
		}
		return Record{}, err
	}
	if err := r.Commit(); err != nil {
		return Record{}, err
	}
	return r.Record(), nil
}

// Write appends p as one record using a background context.
func (l *Log) Write(p []byte) (int, error) {
	if _, err := l.Append(context.Background(), p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// WriteContext is Write with cancellation.
func (l *Log) WriteContext(ctx context.Context, p []byte) (int, error) {
	if _, err := l.Append(ctx, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// VisibleEnd returns the end offset of the contiguous committed prefix.
func (l *Log) VisibleEnd() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.visibleEnd
}

// CommittedEnd is an alias for VisibleEnd.
func (l *Log) CommittedEnd() int64 { return l.VisibleEnd() }

// AllocatedEnd returns the end of the allocator, including reservations that
// are still pending or have already become holes.
func (l *Log) AllocatedEnd() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.allocEnd
}

// WaitVisible waits until the committed prefix reaches off. It never exposes
// bytes beyond that prefix.
func (l *Log) WaitVisible(ctx context.Context, off int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		l.mu.Lock()
		if l.visibleEnd >= off {
			l.mu.Unlock()
			return nil
		}
		if l.closed {
			l.mu.Unlock()
			return ErrClosed
		}
		wait := l.notify
		l.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Snapshot returns the records in the committed prefix, in offset order.
// Holes carry their reserved length but no payload; data records are read
// back from the volume.
func (l *Log) Snapshot() ([]Record, error) {
	l.mu.Lock()
	metas := append([]recordMeta(nil), l.records...)
	l.mu.Unlock()

	out := make([]Record, len(metas))
	for i, m := range metas {
		out[i] = m.record()
		if m.kind != KindData || m.payloadLen == 0 {
			continue
		}
		data := make([]byte, m.payloadLen)
		if err := readFullAt(l.store, data, m.start+headerSize); err != nil {
			return nil, fmt.Errorf("netsec: read record at %d: %w", m.start, err)
		}
		out[i].Data = data
	}
	return out, nil
}

// Close seals any reservation still owned by the log as a hole, syncs the
// volume, and closes it when the Log opened the volume itself. It does not
// truncate or rewrite any committed byte.
func (l *Log) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	active := make([]*Reservation, 0, len(l.active))
	for r := range l.active {
		active = append(active, r)
	}
	l.broadcastLocked()
	l.mu.Unlock()

	var errs []error
	for _, r := range active {
		if err := r.Cancel(); err != nil {
			errs = append(errs, err)
		}
	}
	if l.ownStore {
		if err := l.store.Sync(); err != nil {
			errs = append(errs, err)
		}
		if err := l.store.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (l *Log) resolve(r *Reservation, kind Kind) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.resolved[r.start] = resolvedSpan{
		start:      r.start,
		end:        r.start + recordOverhead + r.payloadLen,
		payloadLen: r.payloadLen,
		kind:       kind,
	}
	delete(l.active, r)
	l.advanceVisibleLocked()
	l.broadcastLocked()
}

func (l *Log) advanceVisibleLocked() {
	for {
		span, ok := l.resolved[l.visibleEnd]
		if !ok {
			return
		}
		l.records = append(l.records, recordMeta{
			start:      span.start,
			payloadLen: span.payloadLen,
			kind:       span.kind,
		})
		l.visibleEnd = span.end
		delete(l.resolved, span.start)
		l.broadcastLocked()
	}
}

func (l *Log) broadcastLocked() {
	close(l.notify)
	l.notify = make(chan struct{})
}

type reservationState uint8

const (
	reservationActive reservationState = iota
	reservationSealing
	reservationCommitted
	reservationHole
)

// Reservation is a server-allocated span. Its write cursor starts at the
// first payload byte and only moves forward. There is no Seek and no writable
// offset parameter: a caller cannot move the position or overwrite an earlier
// record through this handle.
type Reservation struct {
	log *Log

	start      int64
	dataStart  int64
	payloadLen int64

	mu      sync.Mutex
	written int64
	crc     uint32
	state   reservationState
}

// Offset returns the record's server-assigned start offset, including framing.
func (r *Reservation) Offset() int64 { return r.start }

// DataOffset returns the server-assigned offset of the first payload byte.
func (r *Reservation) DataOffset() int64 { return r.dataStart }

// Length returns the reserved payload length.
func (r *Reservation) Length() int64 { return r.payloadLen }

// End returns the end offset of the reservation.
func (r *Reservation) End() int64 { return r.start + recordOverhead + r.payloadLen }

// Written returns the number of payload bytes written so far.
func (r *Reservation) Written() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.written
}

// Record returns the metadata of this reservation. Data is not populated.
func (r *Reservation) Record() Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	kind := KindData
	if r.state == reservationHole {
		kind = KindHole
	}
	return Record{
		Offset:     r.start,
		DataOffset: r.dataStart,
		Length:     r.payloadLen,
		Kind:       kind,
	}
}

// Write appends p to the reservation. The store may short-write; Write keeps
// advancing the server-side cursor until p is complete, an I/O error occurs,
// or the reservation would overflow. Overwriting or moving the cursor is not
// possible.
func (r *Reservation) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writeLocked(p)
}

func (r *Reservation) writeLocked(p []byte) (int, error) {
	if r.state != reservationActive {
		return 0, ErrResolved
	}
	if len(p) == 0 {
		return 0, nil
	}
	remaining := r.payloadLen - r.written
	if remaining <= 0 {
		return 0, ErrOverflow
	}
	truncated := int64(len(p)) > remaining
	if truncated {
		p = p[:remaining]
	}
	n, err := writeFullAt(r.log.store, p, r.dataStart+r.written)
	if n > 0 {
		r.written += int64(n)
		r.crc = crc32.Update(r.crc, crcTable, p[:n])
	}
	if err != nil {
		return n, err
	}
	if truncated {
		return n, ErrOverflow
	}
	return n, nil
}

// WriteContext writes p in bounded chunks and stops when ctx is cancelled.
// Bytes already written remain in the reservation; the caller decides whether
// to finish with Commit or seal with Cancel. Append performs that choice
// automatically.
func (r *Reservation) WriteContext(ctx context.Context, p []byte) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	total := 0
	for len(p) > 0 {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n := len(p)
		if n > ioChunk {
			n = ioChunk
		}
		w, err := r.Write(p[:n])
		total += w
		if err != nil {
			return total, err
		}
		p = p[w:]
	}
	return total, nil
}

// WriteSegments writes several segments in order. If a segment cannot be
// completed, it returns the number of bytes actually accepted and the fault;
// the caller can then finish the reservation or seal it with Cancel.
func (r *Reservation) WriteSegments(segments ...[]byte) (int, error) {
	total := 0
	for _, seg := range segments {
		n, err := r.Write(seg)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// Commit writes the data trailer, syncs the volume, and makes the record
// visible. The full payload must have been written. If commit cannot be made
// durable, the reservation is sealed as a hole instead.
func (r *Reservation) Commit() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch r.state {
	case reservationCommitted:
		return nil
	case reservationHole, reservationSealing:
		return ErrResolved
	}
	if r.written != r.payloadLen {
		return ErrIncomplete
	}

	tr := encodeTrailer(KindData, r.crc)
	if _, err := writeFullAt(r.log.store, tr, r.dataStart+r.payloadLen); err != nil {
		sealErr := r.sealHoleLocked()
		return errors.Join(fmt.Errorf("netsec: commit trailer: %w", err), sealErr)
	}
	if err := r.log.store.Sync(); err != nil {
		sealErr := r.sealHoleLocked()
		return errors.Join(fmt.Errorf("netsec: commit sync: %w", err), sealErr)
	}
	r.state = reservationCommitted
	r.log.resolve(r, KindData)
	return nil
}

// Cancel seals the entire reservation as an explicit hole and syncs it. The
// allocator never reuses the span, so later appends always start after the
// hole. Cancel is idempotent for an already resolved reservation.
func (r *Reservation) Cancel() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.state {
	case reservationHole, reservationCommitted:
		return nil
	default:
		return r.sealHoleLocked()
	}
}

func (r *Reservation) sealHoleLocked() error {
	if r.state == reservationHole {
		return nil
	}
	if r.state == reservationCommitted {
		return ErrResolved
	}
	r.state = reservationSealing
	if err := writeZeros(r.log.store, r.dataStart, r.payloadLen); err != nil {
		return fmt.Errorf("netsec: seal hole payload: %w", err)
	}
	tr := encodeTrailer(KindHole, 0)
	if _, err := writeFullAt(r.log.store, tr, r.dataStart+r.payloadLen); err != nil {
		return fmt.Errorf("netsec: seal hole trailer: %w", err)
	}
	if err := r.log.store.Sync(); err != nil {
		return fmt.Errorf("netsec: seal hole sync: %w", err)
	}
	r.state = reservationHole
	r.log.resolve(r, KindHole)
	return nil
}

func encodeHeader(start, payloadLen int64) []byte {
	b := make([]byte, headerSize)
	copy(b[0:8], recordMagic)
	b[8] = recordVersion
	binary.BigEndian.PutUint64(b[12:20], uint64(payloadLen))
	binary.BigEndian.PutUint64(b[20:28], uint64(start))
	binary.BigEndian.PutUint32(b[28:32], crc32.Checksum(b[:28], crcTable))
	return b
}

func decodeHeader(b []byte, expectedStart int64) (int64, bool) {
	if len(b) != headerSize ||
		string(b[0:8]) != recordMagic ||
		b[8] != recordVersion ||
		crc32.Checksum(b[:28], crcTable) != binary.BigEndian.Uint32(b[28:32]) {
		return 0, false
	}
	payload := binary.BigEndian.Uint64(b[12:20])
	start := binary.BigEndian.Uint64(b[20:28])
	if payload > math.MaxInt64 || start > math.MaxInt64 {
		return 0, false
	}
	payloadLen := int64(payload)
	if payloadLen < 0 || payloadLen > maxPayload || int64(start) != expectedStart {
		return 0, false
	}
	return payloadLen, true
}

func encodeTrailer(kind Kind, sum uint32) []byte {
	b := make([]byte, trailerSize)
	copy(b[0:8], trailerMagic)
	b[8] = byte(kind)
	b[9] = recordVersion
	binary.BigEndian.PutUint32(b[12:16], sum)
	return b
}

func decodeTrailer(b []byte) (Kind, uint32, bool) {
	if len(b) != trailerSize ||
		string(b[0:8]) != trailerMagic ||
		b[9] != recordVersion {
		return 0, 0, false
	}
	kind := Kind(b[8])
	if kind != KindData && kind != KindHole {
		return 0, 0, false
	}
	return kind, binary.BigEndian.Uint32(b[12:16]), true
}

func writeFullAt(w io.WriterAt, p []byte, off int64) (int, error) {
	total := 0
	for total < len(p) {
		n, err := w.WriteAt(p[total:], off+int64(total))
		if n < 0 || n > len(p)-total {
			return total, ErrShortWrite
		}
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, ErrShortWrite
		}
	}
	return total, nil
}

func readFullAt(r io.ReaderAt, p []byte, off int64) error {
	for len(p) > 0 {
		n, err := r.ReadAt(p, off)
		if n > 0 {
			p = p[n:]
			off += int64(n)
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(p) == 0 {
				return nil
			}
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}

func readExactAt(r io.ReaderAt, off int64, n int) ([]byte, bool, error) {
	b := make([]byte, n)
	got, err := r.ReadAt(b, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	if got != n {
		return b, false, nil
	}
	return b, true, nil
}

func writeZeros(w io.WriterAt, off, n int64) error {
	if n <= 0 {
		return nil
	}
	size := int64(ioChunk)
	if n < size {
		size = n
	}
	buf := make([]byte, size)
	for n > 0 {
		take := int64(len(buf))
		if n < take {
			take = n
		}
		if _, err := writeFullAt(w, buf[:take], off); err != nil {
			return err
		}
		off += take
		n -= take
	}
	return nil
}

func recoverStore(s Store) ([]recordMeta, int64, error) {
	size, err := s.Size()
	if err != nil {
		return nil, 0, err
	}
	var records []recordMeta
	off := int64(0)

	for off < size {
		hdr, full, err := readExactAt(s, off, headerSize)
		if err != nil {
			return nil, 0, err
		}
		if !full {
			meta, end, err := recoverUnknownTail(s, off, size)
			if err != nil {
				return nil, 0, err
			}
			records = append(records, meta)
			off = end
			continue
		}

		payloadLen, ok := decodeHeader(hdr, off)
		if !ok {
			meta, end, err := recoverUnknownTail(s, off, size)
			if err != nil {
				return nil, 0, err
			}
			records = append(records, meta)
			off = end
			continue
		}

		meta, end, complete, err := recoverRecord(s, off, payloadLen, size)
		if err != nil {
			return nil, 0, err
		}
		records = append(records, meta)
		off = end
		if !complete {
			break
		}
	}
	return records, off, nil
}

func recoverRecord(s Store, off, payloadLen, size int64) (recordMeta, int64, bool, error) {
	meta := recordMeta{start: off, payloadLen: payloadLen, kind: KindHole}
	dataStart := off + headerSize
	trailerOff := dataStart + payloadLen
	end := trailerOff + trailerSize

	if size < end {
		// The record's header is valid, so the span belongs to one of our
		// reservations. Zeroing and sealing it cannot touch any committed
		// byte; it only covers the unfinished reservation.
		if err := writeHoleSpan(s, off, payloadLen); err != nil {
			return meta, 0, false, err
		}
		return meta, end, true, nil
	}

	tr, full, err := readExactAt(s, trailerOff, trailerSize)
	if err != nil {
		return meta, 0, false, err
	}
	if full {
		kind, sum, ok := decodeTrailer(tr)
		if ok && kind == KindData {
			valid, err := verifyPayloadCRC(s, dataStart, payloadLen, sum)
			if err != nil {
				return meta, 0, false, err
			}
			if valid {
				meta.kind = KindData
				return meta, end, true, nil
			}
		}
		if ok && kind == KindHole {
			return meta, end, true, nil
		}
	}
	// The declared span is complete but its trailer is corrupt. Preserve
	// the bytes rather than rewriting data that may have been committed;
	// the record is exposed as a hole and scanning resumes at its end.
	return meta, end, true, nil
}

func recoverUnknownTail(s Store, off, size int64) (recordMeta, int64, error) {
	from := off + recordOverhead
	if from < off {
		from = size
	}
	next, found, err := findNextHeader(s, from, size)
	if err != nil {
		return recordMeta{}, 0, err
	}
	end := size
	if found {
		end = next
	}
	if end < off+recordOverhead {
		end = off + recordOverhead
	}
	return recordMeta{
		start:      off,
		payloadLen: end - off - recordOverhead,
		kind:       KindHole,
	}, end, nil
}

func writeHoleSpan(s Store, off, payloadLen int64) error {
	if payloadLen < 0 || payloadLen > maxPayload {
		return ErrCorrupt
	}
	hdr := encodeHeader(off, payloadLen)
	if _, err := writeFullAt(s, hdr, off); err != nil {
		return err
	}
	if err := writeZeros(s, off+headerSize, payloadLen); err != nil {
		return err
	}
	tr := encodeTrailer(KindHole, 0)
	if _, err := writeFullAt(s, tr, off+headerSize+payloadLen); err != nil {
		return err
	}
	return s.Sync()
}

func findNextHeader(s Store, from, size int64) (int64, bool, error) {
	if from < 0 {
		from = 0
	}
	if from+headerSize > size {
		return 0, false, nil
	}
	const scanChunk = 64 << 10
	buf := make([]byte, scanChunk)
	magic := []byte(recordMagic)
	for base := from; base+headerSize <= size; {
		n := size - base
		if n > scanChunk {
			n = scanChunk
		}
		if err := readFullAt(s, buf[:n], base); err != nil {
			return 0, false, err
		}
		for i := 0; i+headerSize <= int(n); i++ {
			if !bytes.Equal(buf[i:i+8], magic) {
				continue
			}
			candidate := base + int64(i)
			if _, ok := decodeHeader(buf[i:i+headerSize], candidate); ok {
				return candidate, true, nil
			}
		}
		if n < scanChunk {
			break
		}
		base += n - headerSize + 1
	}
	return 0, false, nil
}

func verifyPayloadCRC(s Store, off, n int64, want uint32) (bool, error) {
	sum := crc32.Update(0, crcTable, []byte{byte(KindData)})
	buf := make([]byte, ioChunk)
	for n > 0 {
		take := int64(len(buf))
		if n < take {
			take = n
		}
		if err := readFullAt(s, buf[:take], off); err != nil {
			return false, err
		}
		sum = crc32.Update(sum, crcTable, buf[:take])
		off += take
		n -= take
	}
	return sum == want, nil
}
