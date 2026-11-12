package netsec

import (
	"sort"
	"sync"
	"time"
)

// Barrier gates model publication for a privacy host. A training task fixes its
// input manifest and the current deletion generation at start time, and every
// asynchronous training callback must pass through Publish before its model can
// be served. A model is admitted only when every deletion known at publish time
// is already reflected in the task.
type Barrier struct {
	mu      sync.Mutex
	deletes *DeletionLog
	tasks   map[string]*TrainingTask
	ledger  []RejectedModel
	clock   func() time.Time
}

// NewBarrier creates a publication barrier backed by the given deletion log.
func NewBarrier(deletes *DeletionLog, opts ...Option) *Barrier {
	config := defaultConfig()
	for _, opt := range opts {
		opt(config)
	}
	return &Barrier{
		deletes: deletes,
		tasks:   make(map[string]*TrainingTask),
		clock:   config.clock,
	}
}

// StartTask pins a new training task to the caller's manifest and the deletion
// generation observed now. The returned task is the only identity under which
// a later Publish will be accepted.
func (b *Barrier) StartTask(id string, manifest Manifest) (*TrainingTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.tasks[id]; exists {
		return nil, ErrTaskExists
	}
	task := &TrainingTask{
		id:             id,
		manifest:       manifest,
		manifestDigest: manifest.Digest(),
		pinnedGen:      b.deletes.CurrentGeneration(),
		state:          stateRunning,
	}
	b.tasks[id] = task
	return cloneTask(task), nil
}

// Cancel aborts a running task. A cancelled task rejects every subsequent
// publish attempt, including callbacks that were already in flight. Cancelling
// an unknown task returns ErrUnknownTask; cancelling a non-running task is a
// no-op so callers can cancel idempotently.
func (b *Barrier) Cancel(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	task, ok := b.tasks[id]
	if !ok {
		return ErrUnknownTask
	}
	if task.state == stateRunning {
		task.state = stateCancelled
	}
	return nil
}

// Rebuild retires oldTaskID and starts a fresh task under newTaskID, pinning
// the current manifest and deletion generation. This is the only way forward
// for a stale task: the old pinned contract cannot be patched or re-versioned,
// so training must run again against the post-deletion inputs.
func (b *Barrier) Rebuild(oldTaskID, newTaskID string, manifest Manifest) (*TrainingTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	old, ok := b.tasks[oldTaskID]
	if !ok {
		return nil, ErrUnknownTask
	}
	if _, exists := b.tasks[newTaskID]; exists {
		return nil, ErrTaskExists
	}
	old.state = stateRebuilt
	task := &TrainingTask{
		id:             newTaskID,
		manifest:       manifest,
		manifestDigest: manifest.Digest(),
		pinnedGen:      b.deletes.CurrentGeneration(),
		state:          stateRunning,
	}
	b.tasks[newTaskID] = task
	return cloneTask(task), nil
}

// Publish is the gate every asynchronous training callback calls before a model
// may be served. It never admits a model whose task is unknown, cancelled or
// terminal, whose inputs differ from the pinned manifest, or whose pinned
// generation predates an unincorporated deletion. Every refusal is appended to
// the rejection ledger as evidence.
func (b *Barrier) Publish(model Model) Decision {
	b.mu.Lock()
	defer b.mu.Unlock()

	current := b.deletes.CurrentGeneration()

	task, ok := b.tasks[model.TaskID]
	if !ok {
		return b.reject(model, Decision{
			Allowed:           false,
			Reason:            ReasonUnknownTask,
			CurrentGeneration: current,
		})
	}

	decision := Decision{
		Allowed:           false,
		PinnedGeneration:  task.pinnedGen,
		CurrentGeneration: current,
	}

	switch task.state {
	case stateCancelled:
		decision.Reason = ReasonTaskCancelled
		return b.reject(model, decision)
	case statePublished, stateRejected, stateRebuilt:
		decision.Reason = ReasonTaskTerminal
		return b.reject(model, decision)
	}

	received := NewManifest(model.Inputs...)
	if model.ManifestDigest != task.manifestDigest || received.Digest() != task.manifestDigest {
		decision.Reason = ReasonManifestMismatch
		return b.reject(model, decision)
	}

	missing := b.deletes.Since(task.pinnedGen)
	deletedInputs := b.deletedInputs(received)
	decision.MissingDeletions = cloneEvents(missing)
	decision.DeletedInputs = deletedInputs
	switch {
	case len(missing) > 0:
		// A task pinned before an unincorporated deletion is structurally
		// stale; any erased inputs are reported alongside as supporting
		// evidence. It must be rebuilt, never re-versioned.
		decision.Reason = ReasonStaleGeneration
		task.state = stateRejected
		return b.reject(model, decision)
	case len(deletedInputs) > 0:
		// Pinned at the current generation, but the manifest still names an
		// individual whose data was already erased.
		decision.Reason = ReasonDeletedInput
		task.state = stateRejected
		return b.reject(model, decision)
	}

	task.state = statePublished
	decision.Allowed = true
	return decision
}

// Rejected returns a copy of every model refused by the barrier, in the order
// the refusals happened. The ledger is append-only and survives task rebuilds,
// giving auditors a durable record of what was kept out of serving.
func (b *Barrier) Rejected() []RejectedModel {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]RejectedModel, len(b.ledger))
	for i, entry := range b.ledger {
		out[i] = cloneRejected(entry)
	}
	return out
}

// Task returns a copy of the named task's current state, or ErrUnknownTask.
func (b *Barrier) Task(id string) (TrainingTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	task, ok := b.tasks[id]
	if !ok {
		return TrainingTask{}, ErrUnknownTask
	}
	return *cloneTask(task), nil
}

func (b *Barrier) deletedInputs(manifest Manifest) []string {
	var hits []string
	for _, input := range manifest.Inputs() {
		if b.deletes.IsDeleted(input) {
			hits = append(hits, input)
		}
	}
	sort.Strings(hits)
	return hits
}

func (b *Barrier) reject(model Model, decision Decision) Decision {
	entry := RejectedModel{
		Model:             cloneModel(model),
		Reason:            decision.Reason,
		PinnedGeneration:  decision.PinnedGeneration,
		CurrentGeneration: decision.CurrentGeneration,
		MissingDeletions:  cloneEvents(decision.MissingDeletions),
		DeletedInputs:     cloneStrings(decision.DeletedInputs),
		RejectedAt:        b.clock(),
	}
	b.ledger = append(b.ledger, entry)
	return decision
}

func cloneTask(task *TrainingTask) *TrainingTask {
	copyTask := *task
	copyTask.manifest = NewManifest(task.manifest.Inputs()...)
	return &copyTask
}

func cloneModel(model Model) Model {
	return Model{
		TaskID:         model.TaskID,
		Version:        model.Version,
		ManifestDigest: model.ManifestDigest,
		Inputs:         cloneStrings(model.Inputs),
	}
}

func cloneEvents(events []DeletionEvent) []DeletionEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]DeletionEvent, len(events))
	copy(out, events)
	return out
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func cloneRejected(entry RejectedModel) RejectedModel {
	entry.Model = cloneModel(entry.Model)
	entry.MissingDeletions = cloneEvents(entry.MissingDeletions)
	entry.DeletedInputs = cloneStrings(entry.DeletedInputs)
	return entry
}
