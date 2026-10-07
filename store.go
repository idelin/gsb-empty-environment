package mailbox

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"time"
)

// OpKind identifies the kind of a durable mutation record.
type OpKind string

const (
	OpEnqueue OpKind = "enqueue"
	OpClaim   OpKind = "claim"
	OpAck     OpKind = "ack"
	OpNack    OpKind = "nack"
)

// MessageRecord is the durable form of an enqueued message.
type MessageRecord struct {
	Seq        uint64    `json:"seq"`
	ID         string    `json:"id"`
	Body       []byte    `json:"body"`
	EnqueuedAt time.Time `json:"enqueued_at"`
}

// Op is a single durable mutation. A multi-message send is recorded as
// one Op, which is what makes transactional batches atomic: either the
// whole record is durable, or none of its messages ever become visible.
type Op struct {
	Kind       OpKind          `json:"kind"`
	Messages   []MessageRecord `json:"messages,omitempty"`
	MessageID  string          `json:"message_id,omitempty"`
	Generation uint64          `json:"generation,omitempty"`
	Deadline   time.Time       `json:"deadline,omitempty"`
}

// Store persists queue mutations. Append must make the Op durable before
// returning nil; the Queue applies mutations to its in-memory state only
// after a successful Append, so a failed Append leaves no trace.
type Store interface {
	Append(op Op) error
	Load() ([]Op, error)
}

// MemoryStore is an in-memory Store for tests and ephemeral queues.
type MemoryStore struct {
	Ops []Op
}

func (m *MemoryStore) Append(op Op) error {
	m.Ops = append(m.Ops, op)
	return nil
}

func (m *MemoryStore) Load() ([]Op, error) {
	return append([]Op(nil), m.Ops...), nil
}

// FileStore is an append-only, fsync'd JSON-lines write-ahead log held in
// a single file. On open it truncates a torn tail left by a crash in the
// middle of an append, so replay only ever sees complete records.
type FileStore struct {
	f *os.File
}

// OpenFileStore opens (or creates) the WAL at path and recovers from any
// torn tail before returning.
func OpenFileStore(path string) (*FileStore, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	s := &FileStore{f: f}
	if err := s.recover(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

func (s *FileStore) recover() error {
	_, valid, err := scanOps(s.f)
	if err != nil {
		return err
	}
	if err := s.f.Truncate(valid); err != nil {
		return err
	}
	_, err = s.f.Seek(valid, io.SeekStart)
	return err
}

// Append writes the op as one line and fsyncs before returning.
func (s *FileStore) Append(op Op) error {
	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := s.f.Write(data); err != nil {
		return err
	}
	return s.f.Sync()
}

// Load replays all complete records from the beginning of the file.
func (s *FileStore) Load() ([]Op, error) {
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	ops, valid, err := scanOps(s.f)
	if err != nil {
		return nil, err
	}
	if _, err := s.f.Seek(valid, io.SeekStart); err != nil {
		return nil, err
	}
	return ops, nil
}

// Close closes the underlying file.
func (s *FileStore) Close() error { return s.f.Close() }

// scanOps reads newline-delimited ops from r. It returns the ops decoded
// from complete lines and the offset just past the last complete line; a
// trailing partial line (torn write) is ignored.
func scanOps(r io.Reader) (ops []Op, valid int64, err error) {
	br := bufio.NewReader(r)
	for {
		line, readErr := br.ReadBytes('\n')
		if readErr == nil {
			var op Op
			if uerr := json.Unmarshal(line, &op); uerr != nil {
				return nil, 0, uerr
			}
			ops = append(ops, op)
			valid += int64(len(line))
			continue
		}
		if readErr == io.EOF {
			return ops, valid, nil
		}
		return nil, 0, readErr
	}
}
