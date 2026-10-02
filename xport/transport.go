package xport

import "fmt"

// Transport streams one large transaction from a change iterator to a sink,
// chunking it, bounding memory with a limiter, and spilling to a temporary
// file whenever the in-memory budget is exhausted.
type Transport struct {
	// Threshold is the transaction size (rows) above which chunking applies.
	Threshold int
	// BatchSize is the max number of changes per chunk.
	BatchSize int
	// MemLimit is the in-memory budget in bytes for buffered chunks.
	MemLimit int64
	// QueueCap bounds how many chunks may be in flight concurrently.
	QueueCap int
	// SpillDir is the directory for temporary spill files ("" = system temp).
	SpillDir string
}

// Stats summarizes one Transfer run.
type Stats struct {
	Split   SplitStats
	Receive ReceiveStats

	SpilledChunks int
	SpilledBytes  int64
	InMemChunks   int
	PeakReserved  int64 // peak bytes held in memory at once (limiter)
}

type queueItem struct {
	chunk   *Chunk // non-nil when held in memory
	spilled bool   // true when the chunk must be read back from the spool
}

// Transfer moves the transaction produced by iter to sink. The sink is
// invoked once per change, in order, from the calling goroutine.
func (t *Transport) Transfer(txID uint64, iter ChangeIter, sink func(Change) error) (Stats, error) {
	var st Stats
	lim := NewMemLimiter(t.MemLimit)
	queueCap := t.QueueCap
	if queueCap <= 0 {
		queueCap = 4
	}
	spool, err := NewSpool(t.SpillDir)
	if err != nil {
		return st, err
	}
	defer spool.Remove()

	queue := make(chan queueItem, queueCap)
	errc := make(chan error, 1)

	var sendSplit SplitStats
	var sendInMem int
	splitter := &Splitter{
		Threshold: t.Threshold,
		BatchSize: t.BatchSize,
	}
	go func() {
		stats, err := splitter.Split(txID, iter, func(c *Chunk) error {
			if lim.TryAcquire(c.Bytes()) {
				sendInMem++
				queue <- queueItem{chunk: c}
				return nil
			}
			if err := spool.Write(c); err != nil {
				return err
			}
			queue <- queueItem{spilled: true}
			return nil
		})
		if err == nil {
			err = spool.Close()
		}
		sendSplit = stats
		errc <- err
		close(queue)
	}()

	reader, err := spool.Open()
	if err != nil {
		return st, err
	}
	defer reader.Close()

	reasm := &Reassembler{}
	for item := range queue {
		var c *Chunk
		if item.spilled {
			sc, err := reader.Next()
			if err != nil {
				return st, fmt.Errorf("transport: read spilled chunk: %w", err)
			}
			c = sc
			st.SpilledChunks++
			st.SpilledBytes += c.Bytes()
		} else {
			c = item.chunk
			lim.Release(c.Bytes())
		}
		if err := reasm.Apply(c, sink); err != nil {
			return st, err
		}
	}
	if err := <-errc; err != nil {
		return st, err
	}
	st.Split = sendSplit
	st.InMemChunks = sendInMem
	rcv, err := reasm.Finish()
	if err != nil {
		return st, err
	}
	st.Receive = rcv
	st.PeakReserved = lim.Peak()
	return st, nil
}
