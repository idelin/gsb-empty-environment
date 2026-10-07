package receipt

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	logFileName        = "events.log"
	checkpointFileName = "checkpoint.json"
)

// Store persists the event stream on disk and rebuilds the Machine on
// recovery: latest checkpoint plus replay of the log tail.
type Store struct {
	dir     string
	log     *os.File
	machine *Machine
	seq     int64
}

// Open creates or recovers a store rooted at dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, machine: NewMachine()}
	if err := s.recover(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, logFileName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s.log = f
	return s, nil
}

func (s *Store) recover() error {
	cp := filepath.Join(s.dir, checkpointFileName)
	var fromSeq int64
	if data, err := os.ReadFile(cp); err == nil {
		var snap struct {
			Seq      int64    `json:"seq"`
			Snapshot Snapshot `json:"snapshot"`
		}
		if err := json.Unmarshal(data, &snap); err != nil {
			return fmt.Errorf("recover: corrupt checkpoint: %w", err)
		}
		s.machine = RestoreSnapshot(snap.Snapshot)
		fromSeq = snap.Seq
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	f, err := os.Open(filepath.Join(s.dir, logFileName))
	if errors.Is(err, os.ErrNotExist) {
		s.seq = fromSeq
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		e, err := decodeEvent(line)
		if err != nil {
			return fmt.Errorf("recover: corrupt log line: %w", err)
		}
		if e.Seq <= fromSeq {
			continue // already folded into the checkpoint
		}
		if err := s.machine.Apply(e); err != nil && !isBusinessError(err) {
			return fmt.Errorf("recover: replay event %d: %w", e.Seq, err)
		}
		if e.Seq > s.seq {
			s.seq = e.Seq
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if s.seq < fromSeq {
		s.seq = fromSeq
	}
	return nil
}

// isBusinessError marks expected outcomes that are themselves persisted
// evidence (conflicts excluded: they are never persisted).
func isBusinessError(err error) bool {
	var reg *RegressionError
	var unk *UnknownGenerationError
	return errors.As(err, &reg) || errors.As(err, &unk)
}

// Machine exposes the read model.
func (s *Store) Machine() *Machine { return s.machine }

// Bootstrap records the first execution generation.
func (s *Store) Bootstrap(generation string) error {
	if err := s.machine.Bootstrap(generation); err != nil {
		return err
	}
	return s.commit(Event{Type: EventBootstrap, Bootstrap: &generation})
}

// Submit records a receipt. A conflicting receipt is rejected and never
// persisted; archived and buffered receipts are persisted as evidence even
// when they do not advance business state.
func (s *Store) Submit(r Receipt) error {
	err := s.machine.applyReceipt(r)
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		return err // rejected, nothing persisted, state untouched
	}
	if perr := s.commit(Event{Type: EventReceipt, Receipt: &r}); perr != nil {
		return perr
	}
	return err
}

// Supersede commits a resolution replacing replacing old by new.
func (s *Store) Supersede(old, new string) error {
	rec := SupersedeRecord{Old: old, New: new}
	if err := s.machine.applySupersede(rec); err != nil {
		return err
	}
	return s.commit(Event{Type: EventSupersede, Supersede: &rec})
}

func (s *Store) commit(e Event) error {
	s.seq++
	e.Seq = s.seq
	line, err := encodeEvent(e)
	if err != nil {
		return err
	}
	if _, err := s.log.Write(line); err != nil {
		return err
	}
	return s.log.Sync()
}

// Checkpoint persists a full snapshot and truncates the log; replay after
// recovery starts from the checkpoint.
func (s *Store) Checkpoint() error {
	snap := struct {
		Seq      int64    `json:"seq"`
		Snapshot Snapshot `json:"snapshot"`
	}{Seq: s.seq, Snapshot: s.machine.Snapshot()}
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, checkpointFileName+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, checkpointFileName)); err != nil {
		return err
	}
	// Truncate the log: all events up to seq are folded into the snapshot.
	if err := s.log.Truncate(0); err != nil {
		return err
	}
	if _, err := s.log.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return s.log.Sync()
}

// Close flushes and closes the log.
func (s *Store) Close() error {
	if s.log == nil {
		return nil
	}
	err := s.log.Close()
	s.log = nil
	return err
}
