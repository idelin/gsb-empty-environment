package batch

import (
	"context"
	"fmt"
)

// Item is a frozen input handed to the Processor.
type Item struct {
	Path    string
	Digest  string
	Content []byte
}

// DepResult is the confirmed same-batch result of one dependency.
type DepResult struct {
	Path   string
	Digest string // input digest the dependency result was computed from
	Output []byte
}

// Processor computes the output for one item. It must be deterministic for
// the full-recompute guarantee to hold.
type Processor func(ctx context.Context, item Item, deps []DepResult) ([]byte, error)

// Options configures a Runner.
type Options struct {
	SrcDir   string              // directory with input files
	OutDir   string              // directory receiving outputs
	StateDir string              // directory holding the checkpoint
	Deps     map[string][]string // explicit inter-file dependencies
	Process  Processor
	// AfterPersist is a fault-injection hook invoked after every checkpoint
	// persist with the new sequence number. Returning an error aborts Run,
	// simulating a crash at that exact cut point.
	AfterPersist func(seq int) error
}

// Runner executes one batch at a time over a virtual directory.
type Runner struct {
	fs      FS
	opts    Options
	cp      *Checkpoint
	ignored []string          // new files not auto-included in this batch
	outputs map[string][]byte // confirmed outputs produced/loaded this run
}

// Open resumes the persisted batch when a checkpoint exists, otherwise it
// freezes a fresh manifest and starts batch 1.
func Open(fs FS, opts Options) (*Runner, error) {
	if opts.Process == nil {
		return nil, fmt.Errorf("batch: Options.Process is required")
	}
	r := &Runner{fs: fs, opts: opts, outputs: make(map[string][]byte)}
	cp, err := loadCheckpoint(fs, opts.StateDir)
	if err != nil {
		return nil, err
	}
	if cp == nil {
		m, err := FreezeManifest(fs, opts.SrcDir, opts.Deps)
		if err != nil {
			return nil, err
		}
		cp = &Checkpoint{
			BatchID:  1,
			SrcDir:   opts.SrcDir,
			Manifest: m,
			Items:    make(map[string]*ItemRecord, len(m.Entries)),
		}
		for _, p := range m.Order {
			cp.Items[p] = &ItemRecord{Status: StatusPending}
		}
		r.cp = cp
		if err := r.persist(); err != nil {
			return nil, err
		}
		return r, nil
	}
	if cp.SrcDir != opts.SrcDir {
		return nil, fmt.Errorf("batch: checkpoint src dir %q != configured %q", cp.SrcDir, opts.SrcDir)
	}
	r.cp = cp
	if err := r.verify(); err != nil {
		return nil, err
	}
	if err := r.persist(); err != nil {
		return nil, err
	}
	return r, nil
}

// NewBatch explicitly starts a fresh batch: the manifest is re-frozen from
// the current source directory (previously ignored new files are included
// now) and every item is pending again. Results from earlier batches are
// never reused.
func (r *Runner) NewBatch() error {
	m, err := FreezeManifest(r.fs, r.opts.SrcDir, r.opts.Deps)
	if err != nil {
		return err
	}
	cp := &Checkpoint{
		BatchID:  r.cp.BatchID + 1,
		SrcDir:   r.opts.SrcDir,
		Manifest: m,
		Items:    make(map[string]*ItemRecord, len(m.Entries)),
		Seq:      r.cp.Seq,
	}
	for _, p := range m.Order {
		cp.Items[p] = &ItemRecord{Status: StatusPending}
	}
	r.cp = cp
	r.outputs = make(map[string][]byte)
	r.ignored = nil
	return r.persist()
}

// BatchID returns the current batch identifier.
func (r *Runner) BatchID() int { return r.cp.BatchID }

// Record returns a copy of the checkpoint record for path.
func (r *Runner) Record(path string) (ItemRecord, bool) {
	rec, ok := r.cp.Items[path]
	if !ok {
		return ItemRecord{}, false
	}
	return *rec, true
}

// Ignored returns files present in the source directory but not part of the
// frozen manifest.
func (r *Runner) Ignored() []string { return append([]string(nil), r.ignored...) }

// Summary reports what a Run did.
type Summary struct {
	BatchID   int
	Processed []string          // items processed in this Run call
	Done      []string          // items already confirmed before this Run
	Isolated  map[string]string // path -> reason
	Ignored   []string          // new files not auto-included
}

