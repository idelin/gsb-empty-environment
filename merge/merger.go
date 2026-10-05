package merge

import (
	"container/heap"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Phase identifies a fault-injection point in the merge loop.
type Phase int

const (
	// PhaseAfterOutputSync fires after output bytes are flushed and fsynced
	// but before the checkpoint acknowledging them is written. A crash here
	// simulates output-acknowledgement loss: the output file holds bytes no
	// checkpoint confirms.
	PhaseAfterOutputSync Phase = iota
	// PhaseAfterCheckpoint fires right after a checkpoint becomes durable.
	PhaseAfterCheckpoint
)

// ErrCrashed can be returned by a Hook to simulate a process crash: Run
// aborts immediately, leaving files exactly as a crash would.
var ErrCrashed = errors.New("simulated crash")

// ErrDone is returned by Run when the checkpoint already marks the merge
// complete; re-running is a no-op and never appends duplicate output.
var ErrDone = errors.New("merge already complete")

// Options configures a Merger.
type Options struct {
	Inputs          []string
	OutputPath      string
	CheckpointPath  string // empty disables checkpointing
	MemLimit        int64  // soft cap on buffered bytes; a single record may exceed it
	BlockSize       int    // normal I/O block size in bytes (default 64KiB)
	CheckpointEvery int    // emitted records between checkpoints (default 1024)
	Resume          bool   // resume from CheckpointPath if one exists
	// Hook, if set, is called at fault-injection phases. Returning an error
	// (e.g. ErrCrashed) aborts the merge, leaving files as a crash would.
	Hook func(Phase) error
}

// runReader is one sorted input run. At most one record per run is pending
// (parsed and waiting in the heap) at any time.
type runReader struct {
	f           *os.File
	buf         []byte // unparsed bytes; bufOff is the file offset of buf[0]
	bufOff      int64
	eof         bool
	truncated   bool // a partial trailing record was discarded
	hasPending  bool
	pending     Record
	pendingSize int64 // framed size of pending
	pendingEnd  int64 // file offset just past pending
	consumedOff int64 // file offset just past the last emitted record
	recordsOut  uint64
}

type heapItem struct {
	rec  Record
	src  int
	size int64 // framed size, for memory accounting
	end  int64 // file offset just past this record in its source
}

type recHeap []heapItem

func (h recHeap) Len() int            { return len(h) }
func (h recHeap) Less(i, j int) bool  { return Less(h[i].rec, h[j].rec) }
func (h recHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *recHeap) Push(x interface{}) { *h = append(*h, x.(heapItem)) }
func (h *recHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = heapItem{}
	*h = old[:n-1]
	return it
}

// Merger performs a resumable k-way merge under a byte-counted memory limit.
type Merger struct {
	opts    Options
	sources []*runReader
	inHeap  []bool // source i currently has an item in the heap
	out     *os.File
	outBuf  []byte
	outOff  int64 // bytes written to out (confirmed after fsync+checkpoint)
	outRecs uint64
	mem     int64
	// PeakMem is the high-water mark of buffered bytes, for tests/metrics.
	PeakMem int64
	hp      recHeap
	sinceCk int
	done    bool
}

// NewMerger opens inputs and output. With Resume and an existing checkpoint,
// the output is truncated to the last confirmed offset (dropping unacked
// bytes) and inputs are positioned at their confirmed offsets.
func NewMerger(opts Options) (*Merger, error) {
	if opts.BlockSize <= 0 {
		opts.BlockSize = 64 << 10
	}
	if opts.MemLimit <= 0 {
		opts.MemLimit = 16 << 20
	}
	if opts.CheckpointEvery <= 0 {
		opts.CheckpointEvery = 1024
	}
	m := &Merger{opts: opts}
	var ck *Checkpoint
	if opts.Resume && opts.CheckpointPath != "" {
		var err error
		ck, err = LoadCheckpoint(opts.CheckpointPath)
		if err != nil {
			return nil, err
		}
	}
	if ck != nil && len(ck.InputOffsets) != len(opts.Inputs) {
		return nil, fmt.Errorf("checkpoint has %d inputs, want %d", len(ck.InputOffsets), len(opts.Inputs))
	}
	var startOff int64
	if ck != nil {
		startOff = ck.OutputOffset
		m.outRecs = ck.OutputRecords
		m.done = ck.Done
	}
	out, err := os.OpenFile(opts.OutputPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	// Drop output bytes that no checkpoint ever acknowledged.
	if err := out.Truncate(startOff); err != nil {
		out.Close()
		return nil, err
	}
	if _, err := out.Seek(startOff, io.SeekStart); err != nil {
		out.Close()
		return nil, err
	}
	m.out = out
	m.outOff = startOff
	for i, in := range opts.Inputs {
		f, err := os.Open(in)
		if err != nil {
			m.Close()
			return nil, err
		}
		var off int64
		if ck != nil {
			off = ck.InputOffsets[i]
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			m.Close()
			return nil, err
		}
		sr := &runReader{f: f, bufOff: off, consumedOff: off}
		if ck != nil {
			sr.recordsOut = ck.InputRecords[i]
			if ck.Truncated != nil {
				sr.truncated = ck.Truncated[i]
			}
		}
		m.sources = append(m.sources, sr)
	}
	m.inHeap = make([]bool, len(m.sources))
	return m, nil
}

// Close releases all file handles.
func (m *Merger) Close() error {
	for _, s := range m.sources {
		if s.f != nil {
			s.f.Close()
		}
	}
	if m.out != nil {
		return m.out.Close()
	}
	return nil
}

// Done reports whether the merge has completed (and been checkpointed).
func (m *Merger) Done() bool { return m.done }

// Truncated reports whether source i had a partial trailing record that was
// discarded.
func (m *Merger) Truncated(i int) bool { return m.sources[i].truncated }

func (m *Merger) addMem(delta int64) {
	m.mem += delta
	if m.mem > m.PeakMem {
		m.PeakMem = m.mem
	}
}

// readChunk appends up to n bytes from the file to the source buffer.
func (m *Merger) readChunk(s *runReader, n int64) error {
	if n <= 0 {
		return nil
	}
	tmp := make([]byte, n)
	read := 0
	for read < len(tmp) {
		k, err := s.f.Read(tmp[read:])
		read += k
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.eof = true
				break
			}
			return err
		}
	}
	if read > 0 {
		s.buf = append(s.buf, tmp[:read]...)
		m.addMem(int64(read))
	}
	return nil
}

