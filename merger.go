package main

import (
	"bufio"
	"container/heap"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// errSimulatedCrash is returned at injected crash points. It mimics a process
// crash: only fsynced-and-checkpointed state survives.
var errSimulatedCrash = errors.New("simulated crash")

type Config struct {
	MemLimit int64    // max bytes of buffered record data (key bytes + per-record overhead)
	OutPath  string   // final merged output
	WorkDir  string   // run files + checkpoint
	Sources  []string // input files, one record per line, lines must end with '\n'

	// Fault injection (testing only):
	CrashAfter            int64 // crash after this many records are processed in this invocation (0 = off)
	CrashBeforeCheckpoint bool  // crash once after output fsync but before the checkpoint write (lost ack)
	Verbose               bool
}

type Merger struct {
	cfg       Config
	ck        *Checkpoint
	processed int64 // records processed in this invocation, for CrashAfter
	lostAck   bool  // CrashBeforeCheckpoint already fired
}

func NewMerger(cfg Config) *Merger { return &Merger{cfg: cfg} }

func (m *Merger) logf(format string, args ...interface{}) {
	if m.cfg.Verbose {
		fmt.Fprintf(os.Stderr, "[merger] "+format+"\n", args...)
	}
}

func (m *Merger) checkCrash() error {
	if m.cfg.CrashAfter > 0 && m.processed >= m.cfg.CrashAfter {
		return errSimulatedCrash
	}
	return nil
}

// Run executes the job from whatever the durable checkpoint says, and is
// idempotent: re-running after a crash (or after success) is safe.
func (m *Merger) Run() error {
	if err := os.MkdirAll(m.cfg.WorkDir, 0o755); err != nil {
		return err
	}
	ck, err := loadCheckpoint(m.cfg.WorkDir)
	if err != nil {
		return err
	}
	if ck == nil {
		ck = &Checkpoint{Phase: phaseRuns, Sources: append([]string(nil), m.cfg.Sources...)}
	} else if !equalStrings(ck.Sources, m.cfg.Sources) {
		return fmt.Errorf("source list does not match checkpoint (%v vs %v)", ck.Sources, m.cfg.Sources)
	}
	m.ck = ck
	if err := m.cleanOrphanRuns(); err != nil {
		return err
	}
	switch ck.Phase {
	case phaseRuns:
		if err := m.generateRuns(); err != nil {
			return err
		}
		return m.mergeRuns()
	case phaseMerge:
		return m.mergeRuns()
	case phaseDone:
		return m.checkDone()
	default:
		return fmt.Errorf("unknown checkpoint phase %q", ck.Phase)
	}
}

// cleanOrphanRuns removes run files that are not referenced by the checkpoint
// (e.g. a run whose write was interrupted before the checkpoint was saved).
func (m *Merger) cleanOrphanRuns() error {
	keep := map[string]bool{}
	for _, r := range m.ck.Runs {
		keep[r] = true
	}
	entries, err := os.ReadDir(m.cfg.WorkDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "run-") && strings.HasSuffix(name, ".run") && !keep[name] {
			m.logf("removing orphan run file %s", name)
			if err := os.Remove(filepath.Join(m.cfg.WorkDir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// generateRuns reads the sources from the checkpointed position and spills
// sorted runs to disk. The in-memory buffer is bounded by MemLimit bytes of
// record data; a single record larger than the limit gets its own run.
// A checkpoint is taken after every flushed run (buffer empty), so resuming
// never loses or duplicates records.
func (m *Merger) generateRuns() error {
	var buf []Record
	var bufBytes int64

	flush := func(srcIdx int, srcOff int64, seq int) error {
		if len(buf) == 0 {
			return nil
		}
		sort.Slice(buf, func(i, j int) bool { return lessRec(buf[i], buf[j]) })
		name := fmt.Sprintf("run-%06d.run", len(m.ck.Runs))
		if err := writeRunFile(filepath.Join(m.cfg.WorkDir, name), buf); err != nil {
			return err
		}
		m.ck.Runs = append(m.ck.Runs, name)
		m.ck.SrcIdx = srcIdx
		m.ck.SrcOffset = srcOff
		m.ck.Seq = seq
		if err := saveCheckpoint(m.cfg.WorkDir, m.ck); err != nil {
			return err
		}
		m.logf("flushed %s: %d records, resume at source %d offset %d", name, len(buf), srcIdx, srcOff)
		m.processed += int64(len(buf))
		buf = buf[:0]
		bufBytes = 0
		// Crash injection: only after the checkpoint is durable, so every
		// simulated crash in this phase still makes progress on resume.
		return m.checkCrash()
	}

	for i := m.ck.SrcIdx; i < len(m.cfg.Sources); i++ {
		srcOff, seq := int64(0), 0
		if i == m.ck.SrcIdx {
			srcOff, seq = m.ck.SrcOffset, m.ck.Seq
		}
		f, err := os.Open(m.cfg.Sources[i])
		if err != nil {
			return err
		}
		if _, err := f.Seek(srcOff, io.SeekStart); err != nil {
			f.Close()
			return err
		}
		r := bufio.NewReaderSize(f, 64*1024)
		for {
			line, rerr := r.ReadBytes('\n')
			if len(line) > 0 {
				key, ok := parseSourceLine(line)
				if !ok {
					m.logf("source %s: discarding truncated trailing record (%d bytes)", m.cfg.Sources[i], len(line))
				} else {
					if strings.ContainsAny(key, "\t\r") {
						f.Close()
						return fmt.Errorf("source %s: key contains tab/CR, unsupported by output format", m.cfg.Sources[i])
					}
					rec := Record{Key: key, Source: i, Seq: seq}
					if len(buf) > 0 && bufBytes+rec.bufferedBytes() > m.cfg.MemLimit {
						if err := flush(i, srcOff, seq); err != nil {
							f.Close()
							return err
						}
					}
					buf = append(buf, rec)
					bufBytes += rec.bufferedBytes()
					srcOff += int64(len(line))
					seq++
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				f.Close()
				return rerr
			}
		}
		f.Close()
		// Source finished. Only kept in memory; persisted at the next flush.
		m.ck.SrcIdx = i + 1
		m.ck.SrcOffset = 0
		m.ck.Seq = 0
	}

	if err := flush(m.ck.SrcIdx, 0, 0); err != nil {
		return err
	}

	// Transition to the merge phase: fresh empty output, all runs at offset 0.
	m.ck.Phase = phaseMerge
	m.ck.RunOffsets = make([]int64, len(m.ck.Runs))
	m.ck.OutOffset = 0
	m.ck.OutRecords = 0
	out, err := os.OpenFile(m.cfg.OutPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return saveCheckpoint(m.cfg.WorkDir, m.ck)
}

// mergeRuns k-way merges the runs into the output. Output is written in
// byte-bounded chunks; each chunk is fsynced and then checkpointed together
// with the per-run consumed offsets. On resume the output is truncated back
// to the last acknowledged offset, so unacknowledged bytes are rewritten
// (no loss, no duplicates even if an ack/checkpoint is lost).
func (m *Merger) mergeRuns() error {
	out, err := os.OpenFile(m.cfg.OutPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if err := out.Truncate(m.ck.OutOffset); err != nil {
		return err
	}
	if _, err := out.Seek(m.ck.OutOffset, io.SeekStart); err != nil {
		return err
	}

	readers := make([]*runReader, len(m.ck.Runs))
	defer func() {
		for _, rr := range readers {
			if rr != nil {
				rr.close()
			}
		}
	}()
	h := &mergeHeap{}
	for i, name := range m.ck.Runs {
		rr, err := openRunReader(filepath.Join(m.cfg.WorkDir, name), m.ck.RunOffsets[i])
		if err != nil {
			return err
		}
		readers[i] = rr
		rec, end, err := rr.next()
		if err == io.EOF {
			continue
		}
		if err != nil {
			return err
		}
		heap.Push(h, heapItem{rec: rec, run: i, endOff: end})
	}

	chunkLimit := m.cfg.MemLimit / 2
	if chunkLimit < 1 {
		chunkLimit = 1
	}
	var outBuf []byte
	var outBytes int64
	runOffs := append([]int64(nil), m.ck.RunOffsets...)

	flushOut := func() error {
		if len(outBuf) == 0 {
			return nil
		}
		if _, err := out.Write(outBuf); err != nil {
			return err
		}
		if err := out.Sync(); err != nil {
			return err
		}
		m.ck.OutOffset += int64(len(outBuf))
		m.ck.RunOffsets = append([]int64(nil), runOffs...)
		if m.cfg.CrashBeforeCheckpoint && !m.lostAck {
			// Lost-ack injection: output is durable but the checkpoint (the
			// acknowledgement) is not. Resume must truncate and rewrite.
			m.lostAck = true
			return errSimulatedCrash
		}
		if err := saveCheckpoint(m.cfg.WorkDir, m.ck); err != nil {
			return err
		}
		m.logf("checkpoint: %d output bytes acked", m.ck.OutOffset)
		outBuf = outBuf[:0]
		outBytes = 0
		return nil
	}

	for h.Len() > 0 {
		it := heap.Pop(h).(heapItem)
		line := fmt.Sprintf("%s\t%d\t%d\n", it.rec.Key, it.rec.Source, it.rec.Seq)
		outBuf = append(outBuf, line...)
		outBytes += int64(len(line))
		m.ck.OutRecords++
		runOffs[it.run] = it.endOff
		rec, end, err := readers[it.run].next()
		if err != nil && err != io.EOF {
			return err
		}
		if err == nil {
			heap.Push(h, heapItem{rec: rec, run: it.run, endOff: end})
		}
		m.processed++
		if err := m.checkCrash(); err != nil {
			return err
		}
		if outBytes >= chunkLimit {
			if err := flushOut(); err != nil {
				return err
			}
		}
	}
	if err := flushOut(); err != nil {
		return err
	}

	m.ck.Phase = phaseDone
	if err := saveCheckpoint(m.cfg.WorkDir, m.ck); err != nil {
		return err
	}
	for _, name := range m.ck.Runs {
		os.Remove(filepath.Join(m.cfg.WorkDir, name))
	}
	m.ck.Runs = nil
	m.ck.RunOffsets = nil
	if err := saveCheckpoint(m.cfg.WorkDir, m.ck); err != nil {
		return err
	}
	m.logf("done: %d records, %d output bytes", m.ck.OutRecords, m.ck.OutOffset)
	return nil
}

func (m *Merger) checkDone() error {
	st, err := os.Stat(m.cfg.OutPath)
	if err != nil {
		return err
	}
	if st.Size() != m.ck.OutOffset {
		return fmt.Errorf("checkpoint says done but output size %d != acknowledged %d", st.Size(), m.ck.OutOffset)
	}
	// Clean up any run files left by a crash just after the done checkpoint.
	for _, name := range m.ck.Runs {
		os.Remove(filepath.Join(m.cfg.WorkDir, name))
	}
	if len(m.ck.Runs) > 0 {
		m.ck.Runs = nil
		m.ck.RunOffsets = nil
		if err := saveCheckpoint(m.cfg.WorkDir, m.ck); err != nil {
			return err
		}
	}
	m.logf("already complete: %d records, %d output bytes", m.ck.OutRecords, m.ck.OutOffset)
	return nil
}

// heapItem is one buffered record per run during the k-way merge. endOff is
// the run-file offset just past this record; it becomes the checkpointed
// offset for that run once the record is emitted in a flushed chunk.
type heapItem struct {
	rec    Record
	run    int
	endOff int64
}

type mergeHeap []heapItem

func (h mergeHeap) Len() int           { return len(h) }
func (h mergeHeap) Less(i, j int) bool { return lessRec(h[i].rec, h[j].rec) }
func (h mergeHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *mergeHeap) Push(x interface{}) { *h = append(*h, x.(heapItem)) }

func (h *mergeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