// verify reconciles the persisted checkpoint with the current source and
// output directories on restart.
func (r *Runner) verify() error {
	cp := r.cp
	// 1. Confirm recorded outputs; revert broken confirmations to pending so
	//    they are recomputed. Topo order makes a single pass sufficient.
	for _, p := range cp.Manifest.Order {
		rec := cp.Items[p]
		if rec.Status != StatusDone {
			continue
		}
		ok := rec.InputDigest == cp.Manifest.Entries[p].Digest
		if ok {
			data, err := r.fs.ReadFile(joinPath(r.opts.OutDir, p))
			ok = err == nil && DigestOf(data) == rec.OutputDigest
		}
		if ok {
			for dep, dg := range rec.DepDigests {
				depRec := cp.Items[dep]
				if depRec.Status != StatusDone || depRec.InputDigest != dg {
					ok = false
					break
				}
			}
		}
		if !ok {
			rec.Status = StatusPending
			rec.InputDigest = ""
			rec.OutputDigest = ""
			rec.DepDigests = nil
		}
	}
	// 2. Compare pending items against the frozen manifest. Files that
	//    appeared after the freeze are ignored, never auto-included.
	paths, err := r.fs.List(cp.SrcDir)
	if err != nil {
		return fmt.Errorf("batch: list %q: %v", cp.SrcDir, err)
	}
	current := make(map[string]bool, len(paths))
	for _, p := range paths {
		current[p] = true
		if _, frozen := cp.Manifest.Entries[p]; !frozen {
			r.ignored = append(r.ignored, p)
		}
	}
	for _, p := range cp.Manifest.Order {
		if cp.Items[p].Status != StatusPending {
			continue
		}
		if !current[p] {
			r.isolate(p, "source deleted")
			continue
		}
		data, err := r.fs.ReadFile(joinPath(cp.SrcDir, p))
		if err != nil {
			return fmt.Errorf("batch: read %q: %v", p, err)
		}
		if DigestOf(data) != cp.Manifest.Entries[p].Digest {
			r.isolate(p, "source modified")
		}
	}
	return nil
}

// isolate marks path isolated and cascades to its pending transitive
// dependents. Done items keep their results: they are bound to the original
// frozen digests they were computed from.
func (r *Runner) isolate(path, reason string) {
	rec := r.cp.Items[path]
	if rec.Status == StatusDone {
		return
	}
	rec.Status = StatusIsolated
	rec.Reason = reason
	for dep := range r.cp.Manifest.Dependents(path) {
		drec := r.cp.Items[dep]
		if drec.Status == StatusPending {
			drec.Status = StatusIsolated
			drec.Reason = fmt.Sprintf("dependency %q isolated", path)
		}
	}
}

func (r *Runner) persist() error {
	if err := saveCheckpoint(r.fs, r.opts.StateDir, r.cp); err != nil {
		return err
	}
	if r.opts.AfterPersist != nil {
		return r.opts.AfterPersist(r.cp.Seq)
	}
	return nil
}

// Run processes every pending item whose same-batch dependencies are all
// confirmed, in frozen topological order. Each item's content digest is
// re-checked immediately before processing so a single batch never mixes
// input versions.
func (r *Runner) Run(ctx context.Context) (*Summary, error) {
	cp := r.cp
	sum := &Summary{
		BatchID:  cp.BatchID,
		Isolated: make(map[string]string),
		Ignored:  r.Ignored(),
	}
	for _, p := range cp.Manifest.Order {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		rec := cp.Items[p]
		switch rec.Status {
		case StatusDone:
			if _, ok := r.outputs[p]; !ok {
				data, err := r.fs.ReadFile(joinPath(r.opts.OutDir, p))
				if err == nil && DigestOf(data) == rec.OutputDigest {
					r.outputs[p] = data
				}
			}
			sum.Done = append(sum.Done, p)
			continue
		case StatusIsolated:
			sum.Isolated[p] = rec.Reason
			continue
		}
		// Re-check the frozen version right before use.
		data, err := r.fs.ReadFile(joinPath(cp.SrcDir, p))
		if err != nil || DigestOf(data) != cp.Manifest.Entries[p].Digest {
			if err != nil {
				r.isolate(p, "source deleted")
			} else {
				r.isolate(p, "source modified")
			}
			if perr := r.persist(); perr != nil {
				return sum, perr
			}
			sum.Isolated[p] = cp.Items[p].Reason
			continue
		}
		entry := cp.Manifest.Entries[p]
		deps := make([]DepResult, 0, len(entry.Deps))
		for _, d := range entry.Deps {
			drec := cp.Items[d]
			if drec.Status != StatusDone {
				// A dependency regressed mid-run; isolate rather than mix.
				r.isolate(p, fmt.Sprintf("dependency %q not confirmed", d))
				sum.Isolated[p] = cp.Items[p].Reason
				continue
			}
			out, ok := r.outputs[d]
			if !ok {
				raw, rerr := r.fs.ReadFile(joinPath(r.opts.OutDir, d))
				if rerr != nil || DigestOf(raw) != drec.OutputDigest {
					return sum, fmt.Errorf("batch: confirmed output for %q unavailable", d)
				}
				out = raw
				r.outputs[d] = raw
			}
			deps = append(deps, DepResult{Path: d, Digest: drec.InputDigest, Output: out})
		}
		if cp.Items[p].Status != StatusPending {
			continue
		}
		out, err := r.opts.Process(ctx, Item{Path: p, Digest: entry.Digest, Content: data}, deps)
		if err != nil {
			return sum, fmt.Errorf("batch: process %q: %v", p, err)
		}
		if err := r.fs.WriteFile(joinPath(r.opts.OutDir, p), out); err != nil {
			return sum, fmt.Errorf("batch: write output for %q: %v", p, err)
		}
		rec.Status = StatusDone
		rec.InputDigest = entry.Digest
		rec.OutputDigest = DigestOf(out)
		rec.DepDigests = make(map[string]string, len(entry.Deps))
		for _, d := range entry.Deps {
			rec.DepDigests[d] = cp.Manifest.Entries[d].Digest
		}
		if err := r.persist(); err != nil {
			return sum, err
		}
		r.outputs[p] = out
		sum.Processed = append(sum.Processed, p)
	}
	return sum, nil
}
