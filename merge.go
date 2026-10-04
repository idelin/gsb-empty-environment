package extsort

import (
	"bufio"
	"container/heap"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrCrashed is returned by Merge when a CrashHook aborts the merge,
// simulating a crash. The merge is resumable: call Merge again.
var ErrCrashed = errors.New("extsort: simulated crash")

// mergeTask merges inputFiles into output; the plan is a deterministic
// function of (runs, fanIn, final output name) so a restarted process
// replays the exact same plan.
type mergeTask struct {
	id         string
	inputFiles []string
	output     string
}

func (t *mergeTask) checkpointPath(dir string) string {
	return filepath.Join(dir, t.output+".checkpoint.json")
}

func (t *mergeTask) manifestPath(dir string) string {
	return filepath.Join(dir, t.output+".manifest.json")
}

// planTasks lays out the merge tree. Levels of fanIn-way tasks reduce the
// runs until a single final task writes `output`.
func planTasks(runs []InputBinding, fanIn int, output string) []mergeTask {
	var tasks []mergeTask
	level := make([]string, len(runs))
	for i, r := range runs {
		level[i] = r.File
	}
	levelIdx := 0
	for len(level) > fanIn {
		var next []string
		for g := 0; g*fanIn < len(level); g++ {
			end := (g + 1) * fanIn
			if end > len(level) {
				end = len(level)
			}
			out := fmt.Sprintf("merge-L%02d-G%04d.bin", levelIdx, g)
			tasks = append(tasks, mergeTask{
				id:         fmt.Sprintf("L%02d-G%04d", levelIdx, g),
				inputFiles: append([]string(nil), level[g*fanIn:end]...),
				output:     out,
			})
			next = append(next, out)
		}
		level = next
		levelIdx++
	}
	tasks = append(tasks, mergeTask{id: "final", inputFiles: level, output: output})
	return tasks
}

// Merge consumes the runs recorded in runs.json (see GenerateRuns) and
// produces the final sorted output inside opts.Dir. It resumes from the
// last published checkpoint of the first incomplete task; completed tasks
// are skipped after re-verifying their output digests.
func Merge(output string, opts Options) (*InputBinding, error) {
	o := opts.withDefaults()
	mf, err := loadRunsManifest(o.Dir)
	if err != nil {
		return nil, err
	}
	tasks := planTasks(mf.Runs, o.FanIn, output)
	bindings := make(map[string]InputBinding, len(mf.Runs)+len(tasks))
	for _, r := range mf.Runs {
		bindings[r.File] = r
	}
	published := 0
	for _, t := range tasks {
		inputs := make([]InputBinding, len(t.inputFiles))
		for i, name := range t.inputFiles {
			b, ok := bindings[name]
			if !ok {
				return nil, fmt.Errorf("task %s: unknown input %s", t.id, name)
			}
			inputs[i] = b
		}
		b, err := executeTask(&o, &t, inputs, &published)
		if err != nil {
			return nil, err
		}
		bindings[t.output] = *b
	}
	final := bindings[output]
	return &final, nil
}

// executeTask runs one merge task to completion, resuming from its
// checkpoint when present. On success it writes <output>.manifest.json
// (the output binding) and removes the checkpoint file.
func executeTask(o *Options, t *mergeTask, inputs []InputBinding, published *int) (*InputBinding, error) {
	manifestPath := t.manifestPath(o.Dir)
	if _, err := os.Stat(manifestPath); err == nil {
		var done InputBinding
		if err := loadJSON(manifestPath, &done); err != nil {
			return nil, fmt.Errorf("task %s: load manifest: %w", t.id, err)
		}
		if err := verifyBinding(o.Dir, done); err != nil {
			return nil, fmt.Errorf("task %s: completed output failed verification: %w", t.id, err)
		}
		return &done, nil
	}

	outPath := filepath.Join(o.Dir, t.output)
	ckptPath := t.checkpointPath(o.Dir)

	var cursors []Cursor
	var outLen, outRecords int64
	outHash := sha256.New()

	out, err := countedOpenFile(outPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}

	if _, statErr := os.Stat(ckptPath); statErr == nil {
		// Resume: trust only what the checkpoint binds.
		var ck Checkpoint
		if err := loadJSON(ckptPath, &ck); err != nil {
			out.Close()
			return nil, fmt.Errorf("task %s: load checkpoint: %w", t.id, err)
		}
		if ck.Version != checkpointVersion {
			out.Close()
			return nil, fmt.Errorf("task %s: unsupported checkpoint version %d", t.id, ck.Version)
		}
		if len(ck.Inputs) != len(inputs) || len(ck.Cursors) != len(inputs) {
			out.Close()
			return nil, fmt.Errorf("task %s: checkpoint arity mismatch", t.id)
		}
		for i := range inputs {
			if !ck.Inputs[i].equal(inputs[i]) {
				out.Close()
				return nil, fmt.Errorf("task %s: checkpoint input binding mismatch for %s", t.id, inputs[i].File)
			}
			if err := verifyBinding(o.Dir, inputs[i]); err != nil {
				out.Close()
				return nil, fmt.Errorf("task %s: %w", t.id, err)
			}
		}
		if ck.Output.File != t.output {
			out.Close()
			return nil, fmt.Errorf("task %s: checkpoint output %q, want %q", t.id, ck.Output.File, t.output)
		}
		st, err := out.Stat()
		if err != nil {
			out.Close()
			return nil, err
		}
		if st.Size() < ck.Output.Length {
			out.Close()
			return nil, fmt.Errorf("task %s: output shorter than published prefix (%d < %d)", t.id, st.Size(), ck.Output.Length)
		}
		// Re-hash and verify the published prefix, then truncate away any
		// unsynced tail: buffered bytes past the checkpoint are never trusted.
		if _, err := out.Seek(0, io.SeekStart); err != nil {
			out.Close()
			return nil, err
		}
		if _, err := io.CopyN(outHash, out, ck.Output.Length); err != nil {
			out.Close()
			return nil, fmt.Errorf("task %s: read published prefix: %w", t.id, err)
		}
		if got := hex.EncodeToString(outHash.Sum(nil)); got != ck.Output.SHA256 {
			out.Close()
			return nil, fmt.Errorf("task %s: published prefix digest mismatch", t.id)
		}
		if err := out.Truncate(ck.Output.Length); err != nil {
			out.Close()
			return nil, err
		}
		cursors = ck.Cursors
		outLen = ck.Output.Length
		outRecords = ck.Output.Records
	} else {
		// Fresh start: discard any stale output left by an attempt that
		// crashed before its first checkpoint.
		if err := out.Truncate(0); err != nil {
			out.Close()
			return nil, err
		}
		cursors = make([]Cursor, len(inputs))
	}
	if _, err := out.Seek(outLen, io.SeekStart); err != nil {
		out.Close()
		return nil, err
	}

	// Open all inputs at their cursor positions.
	readers := make([]*segmentReader, len(inputs))
	defer func() {
		for _, r := range readers {
			if r != nil {
				r.close()
			}
		}
	}()
	for i, b := range inputs {
		r, err := openSegmentReader(filepath.Join(o.Dir, b.File), cursors[i])
		if err != nil {
			out.Close()
			return nil, fmt.Errorf("task %s: open input %s: %w", t.id, b.File, err)
		}
		readers[i] = r
	}

	bw := bufio.NewWriter(io.MultiWriter(out, outHash))

	publish := func() error {
		if err := bw.Flush(); err != nil {
			return err
		}
		if err := out.Sync(); err != nil {
			return err
		}
		ck := Checkpoint{
			Version: checkpointVersion,
			Inputs:  append([]InputBinding(nil), inputs...),
			Cursors: make([]Cursor, len(readers)),
			Output: OutputBinding{
				File:    t.output,
				Length:  outLen,
				Records: outRecords,
				SHA256:  hex.EncodeToString(outHash.Sum(nil)),
			},
		}
		copy(ck.Cursors, cursors)
		if err := saveJSONAtomic(ckptPath, &ck); err != nil {
			return err
		}
		*published++
		if o.CrashHook != nil {
			if err := o.CrashHook(CrashInfo{
				Task:         t.id,
				OutputFile:   outPath,
				Published:    *published,
				PublishedLen: outLen,
			}); err != nil {
				return err
			}
		}
		return nil
	}

	// k-way merge over (key, seq); the heap pops the smallest key and, for
	// equal keys, the smallest original sequence number: stable order.
	//
	// Cursor bookkeeping: decoders read ahead of the output (popped records
	// sit in the heap before being written), so a checkpoint must NOT bind
	// raw decoder positions. Each heap item carries the cursor just past
	// its own record; cursors[src] always tracks the last record actually
	// written to the output, which is exactly where a resume must restart.
	mh := &mergeHeap{}
	for i, r := range readers {
		rec, cur, err := r.next()
		if err == io.EOF {
			continue
		}
		if err != nil {
			out.Close()
			return nil, fmt.Errorf("task %s: read %s: %w", t.id, inputs[i].File, err)
		}
		heap.Push(mh, &heapItem{rec: rec, src: i, cur: cur})
	}
	for mh.Len() > 0 {
		it := heap.Pop(mh).(*heapItem)
		enc := encodeRecord(it.rec)
		if _, err := bw.Write(enc); err != nil {
			out.Close()
			return nil, err
		}
		outLen += int64(len(enc))
		outRecords++
		cursors[it.src] = it.cur
		rec, cur, err := readers[it.src].next()
		if err != nil && err != io.EOF {
			out.Close()
			return nil, fmt.Errorf("task %s: read %s: %w", t.id, inputs[it.src].File, err)
		}
		if err == nil {
			heap.Push(mh, &heapItem{rec: rec, src: it.src, cur: cur})
		}
		if outRecords%int64(o.CheckpointEvery) == 0 {
			if err := publish(); err != nil {
				out.Close()
				return nil, err
			}
		}
	}

	if err := bw.Flush(); err != nil {
		out.Close()
		return nil, err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return nil, err
	}
	if err := out.Close(); err != nil {
		return nil, err
	}
	for i, r := range readers {
		if r != nil {
			r.close()
			readers[i] = nil
		}
	}

	binding := InputBinding{
		File:    t.output,
		Size:    outLen,
		SHA256:  hex.EncodeToString(outHash.Sum(nil)),
		Records: outRecords,
	}
	if err := saveJSONAtomic(manifestPath, &binding); err != nil {
		return nil, err
	}
	if err := os.Remove(ckptPath); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := syncDir(o.Dir); err != nil {
		return nil, err
	}
	return &binding, nil
}

// segmentReader decodes records from one input segment starting at a
// cursor, tracking the exact byte offset for the next checkpoint.
type segmentReader struct {
	f   *trackedFile
	dec *recordDecoder
	cur Cursor
}

func openSegmentReader(path string, cur Cursor) (*segmentReader, error) {
	f, err := countedOpen(path)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(cur.Offset, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return &segmentReader{
		f:   f,
		dec: newRecordDecoder(f, cur.Offset),
		cur: cur,
	}, nil
}

// next decodes the next record and returns the cursor just past it.
func (s *segmentReader) next() (*Record, Cursor, error) {
	rec, err := s.dec.next()
	if err != nil {
		return nil, s.cur, err
	}
	s.cur.Offset = s.dec.position()
	s.cur.Records++
	return rec, s.cur, nil
}

func (s *segmentReader) close() error { return s.f.Close() }

// mergeHeap is a min-heap over (key, seq).
type mergeHeap []*heapItem

type heapItem struct {
	rec *Record
	src int
	cur Cursor // cursor just past rec in its source segment
}

func (h mergeHeap) Len() int { return len(h) }

func (h mergeHeap) Less(i, j int) bool { return h[i].rec.Less(h[j].rec) }

func (h mergeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *mergeHeap) Push(x any) { *h = append(*h, x.(*heapItem)) }

func (h *mergeHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}
