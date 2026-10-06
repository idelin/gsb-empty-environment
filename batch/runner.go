package batch

import (
	"context"
	"errors"
	"fmt"
)

// Result is a confirmed per-item output. InputDigest is the frozen digest
// the output was computed from; it never changes for the lifetime of the
// batch, even if the source file is replaced afterwards.
type Result struct {
	Path         string
	InputDigest  string
	Output       []byte
	OutputDigest string
}

// ItemReport describes the final state of one manifest item.
type ItemReport struct {
	Path   string
	Status Status
	Reason string
	Result *Result // non-nil only when Status == StatusDone
}

// Report is the outcome of a Start or Resume call, in processing order.
type Report struct {
	BatchID string
	Items   []ItemReport
}

// StatusOf returns the final status of a manifest path.
func (r *Report) StatusOf(path string) Status {
	for _, it := range r.Items {
		if it.Path == path {
			return it.Status
		}
	}
	return ""
}

// ResultOf returns the confirmed result of a done item, or nil.
func (r *Report) ResultOf(path string) *Result {
	for _, it := range r.Items {
		if it.Path == path {
			return it.Result
		}
	}
	return nil
}

// ProcessFunc computes the output for one item. content is the item's
// source, verified against the frozen digest immediately before the call.
// deps holds confirmed results of the item's dependencies from this batch
// only; results from other batches are never mixed in.
type ProcessFunc func(ctx context.Context, item Item, content []byte, deps map[string]Result) ([]byte, error)

// Runner executes batches against an FS, persisting checkpoints to a Store.
type Runner struct {
	fs      FS
	store   Store
	process ProcessFunc
}

func NewRunner(fs FS, store Store, process ProcessFunc) *Runner {
	return &Runner{fs: fs, store: store, process: process}
}

// Start freezes the manifest and current content digests into a new
// checkpoint, then runs the batch. Files not listed in the manifest are
// never picked up, even if they appear in the directory mid-batch. A new
// batch requires a fresh batchID; reusing one returns ErrBatchExists.
func (r *Runner) Start(ctx context.Context, batchID string, manifest []Item) (*Report, error) {
	order, err := validateManifest(manifest)
	if err != nil {
		return nil, err
	}
	if _, err := r.store.Load(batchID); err == nil {
		return nil, fmt.Errorf("%w: %q", ErrBatchExists, batchID)
	} else if !errors.Is(err, ErrBatchNotFound) {
		return nil, err
	}

	cp := &Checkpoint{BatchID: batchID, Order: order, Items: make(map[string]*ItemCheckpoint, len(manifest))}
	for _, it := range manifest {
		cp.Items[it.Path] = &ItemCheckpoint{
			Path:       it.Path,
			DependsOn:  append([]string(nil), it.DependsOn...),
			DepDigests: make(map[string]string, len(it.DependsOn)),
			Status:     StatusPending,
		}
	}

	// Freeze: pin every manifest item to its current content digest.
	// Items already missing are isolated from the start.
	for _, path := range order {
		ic := cp.Items[path]
		content, err := r.fs.ReadFile(path)
		if err != nil {
			ic.Status = StatusIsolated
			ic.Reason = "source missing at freeze"
			continue
		}
		ic.InputDigest = digestOf(content)
	}
	// Bind each item to the frozen digests of its dependencies.
	for _, path := range order {
		ic := cp.Items[path]
		for _, dep := range ic.DependsOn {
			ic.DepDigests[dep] = cp.Items[dep].InputDigest
		}
	}
	propagateIsolation(cp)
	if err := r.store.Save(cp); err != nil {
		return nil, fmt.Errorf("batch: saving freeze checkpoint: %w", err)
	}
	return r.run(ctx, cp)
}

// Resume reloads a checkpoint and continues the batch. Before any
// processing it confirms every stored output against its digest, and
// re-verifies that every unprocessed item's source still matches the
// frozen digest; items that were deleted or replaced are isolated along
// with their successors, while independent items keep going.
func (r *Runner) Resume(ctx context.Context, batchID string) (*Report, error) {
	cp, err := r.store.Load(batchID)
	if err != nil {
		return nil, err
	}

	// Output confirmation: stored outputs must re-hash to their digests.
	for _, path := range cp.Order {
		ic := cp.Items[path]
		if ic.Status == StatusDone && digestOf(ic.Output) != ic.OutputDigest {
			return nil, fmt.Errorf("%w: item %q", ErrCheckpointCorrupt, path)
		}
	}

	// Version check: unprocessed items must still match the frozen digest.
	for _, path := range cp.Order {
		ic := cp.Items[path]
		if ic.Status != StatusPending {
			continue
		}
		content, err := r.fs.ReadFile(path)
		if errors.Is(err, ErrNotExist) {
			isolate(cp, ic, "source deleted before resume")
			continue
		} else if err != nil {
			return nil, fmt.Errorf("batch: reading %q at resume: %w", path, err)
		}
		if digestOf(content) != ic.InputDigest {
			isolate(cp, ic, "source replaced before resume")
		}
	}
	propagateIsolation(cp)
	if err := r.store.Save(cp); err != nil {
		return nil, fmt.Errorf("batch: saving resume checkpoint: %w", err)
	}
	return r.run(ctx, cp)
}

