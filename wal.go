package twopc

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// RecordType identifies the kind of a WAL record.
type RecordType string

const (
	RecBegin    RecordType = "BEGIN"    // transaction started, participants known
	RecVote     RecordType = "VOTE"     // a participant's vote was durably recorded
	RecDecision RecordType = "DECISION" // commit/abort decision (the atomic commit point)
	RecAck      RecordType = "ACK"      // participant acknowledged the final decision
	RecDone     RecordType = "DONE"     // transaction fully resolved, may be garbage collected
	RecAudit    RecordType = "AUDIT"    // pending/conflict audit event for operators
)

// Record is a single write-ahead log entry describing a state transition.
type Record struct {
	Type         RecordType `json:"type"`
	TxID         string     `json:"txid"`
	Participant  string     `json:"participant,omitempty"`
	Vote         Vote       `json:"vote,omitempty"`
	Decision     Decision   `json:"decision,omitempty"`
	Participants []string   `json:"participants,omitempty"`
	Reason       string     `json:"reason,omitempty"`
	Time         time.Time  `json:"time"`
}

// WAL is the write-ahead log. Every coordinator state transition must be
// appended (and durable) before the corresponding external action is taken.
type WAL interface {
	Append(r Record) error
}

// FileWAL is a JSON-lines append-only WAL with fsync on every append.
type FileWAL struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

// OpenFileWAL opens (creating if needed) the WAL at path and returns it along
// with all records previously persisted. A torn final line (crash mid-write)
// is ignored, which is safe because appends are atomic state transitions.
func OpenFileWAL(path string) (*FileWAL, []Record, error) {
	records, err := LoadRecords(path)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, nil, err
	}
	return &FileWAL{f: f, enc: json.NewEncoder(f)}, records, nil
}

// LoadRecords reads all complete records from the WAL file at path.
// A missing file yields zero records.
func LoadRecords(path string) ([]Record, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var records []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			continue // torn tail from a crash: ignore incomplete record
		}
		records = append(records, r)
	}
	return records, sc.Err()
}

// Append writes the record and fsyncs before returning.
func (w *FileWAL) Append(r Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.enc.Encode(r); err != nil {
		return err
	}
	return w.f.Sync()
}

// Close closes the underlying file.
func (w *FileWAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}

// MemWAL is an in-memory WAL, useful for tests and embedding.
type MemWAL struct {
	mu      sync.Mutex
	Records []Record
}

// Append appends the record to memory.
func (m *MemWAL) Append(r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Records = append(m.Records, r)
	return nil
}
