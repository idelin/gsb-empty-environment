package bigtxn

import (
	"encoding/binary"
	"hash/crc32"
	"io"
)

// Phase marks the position of a chunk inside its transaction.
type Phase int

const (
	// PhaseSingle means the whole transaction fits in one chunk.
	PhaseSingle Phase = iota
	// PhaseBegin marks the first chunk of a multi-chunk transaction.
	PhaseBegin
	// PhaseContinue marks an intermediate chunk.
	PhaseContinue
	// PhaseEnd marks the last chunk and carries the commit boundary.
	PhaseEnd
)

func (p Phase) String() string {
	switch p {
	case PhaseSingle:
		return "SINGLE"
	case PhaseBegin:
		return "BEGIN"
	case PhaseContinue:
		return "CONTINUE"
	case PhaseEnd:
		return "END"
	}
	return "UNKNOWN"
}

// Chunk is a shard of a large transaction, tagged with a phase marker so
// the receiver can reassemble and commit the transaction atomically.
type Chunk struct {
	TxnID    uint64
	Seq      int
	Phase    Phase
	Rows     []*Change
	Checksum uint32 // CRC32 of the serialized rows
}

// RowCount returns the number of changes carried by the chunk.
func (c *Chunk) RowCount() int { return len(c.Rows) }

// Size estimates the in-memory footprint of the chunk in bytes.
func (c *Chunk) Size() int64 {
	var n int64 = 64
	for _, r := range c.Rows {
		n += r.Size()
	}
	return n
}

func checksumRows(rows []*Change) uint32 {
	h := crc32.NewIEEE()
	var buf [8]byte
	for _, r := range rows {
		binary.BigEndian.PutUint64(buf[:], r.TxnID)
		h.Write(buf[:])
		binary.BigEndian.PutUint64(buf[:], r.Seq)
		h.Write(buf[:])
		h.Write([]byte{byte(r.Op)})
		h.Write([]byte(r.Table))
		binary.BigEndian.PutUint64(buf[:], uint64(r.Key))
		h.Write(buf[:])
		h.Write(r.Data)
	}
	return h.Sum32()
}

// Chunker splits a change stream into phase-marked chunks. It holds at
// most one chunk in memory, so arbitrarily large transactions stream
// through in bounded space.
type Chunker struct {
	// MaxRows caps the number of changes per chunk. Default 5000.
	MaxRows int
	// MaxBytes caps the estimated in-memory bytes per chunk. 0 disables.
	MaxBytes int64
}

// Stream drains src and invokes emit for every chunk, in order. The emit
// callback owns the chunk; rows are never retained by the chunker.
func (ch *Chunker) Stream(src ChangeSource, emit func(*Chunk) error) error {
	maxRows := ch.MaxRows
	if maxRows <= 0 {
		maxRows = 5000
	}
	var (
		rows        []*Change
		nbytes      int64
		seq         int
		txnID       uint64
		started     bool
		pending     *Change
		havePending bool
	)
	emitChunk := func(phase Phase) error {
		c := &Chunk{
			TxnID:    txnID,
			Seq:      seq,
			Phase:    phase,
			Rows:     rows,
			Checksum: checksumRows(rows),
		}
		seq++
		rows = nil
		nbytes = 0
		return emit(c)
	}
	for {
		var change *Change
		if havePending {
			change = pending
			havePending = false
		} else {
			var err error
			change, err = src.Next()
			if err == io.EOF {
				if len(rows) == 0 {
					return nil
				}
				if seq == 0 {
					return emitChunk(PhaseSingle)
				}
				return emitChunk(PhaseEnd)
			}
			if err != nil {
				return err
			}
		}
		if !started {
			txnID = change.TxnID
			started = true
		}
		rows = append(rows, change)
		nbytes += change.Size()
		if len(rows) >= maxRows || (ch.MaxBytes > 0 && nbytes >= ch.MaxBytes) {
			// Look ahead one change to learn whether this chunk closes
			// the transaction, so the final chunk gets PhaseEnd.
			next, err := src.Next()
			if err == io.EOF {
				if seq == 0 {
					if err := emitChunk(PhaseSingle); err != nil {
						return err
					}
				} else if err := emitChunk(PhaseEnd); err != nil {
					return err
				}
				return nil
			}
			if err != nil {
				return err
			}
			pending = next
			havePending = true
			phase := PhaseContinue
			if seq == 0 {
				phase = PhaseBegin
			}
			if err := emitChunk(phase); err != nil {
				return err
			}
		}
	}
}
