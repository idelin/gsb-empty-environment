package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const checkpointVersion = 1

// Checkpoint is the durable, restartable state of a processor.
type Checkpoint struct {
	Version       int    `json:"version"`
	JournalID     uint64 `json:"journal_id"`
	NextUSN       uint64 `json:"next_usn"`
	SavedAtMicros int64  `json:"saved_at_micros"`
	Index         Index  `json:"index"`
}

// Processor applies change-log records to a directory index. It tolerates
// records arriving out of order (they are buffered until the log offset
// catches up), checkpoints atomically, and resumes across restarts including
// unpaired rename halves.
type Processor struct {
	mu        sync.RWMutex
	path      string
	journalID uint64
	nextUSN   uint64
	index     *Index
	// buffered holds records whose USN is ahead of nextUSN; the gap has not
	// been delivered yet. Records are applied strictly in USN order.
	buffered map[uint64]Record
}

// OpenProcessor opens a checkpointed processor, or starts a fresh one when no
// checkpoint exists. currentLowUSN is the oldest USN still retained by the
// change log. On resume, a checkpoint past the retained range yields ErrGap;
// a changed log incarnation yields ErrJournalID.
func OpenProcessor(path string, journalID, currentLowUSN uint64) (*Processor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		p := &Processor{
			path:      path,
			journalID: journalID,
			nextUSN:   currentLowUSN,
			index:     newIndex(),
			buffered:  map[uint64]Record{},
		}
		return p, p.checkpointLocked()
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("journal: corrupt checkpoint: %w", err)
	}
	if cp.Version != checkpointVersion {
		return nil, fmt.Errorf("journal: unsupported checkpoint version %d", cp.Version)
	}
	if cp.JournalID != journalID {
		return nil, fmt.Errorf("%w: checkpoint %d, log %d", ErrJournalID, cp.JournalID, journalID)
	}
	if cp.NextUSN < currentLowUSN {
		return nil, fmt.Errorf("%w: next usn %d below retained low usn %d", ErrGap, cp.NextUSN, currentLowUSN)
	}

	idx := cp.Index
	if idx.Objects == nil {
		idx = *newIndex()
	}
	return &Processor{
		path:      path,
		journalID: journalID,
		nextUSN:   cp.NextUSN,
		index:     &idx,
		buffered:  map[uint64]Record{},
	}, nil
}

// ApplyBatch accepts one delivery of change records. Records may be in any
// order and may arrive in multiple batches: every record is buffered and
// applied strictly by USN once the contiguous prefix reaches it. After each
// batch the checkpoint is atomically rewritten, so an unpaired rename half
// already survives a crash. It returns the number of records newly applied.
func (p *Processor) ApplyBatch(records []Record) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, r := range records {
		if r.JournalID != p.journalID {
			return 0, fmt.Errorf("%w: record %d, processor %d", ErrJournalID, r.JournalID, p.journalID)
		}
		if r.USN < p.nextUSN {
			// Already applied in an earlier, checkpointed batch: idempotent skip.
			continue
		}
		// Keep the first record seen for a USN; duplicate deliveries are ignored.
		if _, seen := p.buffered[r.USN]; !seen {
			p.buffered[r.USN] = r
		}
	}

	applied := 0
	for {
		r, ok := p.buffered[p.nextUSN]
		if !ok {
			break
		}
		p.index.applyRecord(r)
		delete(p.buffered, p.nextUSN)
		p.nextUSN++
		applied++
	}

	if err := p.checkpointLocked(); err != nil {
		return applied, err
	}
	return applied, nil
}

// ValidateJournal checks the live log against the checkpoint. It must be
// called when the reader reports a new retained range or log incarnation.
func (p *Processor) ValidateJournal(journalID, currentLowUSN uint64) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if journalID != p.journalID {
		return fmt.Errorf("%w: checkpoint %d, log %d", ErrJournalID, p.journalID, journalID)
	}
	if p.nextUSN < currentLowUSN {
		return fmt.Errorf("%w: next usn %d below retained low usn %d", ErrGap, p.nextUSN, currentLowUSN)
	}
	return nil
}

// Reset discards all index state and starts a fresh lineage against a new log
// incarnation. It is the documented recovery after ErrGap/ErrJournalID,
// once the caller has rebuilt the index from a full scan.
func (p *Processor) Reset(journalID, lowUSN uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.journalID = journalID
	p.nextUSN = lowUSN
	p.index = newIndex()
	p.buffered = map[uint64]Record{}
	return p.checkpointLocked()
}

// Snapshot returns an immutable point-in-time view of the index.
func (p *Processor) Snapshot() *Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return newSnapshot(p.index)
}

// Stats returns the current counters.
func (p *Processor) Stats() Stats {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.index.Stats
}

// NextUSN returns the next expected log offset.
func (p *Processor) NextUSN() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.nextUSN
}

// PendingRenames reports how many rename halves are waiting for their partner.
func (p *Processor) PendingRenames() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.index.pendingCount()
}

// BufferedCount reports records held because their USN is ahead of the next
// contiguous offset; they have not been applied and are not checkpointed yet.
func (p *Processor) BufferedCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.buffered)
}

// CheckpointPath exposes where state is persisted.
func (p *Processor) CheckpointPath() string { return p.path }

func (p *Processor) checkpointLocked() error {
	cp := Checkpoint{
		Version:       checkpointVersion,
		JournalID:     p.journalID,
		NextUSN:       p.nextUSN,
		SavedAtMicros: time.Now().UnixMicro(),
		Index:         *p.index,
	}
	data, err := json.MarshalIndent(&cp, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(p.path)
	tmp, err := os.CreateTemp(dir, filepath.Base(p.path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, p.path); err != nil {
		return err
	}
	return nil
}
