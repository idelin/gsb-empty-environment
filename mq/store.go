package mq

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"
	"time"
)

// Op is the kind of a persisted state transition.
type Op int

const (
	OpEnqueue  Op = iota + 1 // message becomes visible at the tail
	OpInflight               // message is claimed; invisible until Deadline
	OpAck                    // message is deleted
	OpRequeue                // message returns to the visible tail
)

// Record is one persisted state transition. A batch of records applied
// together is the atomic unit of persistence.
type Record struct {
	Op       Op
	Msg      Message
	Gen      uint64
	Token    uint64
	Deadline time.Time
}

// Persister durably records queue mutations. Apply must persist the whole
// batch atomically: if it returns an error, no record of the batch may become
// visible to a later Replay.
type Persister interface {
	Apply(batch []Record) error
	Replay() ([]Record, error)
}

// MemPersister is an in-memory Persister, useful for tests and as a base for
// fault-injection wrappers.
type MemPersister struct {
	mu   sync.Mutex
	recs []Record
}

func NewMemPersister() *MemPersister { return &MemPersister{} }

func (m *MemPersister) Apply(batch []Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs = append(m.recs, batch...)
	return nil
}

func (m *MemPersister) Replay() ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Record, len(m.recs))
	copy(out, m.recs)
	return out, nil
}

// FilePersister appends records to a write-ahead log file. Each line is
// CRC-protected; Apply fsyncs once per batch so a batch is either fully
// durable or (on crash mid-write) dropped as a corrupt tail during Replay.
type FilePersister struct {
	mu   sync.Mutex
	f    *os.File
	path string
}

func OpenFilePersister(path string) (*FilePersister, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return &FilePersister{f: f, path: path}, nil
}

func (p *FilePersister) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.f.Close()
}

func (p *FilePersister) Apply(batch []Record) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var buf []byte
	for _, r := range batch {
		payload, err := json.Marshal(r)
		if err != nil {
			return err
		}
		var line [4]byte
		binary.BigEndian.PutUint32(line[:], crc32.ChecksumIEEE(payload))
		buf = append(buf, line[:]...)
		buf = append(buf, payload...)
		buf = append(buf, '\n')
	}
	if _, err := p.f.Write(buf); err != nil {
		return err
	}
	return p.f.Sync()
}

// Replay returns all intact records and truncates any corrupt tail left by a
// crash mid-batch.
func (p *FilePersister) Replay() ([]Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var recs []Record
	var good int64
	r := bufio.NewReader(p.f)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if rec, ok := decodeLine(line); ok {
				recs = append(recs, rec)
				good += int64(len(line))
			} else {
				break // corrupt record: treat as torn tail
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
	}
	if err := p.f.Truncate(good); err != nil {
		return nil, err
	}
	if _, err := p.f.Seek(good, io.SeekStart); err != nil {
		return nil, err
	}
	return recs, nil
}

func decodeLine(line []byte) (Record, bool) {
	line = line[:len(line)-1] // strip '\n'
	if len(line) < 5 {
		return Record{}, false
	}
	want := binary.BigEndian.Uint32(line[:4])
	payload := line[4:]
	if crc32.ChecksumIEEE(payload) != want {
		return Record{}, false
	}
	var rec Record
	if err := json.Unmarshal(payload, &rec); err != nil {
		return Record{}, false
	}
	return rec, true
}

func (o Op) MarshalJSON() ([]byte, error) { return json.Marshal(int(o)) }
func (o *Op) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	if n < int(OpEnqueue) || n > int(OpRequeue) {
		return fmt.Errorf("mq: unknown op %d", n)
	}
	*o = Op(n)
	return nil
}
