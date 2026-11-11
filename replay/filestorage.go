package replay

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
)

// FileConfig configures a FileStorage.
type FileConfig struct {
	// Path is the append-only log file. It is created if missing.
	Path string

	// FileMode is used when creating the log. Zero defaults to 0600.
	FileMode os.FileMode

	// Hook optionally injects power-loss faults at append points. Ordinal is
	// counted per storage handle (one process lifetime).
	Hook FaultHook
}

// FileStorage is a durable, cross-process Storage backed by an append-only
// file. Coordination across processes uses an advisory exclusive lock
// (flock); each record is framed with a CRC-32 and followed by an fsync, so a
// torn tail caused by simulated (or real) power loss is truncated on the next
// Lock instead of poisoning the log.
type FileStorage struct {
	path string
	mode os.FileMode
	hook FaultHook

	mu      sync.Mutex
	file    *os.File
	locked  bool
	closed  bool
	ordinal int // durable append count, carried across reopened handles
}

var magicHeader = []byte("REPLAYLOG1\n")

const frameHeaderSize = 9 // uint32 payload length + uint32 CRC + uint8 kind

// OpenFileStorage opens (or creates) the log file. The lock itself is taken by
// Lock, matching the Storage contract.
func OpenFileStorage(cfg FileConfig) (*FileStorage, error) {
	if cfg.Path == "" {
		return nil, errors.New("replay: FileConfig.Path is required")
	}
	mode := cfg.FileMode
	if mode == 0 {
		mode = 0600
	}
	file, err := os.OpenFile(cfg.Path, os.O_RDWR|os.O_CREATE, mode)
	if err != nil {
		return nil, fmt.Errorf("replay: open log: %w", err)
	}
	store := &FileStorage{path: cfg.Path, mode: mode, hook: cfg.Hook, file: file}
	if err := store.ensureHeader(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return store, nil
}

// Lock acquires an exclusive flock and repairs any torn tail left by a crash.
func (s *FileStorage) Lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.locked {
		return errAlreadyLocked
	}
	if err := syscall.Flock(int(s.file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("replay: flock: %w", err)
	}
	s.locked = true
	// ensureHeader leaves the offset at end-of-header; frame parsing always
	// starts from the beginning of the file.
	if _, err := s.file.Seek(int64(len(magicHeader)), io.SeekStart); err != nil {
		_ = syscall.Flock(int(s.file.Fd()), syscall.LOCK_UN)
		s.locked = false
		return fmt.Errorf("replay: seek before repair: %w", err)
	}
	records, err := s.repairTailLocked()
	if err != nil {
		_ = syscall.Flock(int(s.file.Fd()), syscall.LOCK_UN)
		s.locked = false
		return err
	}
	// Continue durable append numbering from the repaired log so fault
	// injection can target a specific write point across process restarts.
	s.ordinal = len(records)
	if _, err := s.file.Seek(0, io.SeekEnd); err != nil {
		_ = syscall.Flock(int(s.file.Fd()), syscall.LOCK_UN)
		s.locked = false
		return fmt.Errorf("replay: seek end after lock: %w", err)
	}
	return nil
}

// Unlock releases the flock.
func (s *FileStorage) Unlock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.locked {
		return nil
	}
	if err := syscall.Flock(int(s.file.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("replay: unlock: %w", err)
	}
	s.locked = false
	return nil
}

// ReadAll returns all valid records from the log. The caller must hold Lock.
func (s *FileStorage) ReadAll(ctx context.Context) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if !s.locked {
		return nil, errors.New("replay: ReadAll without Lock")
	}
	if _, err := s.file.Seek(int64(len(magicHeader)), io.SeekStart); err != nil {
		return nil, fmt.Errorf("replay: seek: %w", err)
	}
	records, _, err := readFrames(s.file)
	if err != nil {
		return nil, err
	}
	return records, nil
}

// Append frames, writes and fsyncs one record. The caller must hold Lock.
func (s *FileStorage) Append(ctx context.Context, rec Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := encodeEnvelope(rec)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if !s.locked {
		return errors.New("replay: Append without Lock")
	}
	s.ordinal++
	ordinal := s.ordinal

	if s.hook != nil {
		if fault, fire := s.hook(ordinal, rec); fire {
			switch fault {
			case FaultBeforeWrite:
				return ErrPowerLoss
			case FaultAfterWrite:
				if err := s.writeFrameLocked(rec.Kind, payload); err != nil {
					return err
				}
				return ErrPowerLoss
			case FaultTornWrite:
				return s.writeTornLocked(rec.Kind, payload)
			}
		}
	}
	return s.writeFrameLocked(rec.Kind, payload)
}

// Close unlocks and closes the backing file.
func (s *FileStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.locked {
		_ = syscall.Flock(int(s.file.Fd()), syscall.LOCK_UN)
		s.locked = false
	}
	return s.file.Close()
}

// appendTorn is used by FaultyStorage's FaultTornWrite mode.
func (s *FileStorage) appendTorn(ctx context.Context, rec Record) error {
	payload, err := encodeEnvelope(rec)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return s.writeTornLocked(rec.Kind, payload)
}

type envelope struct {
	Kind      RecordKind       `json:"kind"`
	Entry     *EntryRecord     `json:"entry,omitempty"`
	Watermark *WatermarkRecord `json:"watermark,omitempty"`
}

