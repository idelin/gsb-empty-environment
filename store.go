package netsec

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Record is the durable evidence of a release authorization. Implementations
// must persist it atomically before Process returns.
type Record struct {
	MessageKey       Hash      `json:"message_key"`
	SourceChain      ChainID   `json:"source_chain"`
	DestinationChain ChainID   `json:"destination_chain"`
	BridgeID         string    `json:"bridge_id,omitempty"`
	BlockHeight      uint64    `json:"block_height"`
	BlockHash        Hash      `json:"block_hash"`
	TxHash           Hash      `json:"tx_hash"`
	TxIndex          uint32    `json:"tx_index"`
	EventIndex       uint32    `json:"event_index"`
	Kind             EventKind `json:"kind"`
	Sender           []byte    `json:"sender"`
	AssetID          string    `json:"asset_id"`
	Amount           uint64    `json:"amount"`
	Recipient        []byte    `json:"recipient"`
	Nonce            uint64    `json:"nonce"`
	EventHash        Hash      `json:"event_hash"`
	ProcessedAt      time.Time `json:"processed_at"`
}

// AlreadyProcessedError is returned when the unique message key is already
// present in the replay store.
type AlreadyProcessedError struct {
	Key      Hash
	Existing Record
}

func (e *AlreadyProcessedError) Error() string {
	return fmt.Sprintf("%v: message %s", ErrAlreadyProcessed, e.Key)
}

// Unwrap lets callers use errors.Is(err, ErrAlreadyProcessed).
func (e *AlreadyProcessedError) Unwrap() error {
	return ErrAlreadyProcessed
}

// Store is the idempotency boundary. Consume must atomically insert a key and
// return ErrAlreadyProcessed (ideally as *AlreadyProcessedError) for a
// duplicate. A production implementation should perform the target-chain
// ledger/release update in the same database transaction as the key insert;
// the record contains every field needed to do so.
type Store interface {
	Lookup(key Hash) (Record, bool, error)
	Consume(record Record) error
}

// MemoryStore is a concurrency-safe in-process replay store. It is suitable
// for tests and for deployments that provide their own durable Store.
type MemoryStore struct {
	mu      sync.RWMutex
	records map[Hash]Record
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: make(map[Hash]Record)}
}

// Lookup returns a copy of the record for key.
func (s *MemoryStore) Lookup(key Hash) (Record, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[key]
	if !ok {
		return Record{}, false, nil
	}
	return cloneRecord(record), true, nil
}

// Consume atomically inserts record.
func (s *MemoryStore) Consume(record Record) error {
	if err := validateRecord(record); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = make(map[Hash]Record)
	}
	if existing, ok := s.records[record.MessageKey]; ok {
		return &AlreadyProcessedError{Key: record.MessageKey, Existing: cloneRecord(existing)}
	}
	s.records[record.MessageKey] = cloneRecord(record)
	return nil
}

// Len returns the number of consumed messages.
func (s *MemoryStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

// FileStore is a single-process, append-only JSON-lines replay store. Every
// consume is fsynced before success is returned. A torn final line is
// discarded on open; malformed complete lines are treated as corruption.
type FileStore struct {
	mu      sync.RWMutex
	path    string
	file    *os.File
	records map[Hash]Record
	closed  bool
}

// OpenFileStore opens or creates a replay store at path.
func OpenFileStore(path string) (*FileStore, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("netsec: open replay store: %w", err)
	}
	store := &FileStore{
		path:    path,
		file:    file,
		records: make(map[Hash]Record),
	}
	if err := store.load(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return store, nil
}

func (s *FileStore) load() error {
	raw, err := io.ReadAll(s.file)
	if err != nil {
		return fmt.Errorf("netsec: read replay store: %w", err)
	}
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		lastNewline := strings.LastIndexByte(string(raw), '\n')
		cut := len(raw)
		if lastNewline >= 0 {
			cut = lastNewline + 1
		} else {
			cut = 0
		}
		if err := s.file.Truncate(int64(cut)); err != nil {
			return fmt.Errorf("netsec: truncate incomplete replay record: %w", err)
		}
		raw = raw[:cut]
	}

	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return fmt.Errorf("%w: line %d: %v", ErrCorruptStore, lineNumber, err)
		}
		if err := validateRecord(record); err != nil {
			return fmt.Errorf("%w: line %d: %v", ErrCorruptStore, lineNumber, err)
		}
		if _, exists := s.records[record.MessageKey]; exists {
			return fmt.Errorf("%w: duplicate key %s on line %d", ErrCorruptStore, record.MessageKey, lineNumber)
		}
		s.records[record.MessageKey] = cloneRecord(record)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("netsec: scan replay store: %w", err)
	}
	if _, err := s.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("netsec: seek replay store: %w", err)
	}
	return nil
}

