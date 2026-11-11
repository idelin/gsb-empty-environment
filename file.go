package netsec

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// FileSink is a durable, bounded write-ahead audit log backed by one file.
// Every successful Append is followed by an fsync, so durability is confirmed
// before the Committer is allowed to run a side effect.
type FileSink struct {
	path     string
	file     *os.File
	maxBytes int
	faults   Faults

	mu      sync.Mutex // protects file handle, nextSeq and closed
	faultMu sync.Mutex // serializes fault callbacks; never held while mu is held
	closed  bool
	nextSeq uint64
}

// NewFileSink opens (creating if needed) the WAL at path and replays it.
// maxBytes bounds the on-disk log: once an Append would exceed it, the log is
// compacted to the newest record per OpID (pending records always retained);
// an Append that still does not fit returns ErrSinkFull, which is the
// backpressure signal.
func NewFileSink(path string, maxBytes int, faults Faults) (*FileSink, error) {
	if maxBytes <= 0 {
		return nil, errors.New("maxBytes must be positive")
	}

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}

	sink := &FileSink{
		path:     path,
		file:     file,
		maxBytes: maxBytes,
		faults:   faults,
		nextSeq:  1,
	}
	if err := sink.load(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return sink, nil
}

func (s *FileSink) load() error {
	data, err := io.ReadAll(s.file)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		// New file: write the magic header so a real crash cannot leave a
		// zero-length file that looks valid when it was never initialised.
		return s.writeHeader()
	}
	if len(data) < headerSize || bigEndianUint32(data) != logMagic {
		return ErrCorrupt
	}

	body := data[headerSize:]
	records, decodeErr := decodeFrames(body)

	// Re-seal at the end of the valid prefix. A torn tail from a crash
	// mid-write is truncated away; a checksum failure on a fully framed record
	// is genuine corruption and must not be silently discarded.
	validLen := headerSize
	for _, rec := range records {
		validLen += frameEncodedLen(rec)
		if rec.Seq >= s.nextSeq {
			s.nextSeq = rec.Seq + 1
		}
	}
	if validLen != len(data) {
		if err := s.file.Truncate(int64(validLen)); err != nil {
			return err
		}
	}
	if err := s.file.Sync(); err != nil {
		return err
	}

	if decodeErr != nil && validLen == headerSize && len(body) > 0 {
		return decodeErr
	}
	if decodeErr != nil {
		// The bytes beyond the valid prefix must be one incomplete tail frame.
		// A fully framed but checksum-bad record is real corruption, not a
		// torn write, and truncating it would destroy an acknowledged event.
		tail := body[validLen-headerSize:]
		if len(tail) < framePrefix {
			return nil // partial length header: expected torn tail
		}
		n := int(bigEndianUint32(tail))
		if n <= 0 || framePrefix+n+crcSize <= len(tail) {
			return decodeErr
		}
	}
	return nil
}

func (s *FileSink) writeHeader() error {
	var header [headerSize]byte
	putBigEndianUint32(header[:], logMagic)
	// reserved 4 bytes stay zero.
	if _, err := s.file.Write(header[:]); err != nil {
		return err
	}
	return s.file.Sync()
}

// Append implements Sink. The write and its fsync complete before a nil error
// is returned; that nil error is the gate for running the side effect.
func (s *FileSink) Append(ctx context.Context, rec Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	locked := true
	unlock := func() {
		if locked {
			s.mu.Unlock()
			locked = false
		}
	}
	defer unlock()

	if s.closed {
		return ErrClosed
	}
	if s.faults.BeforeAppend != nil {
		before := s.faults.BeforeAppend
		unlock()
		s.faultMu.Lock()
		err := before(rec)
		s.faultMu.Unlock()
		if err != nil {
			return err
		}

		s.mu.Lock()
		locked = true
		if s.closed {
			return ErrClosed
		}
	}

	frame := encodeFrame(rec)
	info, err := s.file.Stat()
	if err != nil {
		return err
	}
	if int(info.Size())+len(frame) > s.maxBytes {
		if err := s.compact(ctx); err != nil {
			return err
		}
		info, err = s.file.Stat()
		if err != nil {
			return err
		}
		if int(info.Size())+len(frame) > s.maxBytes {
			return ErrSinkFull
		}
	}

	if _, err := s.file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if _, err := s.file.Write(frame); err != nil {
		return err
	}
	// fsync is mandatory: without it a power loss could erase the reservation
	// while the side effect still runs.
	if err := s.file.Sync(); err != nil {
		return err
	}

	if s.faults.AfterAppend != nil {
		after := s.faults.AfterAppend
		unlock()
		s.faultMu.Lock()
		err := after(rec)
		s.faultMu.Unlock()
		return err
	}
	return nil
}

// Replay implements Sink.
func (s *FileSink) Replay(ctx context.Context) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(s.file)
	if err != nil {
		return nil, err
	}
	if len(data) < headerSize {
		return nil, ErrCorrupt
	}
	return decodeFrames(data[headerSize:])
}

// Compact implements Sink.
func (s *FileSink) Compact(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compact(ctx)
}

// compact performs the rewrite; the caller must hold s.mu (or be the single
// holder during Append) and must not reacquire s.mu on the way.
func (s *FileSink) compact(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(s.file)
	if err != nil {
		return err
	}
	records, err := decodeFrames(data[headerSize:])
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}

	keep := make(map[string]Record, len(records))
	for _, rec := range records {
		keep[rec.OpID] = rec
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".audit-compact-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	var header [headerSize]byte
	putBigEndianUint32(header[:], logMagic)
	if _, err := tmp.Write(header[:]); err != nil {
		_ = tmp.Close()
		return err
	}
	for _, rec := range records {
		if latest, ok := keep[rec.OpID]; ok && latest.Seq == rec.Seq {
			if _, err := tmp.Write(encodeFrame(rec)); err != nil {
				_ = tmp.Close()
				return err
			}
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}

	if err := s.file.Close(); err != nil {
		return err
	}
	file, err := os.OpenFile(s.path, os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	s.file = file
	return nil
}

// NextSeq returns the sequence number the next appended record will use.
func (s *FileSink) NextSeq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextSeq
}

// Close implements Sink.
func (s *FileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.closed = true
	if err := s.file.Sync(); err != nil {
		_ = s.file.Close()
		return err
	}
	return s.file.Close()
}

func putBigEndianUint32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}