func encodeEnvelope(rec Record) ([]byte, error) {
	env := envelope{Kind: rec.Kind, Entry: rec.Entry, Watermark: rec.Watermark}
	payload, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("replay: encode record: %w", err)
	}
	return payload, nil
}

func decodeEnvelope(kind RecordKind, payload []byte) (Record, error) {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return Record{}, fmt.Errorf("replay: decode record: %w", err)
	}
	if env.Kind != kind {
		return Record{}, errors.New("replay: frame kind mismatch")
	}
	return Record{Kind: env.Kind, Entry: env.Entry, Watermark: env.Watermark}, nil
}

func (s *FileStorage) ensureHeader() error {
	buf := make([]byte, len(magicHeader))
	_, err := io.ReadFull(s.file, buf)
	if err == nil && bytes.Equal(buf, magicHeader) {
		return nil
	}
	if err == nil {
		return errors.New("replay: log file has an unrecognized header")
	}
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("replay: read header: %w", err)
	}
	// New file or an empty/short file: (re)initialize from the beginning.
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("replay: seek header: %w", err)
	}
	if _, err := s.file.Write(magicHeader); err != nil {
		return fmt.Errorf("replay: write header: %w", err)
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("replay: fsync header: %w", err)
	}
	return nil
}

// repairTailLocked truncates any bytes after the last complete, checksum-valid
// frame. This restores a consistent log after a partial write and returns the
// surviving records.
func (s *FileStorage) repairTailLocked() ([]Record, error) {
	if _, err := s.file.Seek(int64(len(magicHeader)), io.SeekStart); err != nil {
		return nil, fmt.Errorf("replay: seek: %w", err)
	}
	records, validEnd, err := readFrames(s.file)
	if err != nil {
		return nil, err
	}
	info, err := s.file.Stat()
	if err != nil {
		return nil, fmt.Errorf("replay: stat log: %w", err)
	}
	if info.Size() != validEnd {
		if err := s.file.Truncate(validEnd); err != nil {
			return nil, fmt.Errorf("replay: truncate torn tail: %w", err)
		}
		if err := s.file.Sync(); err != nil {
			return nil, fmt.Errorf("replay: fsync repair: %w", err)
		}
	}
	return records, nil
}

func (s *FileStorage) writeFrameLocked(kind RecordKind, payload []byte) error {
	frame, err := makeFrame(kind, payload)
	if err != nil {
		return err
	}
	if _, err := s.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("replay: seek end: %w", err)
	}
	if _, err := s.file.Write(frame); err != nil {
		return fmt.Errorf("replay: write frame: %w", err)
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("replay: fsync frame: %w", err)
	}
	return nil
}

// writeTornLocked writes only a prefix of the frame and fsyncs it, then
// reports simulated power loss. The damaged tail is removed by the next Lock.
func (s *FileStorage) writeTornLocked(kind RecordKind, payload []byte) error {
	frame, err := makeFrame(kind, payload)
	if err != nil {
		return err
	}
	prefix := len(frame) / 2
	if prefix == 0 {
		prefix = 1
	}
	if _, err := s.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("replay: seek end: %w", err)
	}
	if _, err := s.file.Write(frame[:prefix]); err != nil {
		return fmt.Errorf("replay: write torn frame: %w", err)
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("replay: fsync torn frame: %w", err)
	}
	return ErrPowerLoss
}

func makeFrame(kind RecordKind, payload []byte) ([]byte, error) {
	crc := crc32Checksum(payload)
	frame := make([]byte, 0, frameHeaderSize+len(payload))
	var lenbuf [4]byte
	binary.BigEndian.PutUint32(lenbuf[:], uint32(len(payload)))
	frame = append(frame, lenbuf[:]...)
	var crcbuf [4]byte
	binary.BigEndian.PutUint32(crcbuf[:], crc)
	frame = append(frame, crcbuf[:]...)
	frame = append(frame, byte(kind))
	frame = append(frame, payload...)
	return frame, nil
}

// readFrames parses frames from r (positioned after the header) and returns the
// decoded records plus the byte offset immediately after the last valid frame.
// A short or corrupt tail is treated as a torn write: parsing stops and its
// bytes are excluded from validEnd.
func readFrames(r io.Reader) ([]Record, int64, error) {
	offset := int64(len(magicHeader))
	validEnd := offset
	var records []Record

	for {
		header := make([]byte, frameHeaderSize)
		n, err := io.ReadFull(r, header)
		if errors.Is(err, io.EOF) {
			return records, validEnd, nil
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return records, validEnd, nil // torn header
		}
		if err != nil {
			return nil, 0, fmt.Errorf("replay: read frame header: %w", err)
		}
		payloadLen := binary.BigEndian.Uint32(header[:4])
		crc := binary.BigEndian.Uint32(header[4:8])
		kind := RecordKind(header[8])

		// A corrupt length cannot be trusted, so any remaining bytes are a
		// torn tail.
		if payloadLen > 64*1024*1024 {
			return records, validEnd, nil
		}
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			return records, validEnd, nil // torn payload
		}
		offset += int64(n) + int64(payloadLen)

		if crc32Checksum(payload) != crc {
			return records, validEnd, nil // corrupt tail
		}
		rec, err := decodeEnvelope(kind, payload)
		if err != nil {
			return records, validEnd, nil
		}
		records = append(records, rec)
		validEnd = offset
	}
}
