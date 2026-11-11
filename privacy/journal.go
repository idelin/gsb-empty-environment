package privacy

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"sync"
)

type recordType string

const (
	recReserve recordType = "reserve"
	recRelease recordType = "release"
	recResult  recordType = "result"
)

// record is one journal entry. A "result" record is the publish point: it
// atomically settles the reservation for Key and stores the noisy value.
type record struct {
	Type  recordType         `json:"type"`
	Key   string             `json:"key"`
	Costs map[string]float64 `json:"costs,omitempty"`
	Value float64            `json:"value,omitempty"`
}

// Journal is an append-only write-ahead log. Every record is fsynced before
// the in-memory state is updated, so a crash never loses a published result.
type Journal struct {
	mu sync.Mutex
	f  *os.File
}

// OpenJournal opens (or creates) the journal at path and returns the records
// replayed from it. A torn tail left by a crash mid-write is truncated.
func OpenJournal(path string) (*Journal, []record, error) {
	var recs []record
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		orig := len(data)
		off := 0
		for len(data) > 0 {
			i := bytes.IndexByte(data, '\n')
			if i < 0 {
				break // torn tail: no terminating newline
			}
			var r record
			if json.Unmarshal(data[:i], &r) != nil {
				break // torn tail: undecodable line
			}
			recs = append(recs, r)
			off += i + 1
			data = data[i+1:]
		}
		if off < orig {
			if err := os.Truncate(path, int64(off)); err != nil {
				return nil, nil, err
			}
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return &Journal{f: f}, recs, nil
}

func (j *Journal) append(r record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, err := j.f.Write(append(b, '\n')); err != nil {
		return err
	}
	return j.f.Sync()
}

func (j *Journal) Close() error { return j.f.Close() }