// run processes every pending item whose dependencies are confirmed in
// this batch. A processor error aborts the run; the item stays pending
// and the batch can be resumed later.
func (r *Runner) run(ctx context.Context, cp *Checkpoint) (*Report, error) {
	for _, path := range cp.Order {
		ic := cp.Items[path]
		if ic.Status != StatusPending || !depsDone(cp, ic) {
			continue
		}
		if err := r.processItem(ctx, cp, ic); err != nil {
			return nil, err
		}
	}
	return buildReport(cp), nil
}

func (r *Runner) processItem(ctx context.Context, cp *Checkpoint, ic *ItemCheckpoint) error {
	// Version consistency: every dependency's confirmed result must be
	// bound to the digest frozen for it at batch start.
	deps := make(map[string]Result, len(ic.DependsOn))
	for _, dep := range ic.DependsOn {
		dc := cp.Items[dep]
		if dc.Status != StatusDone {
			continue // guarded by depsDone; defensive only
		}
		if dc.InputDigest != ic.DepDigests[dep] {
			isolate(cp, ic, fmt.Sprintf("dependency %q version mismatch", dep))
			propagateIsolation(cp)
			return r.store.Save(cp)
		}
		deps[dep] = Result{
			Path:         dep,
			InputDigest:  dc.InputDigest,
			Output:       dc.Output,
			OutputDigest: dc.OutputDigest,
		}
	}

	// Re-read and re-verify the source against the frozen digest so a
	// batch never mixes input versions for one item.
	content, err := r.fs.ReadFile(ic.Path)
	if errors.Is(err, ErrNotExist) {
		isolate(cp, ic, "source deleted during run")
		propagateIsolation(cp)
		return r.store.Save(cp)
	} else if err != nil {
		return fmt.Errorf("batch: reading %q: %w", ic.Path, err)
	}
	if digestOf(content) != ic.InputDigest {
		isolate(cp, ic, "source replaced during run")
		propagateIsolation(cp)
		return r.store.Save(cp)
	}

	output, err := r.process(ctx, Item{Path: ic.Path, DependsOn: ic.DependsOn}, content, deps)
	if err != nil {
		return fmt.Errorf("batch: processing %q: %w", ic.Path, err)
	}

	// Output confirmation cut point.
	ic.Output = output
	ic.OutputDigest = digestOf(output)
	ic.Status = StatusDone
	return r.store.Save(cp)
}

// isolate marks an item isolated and records why.
func isolate(cp *Checkpoint, ic *ItemCheckpoint, reason string) {
	ic.Status = StatusIsolated
	ic.Reason = reason
}

// propagateIsolation isolates every pending item that depends, directly
// or transitively, on an isolated item.
func propagateIsolation(cp *Checkpoint) {
	for {
		changed := false
		for _, path := range cp.Order {
			ic := cp.Items[path]
			if ic.Status != StatusPending {
				continue
			}
			for _, dep := range ic.DependsOn {
				if cp.Items[dep].Status == StatusIsolated {
					isolate(cp, ic, fmt.Sprintf("dependency %q isolated", dep))
					changed = true
					break
				}
			}
		}
		if !changed {
			return
		}
	}
}

func depsDone(cp *Checkpoint, ic *ItemCheckpoint) bool {
	for _, dep := range ic.DependsOn {
		if cp.Items[dep].Status != StatusDone {
			return false
		}
	}
	return true
}

func buildReport(cp *Checkpoint) *Report {
	rep := &Report{BatchID: cp.BatchID, Items: make([]ItemReport, 0, len(cp.Order))}
	for _, path := range cp.Order {
		ic := cp.Items[path]
		ir := ItemReport{Path: path, Status: ic.Status, Reason: ic.Reason}
		if ic.Status == StatusDone {
			ir.Result = &Result{
				Path:         path,
				InputDigest:  ic.InputDigest,
				Output:       append([]byte(nil), ic.Output...),
				OutputDigest: ic.OutputDigest,
			}
		}
		rep.Items = append(rep.Items, ir)
	}
	return rep
}