// ensurePending makes the next record of s pending. Reads are sized by the
// memory budget, but completing the current record is mandatory: once the
// frame length is known, exactly the remaining bytes are read even if that
// exceeds the budget. This is what lets a record be larger than a block (or
// than the whole limit) while the limit still bounds read-ahead buffering.
func (m *Merger) ensurePending(s *runReader) error {
	for !s.hasPending {
		rec, n, err := DecodeRecord(s.buf)
		if err == nil {
			s.hasPending = true
			s.pending = rec
			s.pendingSize = int64(n)
			s.pendingEnd = s.bufOff + int64(n)
			s.buf = s.buf[n:]
			s.bufOff += int64(n)
			// Bytes moved from raw buffer to pending: net memory unchanged.
			return nil
		}
		if !errors.Is(err, ErrIncomplete) {
			return fmt.Errorf("decoding %s: %w", s.f.Name(), err)
		}
		if s.eof {
			// Truncated input: a partial trailing record is discarded.
			s.truncated = true
			m.addMem(-int64(len(s.buf)))
			s.buf = nil
			return nil
		}
		// Bytes still needed to complete the current record.
		needed := int64(16) // frame itself incomplete: frame is <= 10 bytes
		if bodyLen, fn := binary.Uvarint(s.buf); fn > 0 {
			needed = int64(fn) + int64(bodyLen) - int64(len(s.buf))
			if needed <= 0 {
				needed = 1
			}
		}
		chunk := int64(m.opts.BlockSize)
		if budget := m.opts.MemLimit - m.mem; budget < chunk {
			chunk = budget
		}
		if chunk < needed {
			chunk = needed // mandatory: finish the current record
		}
		if err := m.readChunk(s, chunk); err != nil {
			return err
		}
	}
	return nil
}

