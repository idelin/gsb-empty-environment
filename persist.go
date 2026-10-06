package receipt

import (
	"bufio"
	"encoding/json"
	"os"
)

const (
	kindRegister  = "register"
	kindReceipt   = "receipt"
	kindSupersede = "supersede"
)

// Event is one entry in the persistent log. State is always derivable by
// replaying events in Seq order.
type Event struct {
	Seq      uint64
	Kind     string
	ItemID   string
	Epoch    uint64
	Receipt  *Receipt           `json:",omitempty"`
	Decision *SupersedeDecision `json:",omitempty"`
}

type eventLog struct {
	f   *os.File
	enc *json.Encoder
}

func openEventLog(path string) (*eventLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &eventLog{f: f, enc: json.NewEncoder(f)}, nil
}

func (l *eventLog) append(e Event) error {
	if err := l.enc.Encode(e); err != nil {
		return err
	}
	return l.f.Sync()
}

func (l *eventLog) close() error { return l.f.Close() }

func readEvents(path string) ([]Event, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var events []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, sc.Err()
}

func (s *Store) append(e Event) error {
	e.Seq = s.seq + 1
	if s.log != nil {
		if err := s.log.append(e); err != nil {
			return err
		}
	}
	s.seq = e.Seq
	return nil
}

func (s *Store) replay(e Event) {
	switch e.Kind {
	case kindRegister:
		if _, ok := s.items[e.ItemID]; !ok {
			s.items[e.ItemID] = newItemState(e.Epoch)
		}
	case kindReceipt:
		it, ok := s.items[e.ItemID]
		if !ok {
			return
		}
		r := *e.Receipt
		if _, dup := it.Evidence[evidenceKey(r.Epoch, r.Revision)]; !dup {
			it.foldReceipt(r)
		}
	case kindSupersede:
		if it, ok := s.items[e.ItemID]; ok {
			it.supersede(*e.Decision)
		}
	}
}

type checkpoint struct {
	Seq   uint64
	Items map[string]*itemState
}

// Open recovers a persistent store: it loads the latest checkpoint (if any)
// and replays the log tail after it. New events are appended to logPath.
func Open(logPath, checkpointPath string) (*Store, error) {
	s := New()
	if checkpointPath != "" {
		if err := s.loadCheckpoint(checkpointPath); err != nil {
			return nil, err
		}
	}
	events, err := readEvents(logPath)
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		if e.Seq <= s.seq {
			continue
		}
		s.replay(e)
		s.seq = e.Seq
	}
	l, err := openEventLog(logPath)
	if err != nil {
		return nil, err
	}
	s.log = l
	s.snap = checkpointPath
	return s, nil
}

// Checkpoint writes an atomic snapshot of the folded state; recovery then
// only replays events logged after the snapshot.
func (s *Store) Checkpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snap == "" {
		return ErrNotPersistent
	}
	tmp := s.snap + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	cp := checkpoint{Seq: s.seq, Items: s.items}
	if err := json.NewEncoder(f).Encode(cp); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.snap)
}

func (s *Store) loadCheckpoint(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	var cp checkpoint
	if err := json.NewDecoder(f).Decode(&cp); err != nil {
		return err
	}
	s.seq = cp.Seq
	s.items = cp.Items
	for _, it := range s.items {
		it.rebuild()
	}
	return nil
}

// Close flushes and closes the underlying log.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.log != nil {
		return s.log.close()
	}
	return nil
}