// Lookup returns a copy of the stored record.
func (s *FileStore) Lookup(key Hash) (Record, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return Record{}, false, ErrStoreClosed
	}
	record, ok := s.records[key]
	if !ok {
		return Record{}, false, nil
	}
	return cloneRecord(record), true, nil
}

// Consume atomically appends and fsyncs one record.
func (s *FileStore) Consume(record Record) error {
	if err := validateRecord(record); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	if existing, ok := s.records[record.MessageKey]; ok {
		return &AlreadyProcessedError{Key: record.MessageKey, Existing: cloneRecord(existing)}
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("netsec: encode replay record: %w", err)
	}
	line := append(encoded, '\n')
	offset, err := s.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("netsec: seek replay store: %w", err)
	}
	written, err := s.file.Write(line)
	if err == nil && written != len(line) {
		err = io.ErrShortWrite
	}
	if err != nil {
		_ = s.file.Truncate(offset)
		_, _ = s.file.Seek(offset, io.SeekStart)
		return fmt.Errorf("netsec: append replay record: %w", err)
	}
	if err := s.file.Sync(); err != nil {
		_ = s.file.Truncate(offset)
		_, _ = s.file.Seek(offset, io.SeekStart)
		return fmt.Errorf("netsec: fsync replay store: %w", err)
	}
	s.records[record.MessageKey] = cloneRecord(record)
	return nil
}

// Close closes the underlying file. It is safe to call more than once.
func (s *FileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.file.Close()
}

// Path returns the backing file path.
func (s *FileStore) Path() string {
	return s.path
}

// Len returns the number of consumed messages.
func (s *FileStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

func validateRecord(record Record) error {
	var problems []string
	if record.MessageKey.IsZero() {
		problems = append(problems, "zero message key")
	}
	if err := record.SourceChain.Validate(); err != nil {
		problems = append(problems, "source chain: "+err.Error())
	}
	if err := record.DestinationChain.Validate(); err != nil {
		problems = append(problems, "destination chain: "+err.Error())
	}
	if record.SourceChain == record.DestinationChain {
		problems = append(problems, "source equals destination")
	}
	if len(record.BridgeID) > maxBridgeIDLength {
		problems = append(problems, "bridge id too long")
	}
	if record.BlockHash.IsZero() {
		problems = append(problems, "zero block hash")
	}
	if record.TxHash.IsZero() {
		problems = append(problems, "zero transaction hash")
	}
	if record.EventHash.IsZero() {
		problems = append(problems, "zero event hash")
	}
	if record.Amount == 0 {
		problems = append(problems, "zero amount")
	}
	if record.Kind != EventLock && record.Kind != EventBurn {
		problems = append(problems, "unsupported event kind")
	}
	if record.AssetID == "" {
		problems = append(problems, "empty asset id")
	}
	if len(record.Recipient) == 0 {
		problems = append(problems, "empty recipient")
	}
	if record.ProcessedAt.IsZero() {
		problems = append(problems, "zero processed time")
	}
	expectedKey := MessageKey(record.SourceChain, record.DestinationChain, record.TxHash, record.EventIndex)
	if record.MessageKey != expectedKey {
		problems = append(problems, "message key does not match record fields")
	}
	if len(problems) != 0 {
		return fmt.Errorf("netsec: invalid replay record: %s", strings.Join(problems, ", "))
	}
	return nil
}

func cloneRecord(record Record) Record {
	record.Sender = append([]byte(nil), record.Sender...)
	record.Recipient = append([]byte(nil), record.Recipient...)
	return record
}