// Run merges to completion or until a Hook aborts it. It is safe to create a
// fresh Merger with Resume afterwards and call Run again.
func (m *Merger) Run() error {
	if m.done {
		return ErrDone
	}
	for {
		// Refill invariant: before any pop, every source with remaining
		// data has exactly its next record in the heap.
		for i, s := range m.sources {
			if m.inHeap[i] {
				continue
			}
			if err := m.ensurePending(s); err != nil {
				return err
			}
			if s.hasPending {
				heap.Push(&m.hp, heapItem{rec: s.pending, src: i, size: s.pendingSize, end: s.pendingEnd})
				s.hasPending = false
				m.inHeap[i] = true
			}
		}
		if len(m.hp) == 0 {
			break // all inputs fully consumed
		}
		it := heap.Pop(&m.hp).(heapItem)
		m.inHeap[it.src] = false
		s := m.sources[it.src]
		s.consumedOff = it.end
		s.recordsOut++
		m.addMem(-it.size)
		m.outBuf = AppendRecord(m.outBuf, it.rec)
		m.addMem(int64(it.size)) // record now lives in the output buffer
		m.outRecs++
		if len(m.outBuf) >= m.opts.BlockSize {
			if err := m.flushOut(false); err != nil {
				return err
			}
		}
		m.sinceCk++
		if m.sinceCk >= m.opts.CheckpointEvery {
			if err := m.checkpoint(); err != nil {
				return err
			}
		}
	}
	if err := m.flushOut(true); err != nil {
		return err
	}
	m.done = true
	return m.writeCheckpoint()
}

// flushOut writes the output buffer; with sync it also fsyncs the file.
func (m *Merger) flushOut(sync bool) error {
	if len(m.outBuf) > 0 {
		if _, err := m.out.Write(m.outBuf); err != nil {
			return err
		}
		m.outOff += int64(len(m.outBuf))
		m.addMem(-int64(len(m.outBuf)))
		m.outBuf = m.outBuf[:0]
	}
	if sync {
		return m.out.Sync()
	}
	return nil
}

// checkpoint makes emitted output durable and then acknowledges it. The
// ordering is what makes resume exactly-once: output first, ack second.
func (m *Merger) checkpoint() error {
	if err := m.flushOut(true); err != nil {
		return err
	}
	if m.opts.Hook != nil {
		if err := m.opts.Hook(PhaseAfterOutputSync); err != nil {
			return err
		}
	}
	if err := m.writeCheckpoint(); err != nil {
		return err
	}
	m.sinceCk = 0
	if m.opts.Hook != nil {
		if err := m.opts.Hook(PhaseAfterCheckpoint); err != nil {
			return err
		}
	}
	return nil
}

func (m *Merger) writeCheckpoint() error {
	if m.opts.CheckpointPath == "" {
		return nil
	}
	ck := &Checkpoint{
		OutputOffset:  m.outOff,
		OutputRecords: m.outRecs,
		InputOffsets:  make([]int64, len(m.sources)),
		InputRecords:  make([]uint64, len(m.sources)),
		Truncated:     make([]bool, len(m.sources)),
		Done:          m.done,
	}
	for i, s := range m.sources {
		ck.InputOffsets[i] = s.consumedOff
		ck.InputRecords[i] = s.recordsOut
		ck.Truncated[i] = s.truncated
	}
	return SaveCheckpoint(m.opts.CheckpointPath, ck)
}
