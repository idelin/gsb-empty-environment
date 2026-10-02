package xport

import "fmt"

// ReceiveStats summarizes a successfully reassembled transaction.
type ReceiveStats struct {
	TxID   uint64
	Chunks int
	Rows   int
}

// Reassembler consumes chunks of one transaction in order, verifies stage
// markers, sequence continuity and the final transaction checksum, and
// applies each change to sink as it arrives (streaming, no full buffering).
type Reassembler struct {
	txID    uint64
	started bool
	done    bool
	nextSeq int
	chunks  int
	rows    int
	txSum   uint64
}

// Apply processes one chunk. It must be called with chunks in Seq order.
func (r *Reassembler) Apply(c *Chunk, sink func(Change) error) error {
	if r.done {
		return fmt.Errorf("receive: tx %d already complete, got extra chunk seq=%d", r.txID, c.Seq)
	}
	if !r.started {
		if c.Stage != StageBegin && c.Stage != StageSingle {
			return fmt.Errorf("receive: tx %d first chunk has stage %s, want BEGIN/SINGLE", c.TxID, c.Stage)
		}
		r.txID = c.TxID
		r.started = true
	} else {
		if c.TxID != r.txID {
			return fmt.Errorf("receive: chunk tx=%d does not match in-flight tx=%d", c.TxID, r.txID)
		}
		if c.Stage == StageBegin || c.Stage == StageSingle {
			return fmt.Errorf("receive: tx %d duplicate %s chunk at seq=%d", r.txID, c.Stage, c.Seq)
		}
	}
	if c.Seq != r.nextSeq {
		return fmt.Errorf("receive: tx %d chunk seq=%d out of order, want %d", r.txID, c.Seq, r.nextSeq)
	}
	if sum := chunkChecksum(c.Changes); sum != c.Checksum {
		return fmt.Errorf("receive: tx %d chunk %d checksum mismatch", r.txID, c.Seq)
	}
	for _, ch := range c.Changes {
		if err := sink(ch); err != nil {
			return fmt.Errorf("receive: sink failed at chunk %d: %w", c.Seq, err)
		}
	}
	r.txSum = foldChecksums(r.txSum, c.Checksum)
	r.chunks++
	r.rows += len(c.Changes)
	r.nextSeq++

	if c.Stage == StageEnd || c.Stage == StageSingle {
		if c.TotalChunks != r.chunks {
			return fmt.Errorf("receive: tx %d integrity: got %d chunks, header says %d", r.txID, r.chunks, c.TotalChunks)
		}
		if c.TotalRows != r.rows {
			return fmt.Errorf("receive: tx %d integrity: got %d rows, header says %d", r.txID, r.rows, c.TotalRows)
		}
		if c.TxChecksum != r.txSum {
			return fmt.Errorf("receive: tx %d integrity: checksum mismatch", r.txID)
		}
		r.done = true
	}
	return nil
}

// Finish verifies the transaction reached a terminal chunk.
func (r *Reassembler) Finish() (ReceiveStats, error) {
	if !r.started {
		return ReceiveStats{}, fmt.Errorf("receive: no chunks received")
	}
	if !r.done {
		return ReceiveStats{}, fmt.Errorf("receive: tx %d truncated after %d chunks, no END marker", r.txID, r.chunks)
	}
	return ReceiveStats{TxID: r.txID, Chunks: r.chunks, Rows: r.rows}, nil
}
