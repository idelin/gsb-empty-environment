package logtrunc

import (
	"encoding/json"
	"io"
	"os"
	"sort"
	"sync"
)

// Request is a truncation request: delete these segments.
type Request struct {
	ID         string   `json:"id"`
	SegmentIDs []string `json:"segment_ids"`
}

type storeEntry struct {
	Kind    string   `json:"kind"` // "submit" or "done"
	Request *Request `json:"request,omitempty"`
	ID      string   `json:"id,omitempty"`
}

// Store durably records truncation requests as an append-only JSON
// journal so that a crash or restart never forgets unfinished work.
// Reopening the file replays the journal: submitted requests stay
// pending until a matching "done" entry is found.
type Store struct {
	mu      sync.Mutex
	path    string
	pending map[string]Request
}

// OpenStore opens (or creates) the journal at path and replays it to
// reconstruct the set of pending requests.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, pending: make(map[string]Request)}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var e storeEntry
		err := dec.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch e.Kind {
		case "submit":
			if e.Request != nil {
				s.pending[e.Request.ID] = *e.Request
			}
		case "done":
			delete(s.pending, e.ID)
		}
	}
	return s, nil
}

// Submit persists a new pending request. Submitting an ID that is
// already pending is a no-op, so retries of Submit are safe.
func (s *Store) Submit(r Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pending[r.ID]; ok {
		return nil
	}
	if err := s.appendLocked(storeEntry{Kind: "submit", Request: &r}); err != nil {
		return err
	}
	s.pending[r.ID] = r
	return nil
}

// Complete marks a request done. It is a no-op for unknown IDs.
func (s *Store) Complete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pending[id]; !ok {
		return nil
	}
	if err := s.appendLocked(storeEntry{Kind: "done", ID: id}); err != nil {
		return err
	}
	delete(s.pending, id)
	return nil
}

// Pending returns all unfinished requests, ordered by ID.
func (s *Store) Pending() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, 0, len(s.pending))
	for _, r := range s.pending {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) appendLocked(e storeEntry) error {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(&e); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
