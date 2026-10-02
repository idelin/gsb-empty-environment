package wal

import (
	"io"
	"os"
	"sort"
	"sync"
)

// WAL is a write-ahead log backed by a single file. Appends are buffered
// in memory (the "log buffer"); Flush forces the buffered tail to disk.
// Anything still buffered when the process "crashes" is lost, exactly like
// a real database. If an Injector wraps the file, hostile conditions such
// as disk-full, torn writes and power loss are emulated on Flush.
type WAL struct {
	mu         sync.Mutex
	path       string
	file       *os.File
	sink       io.Writer // file, optionally wrapped by an Injector
	nextLSN    uint64
	pending    []byte // encoded records not yet flushed
	flushedLSN uint64
	index      map[uint64]*Record // every record known to this session
}

// Open opens (or creates) the WAL file at path. Existing contents are
// scanned; a corrupt or torn tail is truncated so the log self-heals, and
// the LSN counter continues after the last durable record.
func Open(path string, inj *Injector) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	recs, goodBytes, err := ScanFile(path)
	if err != nil {
		f.Close()
		return nil, err
	}
	if fi, statErr := f.Stat(); statErr == nil && fi.Size() > goodBytes {
		// Torn/corrupt tail from a crash: truncate to the last good frame.
		if truncErr := f.Truncate(goodBytes); truncErr != nil {
			f.Close()
			return nil, truncErr
		}
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	var sink io.Writer = f
	if inj != nil {
		sink = inj.Wrap(f)
	}
	w := &WAL{
		path:  path,
		file:  f,
		sink:  sink,
		index: make(map[uint64]*Record),
	}
	w.nextLSN = 1
	for _, r := range recs {
		if r.LSN >= w.nextLSN {
			w.nextLSN = r.LSN + 1
		}
		w.index[r.LSN] = r
	}
	w.flushedLSN = w.nextLSN - 1
	return w, nil
}

// Append buffers a record in the log buffer and assigns its LSN. It never
// touches disk; durability failures surface at Flush / AppendAndFlush.
func (w *WAL) Append(rec *Record) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec.LSN = w.nextLSN
	w.nextLSN++
	w.pending = append(w.pending, rec.Encode()...)
	w.index[rec.LSN] = rec
	return rec.LSN
}

// AppendAndFlush atomically appends a single record and forces the whole
// log buffer to disk (group commit). If the write fails, the record is
// rolled back out of the buffer so a failed COMMIT never becomes durable;
// previously buffered records of other transactions stay buffered.
func (w *WAL) AppendAndFlush(rec *Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec.LSN = w.nextLSN
	enc := rec.Encode()
	w.pending = append(w.pending, enc...)
	if _, err := w.sink.Write(w.pending); err != nil {
		w.pending = w.pending[:len(w.pending)-len(enc)]
		return err
	}
	w.pending = w.pending[:0]
	w.nextLSN++
	w.index[rec.LSN] = rec
	w.flushedLSN = w.nextLSN - 1
	return w.file.Sync()
}

// WriteRaw appends an already-LSN-assigned record received via log
// shipping, preserving its original LSN (replica-side apply).
func (w *WAL) WriteRaw(rec *Record) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, rec.Encode()...)
	w.index[rec.LSN] = rec
	if rec.LSN >= w.nextLSN {
		w.nextLSN = rec.LSN + 1
	}
}

// Flush forces the buffered log tail to disk. On a partial (torn) write
// the unwritten remainder stays buffered so a later Flush can complete it.
func (w *WAL) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return nil
	}
	n, err := w.sink.Write(w.pending)
	if n > 0 {
		w.pending = w.pending[n:]
	}
	if err != nil {
		return err
	}
	w.pending = w.pending[:0]
	w.flushedLSN = w.nextLSN - 1
	return w.file.Sync()
}

// FlushedLSN returns the highest LSN known to be durable.
func (w *WAL) FlushedLSN() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushedLSN
}

// Record returns the record with the given LSN, or nil.
func (w *WAL) Record(lsn uint64) *Record {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.index[lsn]
}

// Records returns all records known to this session, ordered by LSN.
func (w *WAL) Records() []*Record {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*Record, 0, len(w.index))
	for _, r := range w.index {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LSN < out[j].LSN })
	return out
}

// Close closes the underlying file without flushing (callers flush
// explicitly; unflushed buffers are what a crash discards).
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

// ScanFile decodes every well-formed record in the file and reports the
// offset of the first byte that does not decode (the durable prefix).
// A corrupt or truncated tail is not an error: it is what a crash leaves
// behind, and callers use the returned offset to truncate it.
func ScanFile(path string) ([]*Record, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	var recs []*Record
	off := 0
	for off < len(data) {
		rec, n, decErr := DecodeFrame(data[off:])
		if decErr != nil {
			break
		}
		recs = append(recs, rec)
		off += n
	}
	return recs, int64(off), nil
}
