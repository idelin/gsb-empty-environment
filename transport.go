package bigtxn

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// Sink receives chunks in order.
type Sink interface {
	WriteChunk(c *Chunk) error
}

// TxnTracker tracks the integrity of one sharded transaction: chunk
// order, phase transitions, row totals and a rolling checksum. The
// transaction may only commit when the End chunk has arrived and every
// expected chunk has been seen.
type TxnTracker struct {
	txnID          uint64
	started        bool
	ended          bool
	nextSeq        int
	chunks         int
	rows           int
	expectedChunks int
	checksum       uint32
}

// Track validates and records one chunk.
func (t *TxnTracker) Track(c *Chunk) error {
	if t.ended {
		return fmt.Errorf("bigtxn: chunk %d arrived after END of txn %d", c.Seq, c.TxnID)
	}
	if !t.started {
		if c.Phase != PhaseBegin && c.Phase != PhaseSingle {
			return fmt.Errorf("bigtxn: txn %d starts with phase %s", c.TxnID, c.Phase)
		}
		t.started = true
		t.txnID = c.TxnID
	} else if c.TxnID != t.txnID {
		return fmt.Errorf("bigtxn: mixed txn ids %d and %d", t.txnID, c.TxnID)
	}
	if c.Seq != t.nextSeq {
		return fmt.Errorf("bigtxn: txn %d chunk out of order: got seq %d, want %d", c.TxnID, c.Seq, t.nextSeq)
	}
	t.nextSeq++
	t.chunks++
	t.rows += len(c.Rows)
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], c.Checksum)
	t.checksum = crc32.Update(t.checksum, crc32.IEEETable, b[:])
	switch c.Phase {
	case PhaseSingle, PhaseEnd:
		t.ended = true
		t.expectedChunks = c.Seq + 1
	}
	return nil
}

// Complete reports whether the transaction closed with every chunk
// accounted for.
func (t *TxnTracker) Complete() bool {
	return t.started && t.ended && t.chunks == t.expectedChunks
}

// Rows returns the total changes tracked.
func (t *TxnTracker) Rows() int { return t.rows }

// Checksum returns the rolling checksum over all chunk checksums.
func (t *TxnTracker) Checksum() uint32 { return t.checksum }

// Report summarizes one Transfer run.
type Report struct {
	TxnID         uint64
	Chunks        int
	Rows          int
	SpilledChunks int
	PeakInFlight  int64 // peak bytes held in memory, per the limiter
	Checksum      uint32
}

// Transporter moves a sharded transaction from a ChangeSource to a Sink
// under a memory budget, spilling to disk when the budget is exhausted.
type Transporter struct {
	Chunker *Chunker
	Limiter *MemoryLimiter // optional
	Spiller *SpillBuffer   // optional, required if the limiter can deny
}

// Transfer streams the whole transaction produced by src into sink and
// returns a report. It fails if the transaction does not close cleanly.
func (t *Transporter) Transfer(src ChangeSource, sink Sink) (*Report, error) {
	chunker := t.Chunker
	if chunker == nil {
		chunker = &Chunker{}
	}
	tracker := &TxnTracker{}
	rep := &Report{}
	err := chunker.Stream(src, func(c *Chunk) error {
		if err := t.deliver(c, sink, rep); err != nil {
			return err
		}
		if err := tracker.Track(c); err != nil {
			return err
		}
		rep.TxnID = c.TxnID
		rep.Chunks++
		rep.Rows += len(c.Rows)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !tracker.Complete() {
		return nil, fmt.Errorf("bigtxn: incomplete transaction: %d chunks, %d rows", tracker.chunks, tracker.rows)
	}
	rep.Checksum = tracker.Checksum()
	if t.Limiter != nil {
		rep.PeakInFlight = t.Limiter.Peak()
	}
	return rep, nil
}

// deliver hands one chunk to the sink, going through the limiter and the
// spill buffer when configured.
func (t *Transporter) deliver(c *Chunk, sink Sink, rep *Report) error {
	size := c.Size()
	if t.Limiter == nil {
		return sink.WriteChunk(c)
	}
	if t.Limiter.TryReserve(size) {
		defer t.Limiter.Release(size)
		return sink.WriteChunk(c)
	}
	// Memory budget exhausted: spill the chunk to temporary disk instead
	// of holding it in memory, then stream it back through the limiter.
	if t.Spiller == nil {
		return fmt.Errorf("bigtxn: memory budget exceeded and no spill buffer configured")
	}
	path, err := t.Spiller.Write(c)
	if err != nil {
		return err
	}
	rep.SpilledChunks++
	back, err := t.Spiller.Read(path)
	if err != nil {
		return err
	}
	if err := t.Spiller.Remove(path); err != nil {
		return err
	}
	t.Limiter.Acquire(back.Size())
	defer t.Limiter.Release(back.Size())
	return sink.WriteChunk(back)
}

// Receiver is a Sink that verifies chunk checksums and tracks overall
// transaction integrity, standing in for the receiving database.
type Receiver struct {
	tracker TxnTracker
}

// WriteChunk verifies and applies one chunk.
func (r *Receiver) WriteChunk(c *Chunk) error {
	if got := checksumRows(c.Rows); got != c.Checksum {
		return fmt.Errorf("bigtxn: checksum mismatch on txn %d chunk %d", c.TxnID, c.Seq)
	}
	return r.tracker.Track(c)
}

// Complete reports whether the transaction closed with all chunks seen.
func (r *Receiver) Complete() bool { return r.tracker.Complete() }

// Rows returns the number of changes applied.
func (r *Receiver) Rows() int { return r.tracker.rows }

// Chunks returns the number of chunks applied.
func (r *Receiver) Chunks() int { return r.tracker.chunks }

// Checksum returns the rolling checksum over all applied chunks; it must
// match the sender's Report.Checksum for end-to-end integrity.
func (r *Receiver) Checksum() uint32 { return r.tracker.Checksum() }
