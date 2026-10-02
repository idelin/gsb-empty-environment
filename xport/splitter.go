package xport

import "fmt"

// ChangeIter yields the changes of one transaction lazily, so the source
// never has to materialize the whole transaction in memory.
type ChangeIter func() (Change, bool)

// SplitStats summarizes one Split run.
type SplitStats struct {
	Chunks int
	Rows   int
	Bytes  int64
}

// Splitter cuts a change stream into bounded-size chunks carrying stage
// markers. When the change count exceeds Threshold the transaction is
// emitted as BEGIN/MIDDLE*/END; otherwise as a single SINGLE chunk.
type Splitter struct {
	// Threshold is the transaction size (rows) above which chunking applies.
	Threshold int
	// BatchSize is the max number of changes per chunk; defaults to Threshold.
	BatchSize int
}

// Split drains iter and invokes emit for every produced chunk, in order.
// The final chunk carries the transaction totals and checksum so the
// receiver can verify integrity. Emission is delayed by one chunk so the
// final chunk can be marked END without emitting empty trailers.
func (s *Splitter) Split(txID uint64, iter ChangeIter, emit func(*Chunk) error) (SplitStats, error) {
	batchSize := s.BatchSize
	if batchSize <= 0 {
		batchSize = s.Threshold
	}
	if batchSize <= 0 {
		batchSize = 1000
	}
	var stats SplitStats
	var txSum uint64
	seq := 0

	buf := make([]Change, 0, batchSize)
	var pending *Chunk // last full chunk, held back until more data arrives

	emitChunk := func(c *Chunk) error {
		if c.Seq == 0 && c.Stage == StageMiddle {
			c.Stage = StageBegin
		}
		if err := emit(c); err != nil {
			return err
		}
		stats.Chunks++
		stats.Bytes += c.Bytes()
		seq++
		return nil
	}

	for {
		ch, ok := iter()
		if !ok {
			break
		}
		buf = append(buf, ch)
		stats.Rows++
		if len(buf) >= batchSize {
			if pending != nil {
				if err := emitChunk(pending); err != nil {
					return stats, fmt.Errorf("split: emit chunk %d: %w", pending.Seq, err)
				}
			}
			sum := chunkChecksum(buf)
			txSum = foldChecksums(txSum, sum)
			pending = &Chunk{TxID: txID, Seq: seq, Stage: StageMiddle, Changes: buf, Checksum: sum}
			buf = make([]Change, 0, batchSize)
		}
	}
	if stats.Rows == 0 {
		return stats, nil
	}
	// Assemble the final chunk from leftover rows (possibly empty when the
	// transaction size is an exact multiple of the batch size).
	if len(buf) > 0 {
		if pending != nil {
			if err := emitChunk(pending); err != nil {
				return stats, fmt.Errorf("split: emit chunk %d: %w", pending.Seq, err)
			}
		}
		sum := chunkChecksum(buf)
		txSum = foldChecksums(txSum, sum)
		pending = &Chunk{TxID: txID, Seq: seq, Stage: StageMiddle, Changes: buf, Checksum: sum}
	}
	pending.TotalChunks = seq + 1
	pending.TotalRows = stats.Rows
	pending.TxChecksum = txSum
	if pending.Seq == 0 {
		pending.Stage = StageSingle
	} else {
		pending.Stage = StageEnd
	}
	if err := emitChunk(pending); err != nil {
		return stats, fmt.Errorf("split: emit final chunk: %w", err)
	}
	return stats, nil
}
