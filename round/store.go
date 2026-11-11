package round

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// EventStore is the durable append-only event log used by a Round.
// Implementations must make Append durable before returning and return the
// complete event sequence from Read in append order.
type EventStore interface {
	Append(ctx context.Context, event *Event) error
	Read(ctx context.Context) ([]Event, error)
}

// FileStore persists events as one framed JSON object per line inside a
// single file. Each line is "<hex sha256(payload)> <json payload>"; the
// checksum is verified on replay. Every append is followed by an fsync, so a
// power cut can leave at most one torn final line, which Replay discards.
type FileStore struct {
	path string
	mu   sync.Mutex

	// preSync, when non-nil, runs after bytes are written but before the
	// durability syscall. It exists for power-loss tests and is unexported;
	// production callers never set it. Returning errSimulatedPowerLoss
	// truncates the file back to its pre-write size, modeling a power cut that
	// loses every byte which had not reached durable storage.
	preSync func(event *Event) error
}

// OpenFileStore opens (creating it if needed) the event file at path. Parent
// directories are created with 0o755 and the event file with 0o600.
func OpenFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty store path", ErrInvalidConfig)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("round: create store directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("round: open event store: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("round: close event store handle: %w", err)
	}
	return &FileStore{path: path}, nil
}

// Path returns the on-disk location of the event file.
func (s *FileStore) Path() string { return s.path }

// Append writes one checksummed line and fsyncs it before returning.
func (s *FileStore) Append(ctx context.Context, event *Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("round: encode event: %w", err)
	}
	sum := sha256.Sum256(payload)
	line := make([]byte, 0, hex.EncodedLen(sha256.Size)+1+len(payload)+1)
	line = append(line, hex.EncodeToString(sum[:])...)
	line = append(line, ' ')
	line = append(line, payload...)
	line = append(line, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("round: open event store for append: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()

	durableSize, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("round: determine durable size: %w", err)
	}
	if _, err := file.Write(line); err != nil {
		return fmt.Errorf("round: write event: %w", err)
	}
	if s.preSync != nil {
		if err := s.preSync(event); err != nil {
			if errors.Is(err, errSimulatedPowerLoss) {
				if truncErr := file.Truncate(durableSize); truncErr != nil {
					return fmt.Errorf("round: simulate power loss: %w", truncErr)
				}
				if syncErr := file.Sync(); syncErr != nil {
					return fmt.Errorf("round: simulate power loss: %w", syncErr)
				}
				closed = true
				_ = file.Close()
			}
			return err
		}
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("round: sync event: %w", err)
	}
	if err := file.Close(); err != nil {
		closed = true
		return fmt.Errorf("round: close event store: %w", err)
	}
	closed = true
	return nil
}

// Read verifies every stored line and returns the events in order. A torn or
// truncated final line (partial write from a power loss) is ignored; any
// checksum or framing failure elsewhere returns ErrCorruptLog.
func (s *FileStore) Read(ctx context.Context) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("round: open event store for read: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Events include secrets; allow generously sized lines.
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)

	var lines [][]byte
	for scanner.Scan() {
		lines = append(lines, append([]byte(nil), scanner.Bytes()...))
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("%w: read event store: %v", ErrCorruptLog, err)
	}

	events := make([]Event, 0, len(lines))
	for i, line := range lines {
		event, err := decodeEventLine(line)
		if err != nil {
			// Only the physical final line may be a torn tail write.
			if i == len(lines)-1 {
				break
			}
			return nil, fmt.Errorf("%w: line %d: %v", ErrCorruptLog, i+1, err)
		}
		events = append(events, event)
	}
	return events, nil
}

func decodeEventLine(line []byte) (Event, error) {
	var event Event
	space := -1
	for i, b := range line {
		if b == ' ' {
			space = i
			break
		}
	}
	if space != hex.EncodedLen(sha256.Size) {
		return event, errors.New("malformed frame")
	}
	payload := line[space+1:]
	gotSum := sha256.Sum256(payload)
	if hex.EncodeToString(gotSum[:]) != string(line[:space]) {
		return event, errors.New("checksum mismatch")
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return event, fmt.Errorf("bad payload: %w", err)
	}
	if event.Seq <= 0 || event.Kind == "" {
		return event, errors.New("incomplete event")
	}
	return event, nil
}
