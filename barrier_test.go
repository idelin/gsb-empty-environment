package netsec_test

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netsec "example.org/netsec/q0444"
)

// fakeClock hands out strictly increasing timestamps so rejection evidence has
// deterministic, ordered RejectedAt values without wall-clock flakiness.
type fakeClock struct {
	tick atomic.Int64
}

func (c *fakeClock) now() time.Time {
	n := c.tick.Add(1)
	return time.Unix(n, 0)
}

// pausableTrainer is a test double for an asynchronous training job. After
// startRun it performs its first training step and then blocks at a
// controllable gate. The test can delete data, cancel, or publish another model
// while the trainer is paused, then resume to release the callback.
type pausableTrainer struct {
	ready    chan struct{}
	release  chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newPausableTrainer() *pausableTrainer {
	return &pausableTrainer{
		ready:   make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// step1 performs the first training step and then blocks until resume.
func (t *pausableTrainer) step1(ctx context.Context) error {
	close(t.ready)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.release:
		return nil
	}
}

// pause waits until the trainer has reached the blocking gate.
func (t *pausableTrainer) pause() {
	<-t.ready
}

// resume releases the blocked training callback.
func (t *pausableTrainer) resume() {
	t.stopOnce.Do(func() { close(t.release) })
}

// wait blocks until the training goroutine has fully returned.
func (t *pausableTrainer) wait() {
	<-t.done
}

// startRun launches a task: the trainer runs to its pause point, after which
// the host goroutine publishes the resulting model through the barrier. The
// returned channel carries the publish decision once the callback runs.
func startRun(t *testing.T, barrier *netsec.Barrier, trainer *pausableTrainer,
	taskID string, manifest netsec.Manifest, version string,
) (context.Context, context.CancelFunc, chan netsec.Decision) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	decisions := make(chan netsec.Decision, 1)

	go func() {
		defer close(trainer.done)
		if err := trainer.step1(ctx); err != nil {
			return
		}
		decisions <- barrier.Publish(netsec.Model{
			TaskID:         taskID,
			Version:        version,
			ManifestDigest: manifest.Digest(),
			Inputs:         manifest.Inputs(),
		})
	}()

	trainer.pause()
	return ctx, cancel, decisions
}

func newTestBarrier() (*netsec.Barrier, *netsec.DeletionLog, *fakeClock) {
	clock := &fakeClock{}
	option := netsec.WithClock(clock.now)
	deletes := netsec.NewDeletionLog(option)
	return netsec.NewBarrier(deletes, option), deletes, clock
}

// TestPublishAdmitsCleanModel establishes the happy path: no deletions means a
// task pinned at generation zero publishes once and leaves no evidence.
func TestPublishAdmitsCleanModel(t *testing.T) {
	barrier, deletes, _ := newTestBarrier()
	manifest := netsec.NewManifest("ind-1", "ind-2")

	task, err := barrier.StartTask("task-a", manifest)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if task.PinnedGeneration() != 0 || task.PinnedGeneration() != deletes.CurrentGeneration() {
		t.Fatalf("pinned generation = %d, want 0", task.PinnedGeneration())
	}

	decision := barrier.Publish(netsec.Model{
		TaskID:         task.ID(),
		Version:        "v1",
		ManifestDigest: manifest.Digest(),
		Inputs:         manifest.Inputs(),
	})
	if !decision.Allowed {
		t.Fatalf("clean model rejected: %+v", decision)
	}
	if len(barrier.Rejected()) != 0 {
		t.Fatalf("clean publish left rejection evidence: %+v", barrier.Rejected())
	}
}

// TestDeletionBeforeStartRejectsAndRebuildRecovers covers an individual already
// gone when the task starts. The stale model is refused, a version bump cannot
// rescue it, and only a rebuild against the post-deletion inputs publishes.
func TestDeletionBeforeStartRejectsAndRebuildRecovers(t *testing.T) {
	barrier, deletes, _ := newTestBarrier()
	event := deletes.Record("ind-2")
	if event.Generation != 1 {
		t.Fatalf("first deletion generation = %d, want 1", event.Generation)
	}

	manifest := netsec.NewManifest("ind-1", "ind-2")
	task, err := barrier.StartTask("stale-task", manifest)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	model := netsec.Model{
		TaskID:         task.ID(),
		Version:        "v1",
		ManifestDigest: manifest.Digest(),
		Inputs:         manifest.Inputs(),
	}
	decision := barrier.Publish(model)
	if decision.Allowed || decision.Reason != netsec.ReasonDeletedInput {
		t.Fatalf("model on erased input decision = %+v, want rejection %s", decision, netsec.ReasonDeletedInput)
	}
	if decision.PinnedGeneration != 1 || decision.CurrentGeneration != 1 {
		t.Fatalf("generations = %+v, want pinned/current 1/1", decision)
	}
	if len(decision.DeletedInputs) != 1 || decision.DeletedInputs[0] != "ind-2" {
		t.Fatalf("deleted inputs = %+v, want [ind-2]", decision.DeletedInputs)
	}

	// Republishing the identical artifact under a new version must not pass.
	model.Version = "v2-attempt"
	again := barrier.Publish(model)
	if again.Allowed || again.Reason != netsec.ReasonTaskTerminal {
		t.Fatalf("re-versioned stale model decision = %+v, want %s", again, netsec.ReasonTaskTerminal)
	}

	rejected := barrier.Rejected()
	if len(rejected) != 2 {
		t.Fatalf("rejection ledger length = %d, want 2", len(rejected))
	}
	if rejected[0].Reason != netsec.ReasonDeletedInput ||
		len(rejected[0].DeletedInputs) != 1 ||
		rejected[0].DeletedInputs[0] != "ind-2" {
		t.Fatalf("first evidence entry wrong: %+v", rejected[0])
	}
	if rejected[1].Model.Version != "v2-attempt" {
		t.Fatalf("second evidence must retain the bumped version, got %+v", rejected[1])
	}

	// The only recovery is a rebuild over inputs that no longer contain ind-2.
	cleanManifest := netsec.NewManifest("ind-1")
	rebuilt, err := barrier.Rebuild("stale-task", "rebuilt-task", cleanManifest)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if rebuilt.PinnedGeneration() != 1 {
		t.Fatalf("rebuild pinned generation = %d, want 1", rebuilt.PinnedGeneration())
	}
	recovered := barrier.Publish(netsec.Model{
		TaskID:         rebuilt.ID(),
		Version:        "v3-rebuilt",
		ManifestDigest: cleanManifest.Digest(),
		Inputs:         cleanManifest.Inputs(),
	})
	if !recovered.Allowed {
		t.Fatalf("rebuilt model rejected: %+v", recovered)
	}

	// The ledger is append-only: rebuilding does not erase prior refusals.
	if len(barrier.Rejected()) != 2 {
		t.Fatalf("ledger mutated by rebuild: %+v", barrier.Rejected())
	}

	old, err := barrier.Task("stale-task")
	if err != nil || old.State() != "rebuilt" {
		t.Fatalf("old task state = %q (%v), want rebuilt", old.State(), err)
	}
}

// TestDeletionDuringTrainingRejectsCallback drives the ordering the package
// exists to stop: training starts, the individual is deleted while the trainer
// is paused mid-run, and the late callback is refused with the deletion kept as
// evidence.
func TestDeletionDuringTrainingRejectsCallback(t *testing.T) {
	barrier, deletes, _ := newTestBarrier()
	manifest := netsec.NewManifest("ind-1", "ind-2", "ind-3")
	task, err := barrier.StartTask("training-task", manifest)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	trainer := newPausableTrainer()
	_, cancel, decisions := startRun(t, barrier, trainer, task.ID(), manifest, "v1")
	defer cancel()

	// Training is in flight; erase ind-2 before the callback can publish.
	event := deletes.Record("ind-2")
	trainer.resume()

	decision := <-decisions
	trainer.wait()
	if decision.Allowed || decision.Reason != netsec.ReasonStaleGeneration {
		t.Fatalf("late callback decision = %+v, want %s", decision, netsec.ReasonStaleGeneration)
	}
	if len(decision.MissingDeletions) != 1 ||
		decision.MissingDeletions[0].Generation != event.Generation {
		t.Fatalf("missing deletions = %+v, want event gen %d", decision.MissingDeletions, event.Generation)
	}
	if len(decision.DeletedInputs) != 1 || decision.DeletedInputs[0] != "ind-2" {
		t.Fatalf("deleted inputs = %+v, want [ind-2]", decision.DeletedInputs)
	}

	ledger := barrier.Rejected()
	if len(ledger) != 1 || ledger[0].Model.Version != "v1" {
		t.Fatalf("ledger = %+v, want the single rejected v1 model", ledger)
	}

	state, err := barrier.Task(task.ID())
	if err != nil || state.State() != "rejected" {
		t.Fatalf("task state = %q (%v), want rejected", state.State(), err)
	}
}

// TestCancelDuringTrainingBlocksLateCallback verifies cancellation wins over a
// still-running callback: a task cancelled while paused can never publish even
// though its inputs are clean, and a later retry stays refused.
func TestCancelDuringTrainingBlocksLateCallback(t *testing.T) {
	barrier, _, _ := newTestBarrier()
	manifest := netsec.NewManifest("ind-1", "ind-2")
	task, err := barrier.StartTask("cancel-task", manifest)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	trainer := newPausableTrainer()
	_, cancel, decisions := startRun(t, barrier, trainer, task.ID(), manifest, "v1")

	if err := barrier.Cancel(task.ID()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	trainer.resume()

	decision := <-decisions
	trainer.wait()
	if decision.Allowed || decision.Reason != netsec.ReasonTaskCancelled {
		t.Fatalf("cancelled callback decision = %+v, want %s", decision, netsec.ReasonTaskCancelled)
	}

	// A second, even later callback after cancellation is still refused.
	late := barrier.Publish(netsec.Model{
		TaskID:         task.ID(),
		Version:        "v1-retry",
		ManifestDigest: manifest.Digest(),
		Inputs:         manifest.Inputs(),
	})
	if late.Allowed {
		t.Fatalf("retry after cancellation admitted: %+v", late)
	}

	ledger := barrier.Rejected()
	if len(ledger) != 2 ||
		ledger[0].Reason != netsec.ReasonTaskCancelled ||
		ledger[1].Reason != netsec.ReasonTaskCancelled {
		t.Fatalf("cancellation ledger = %+v", ledger)
	}
	cancel()
}

// TestCancelledTrainerNeverPublishes covers the other cancellation path: the
// context is cancelled while the trainer is paused, so the job exits and no
// publish call (and no evidence) is ever produced.
func TestCancelledTrainerNeverPublishes(t *testing.T) {
	barrier, _, _ := newTestBarrier()
	manifest := netsec.NewManifest("ind-1")
	task, err := barrier.StartTask("ctx-task", manifest)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	trainer := newPausableTrainer()
	ctx, cancel, decisions := startRun(t, barrier, trainer, task.ID(), manifest, "v1")

	if err := barrier.Cancel(task.ID()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	cancel()
	select {
	case <-trainer.done:
	case <-time.After(time.Second):
		t.Fatal("training goroutine did not exit after context cancellation")
	}

	select {
	case decision, ok := <-decisions:
		if ok {
			t.Fatalf("cancelled trainer published a decision: %+v", decision)
		}
	default:
	}
	_ = ctx
	// Releasing after cancellation is harmless: the run has already exited.
	trainer.resume()

	if len(barrier.Rejected()) != 0 {
		t.Fatalf("cancelled-without-publish must not forge evidence: %+v", barrier.Rejected())
	}
	state, err := barrier.Task(task.ID())
	if err != nil || state.State() != "cancelled" {
		t.Fatalf("task state = %q (%v), want cancelled", state.State(), err)
	}
}

// TestManifestSwapRejected proves the pinned manifest is enforced: claiming a
// different digest, or shipping different inputs under the old digest, is
// refused regardless of the version string.
func TestManifestSwapRejected(t *testing.T) {
	barrier, _, _ := newTestBarrier()
	manifest := netsec.NewManifest("ind-1", "ind-2")
	task, err := barrier.StartTask("swap-task", manifest)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	other := netsec.NewManifest("ind-1", "ind-2", "ind-3")
	swapped := barrier.Publish(netsec.Model{
		TaskID:         task.ID(),
		Version:        "v1",
		ManifestDigest: other.Digest(),
		Inputs:         other.Inputs(),
	})
	if swapped.Allowed || swapped.Reason != netsec.ReasonManifestMismatch {
		t.Fatalf("manifest swap decision = %+v, want %s", swapped, netsec.ReasonManifestMismatch)
	}

	// Forged: claims the pinned digest while actually carrying other inputs.
	forged := barrier.Publish(netsec.Model{
		TaskID:         task.ID(),
		Version:        "v2",
		ManifestDigest: manifest.Digest(),
		Inputs:         other.Inputs(),
	})
	if forged.Allowed || forged.Reason != netsec.ReasonManifestMismatch {
		t.Fatalf("forged digest decision = %+v, want %s", forged, netsec.ReasonManifestMismatch)
	}

	if len(barrier.Rejected()) != 2 {
		t.Fatalf("ledger = %+v, want both refusals retained", barrier.Rejected())
	}
}

// TestUnknownAndDoublePublishRejected covers callbacks for tasks the barrier
// never started, and a second publish attempt after a model already went live.
func TestUnknownAndDoublePublishRejected(t *testing.T) {
	barrier, _, _ := newTestBarrier()
	manifest := netsec.NewManifest("ind-1")

	unknown := barrier.Publish(netsec.Model{
		TaskID:         "ghost",
		Version:        "v1",
		ManifestDigest: manifest.Digest(),
		Inputs:         manifest.Inputs(),
	})
	if unknown.Allowed || unknown.Reason != netsec.ReasonUnknownTask {
		t.Fatalf("unknown task decision = %+v, want %s", unknown, netsec.ReasonUnknownTask)
	}

	task, err := barrier.StartTask("real-task", manifest)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	first := barrier.Publish(netsec.Model{
		TaskID:         task.ID(),
		Version:        "v1",
		ManifestDigest: manifest.Digest(),
		Inputs:         manifest.Inputs(),
	})
	if !first.Allowed {
		t.Fatalf("first publish rejected: %+v", first)
	}
	second := barrier.Publish(netsec.Model{
		TaskID:         task.ID(),
		Version:        "v2",
		ManifestDigest: manifest.Digest(),
		Inputs:         manifest.Inputs(),
	})
	if second.Allowed || second.Reason != netsec.ReasonTaskTerminal {
		t.Fatalf("double publish decision = %+v, want %s", second, netsec.ReasonTaskTerminal)
	}

	// Rebuild requires a known old task and a fresh new ID.
	if _, err := barrier.Rebuild("ghost", "new", manifest); err != netsec.ErrUnknownTask {
		t.Fatalf("rebuild unknown old task err = %v, want ErrUnknownTask", err)
	}
	if _, err := barrier.StartTask("real-task", manifest); err != netsec.ErrTaskExists {
		t.Fatalf("duplicate StartTask err = %v, want ErrTaskExists", err)
	}
}

// TestRejectionLedgerPreservesEvidence checks the audit trail is complete and
// immune to caller mutation: copies come back, timestamps are recorded, and the
// ledger stays ordered and append-only.
func TestRejectionLedgerPreservesEvidence(t *testing.T) {
	barrier, deletes, _ := newTestBarrier()
	manifest := netsec.NewManifest("ind-1", "ind-2")

	deletes.Record("ind-2")
	task, err := barrier.StartTask("ev-task", manifest)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	model := netsec.Model{
		TaskID:         task.ID(),
		Version:        "v1",
		ManifestDigest: manifest.Digest(),
		Inputs:         manifest.Inputs(),
	}
	barrier.Publish(model)

	ledger := barrier.Rejected()
	if len(ledger) != 1 {
		t.Fatalf("ledger length = %d, want 1", len(ledger))
	}
	entry := ledger[0]
	if entry.Reason != netsec.ReasonDeletedInput ||
		entry.Model.TaskID != "ev-task" ||
		entry.RejectedAt != time.Unix(2, 0) {
		t.Fatalf("evidence entry malformed: %+v", entry)
	}

	// Mutating returned slices must not corrupt the barrier's stored evidence.
	ledger[0].Model.Version = "tampered"
	ledger[0].DeletedInputs = append(ledger[0].DeletedInputs, "fake")
	fresh := barrier.Rejected()
	if fresh[0].Model.Version != "v1" {
		t.Fatalf("ledger not defensively copied: %+v", fresh[0])
	}

	// A second refusal appends rather than replacing.
	barrier.Publish(model)
	if len(barrier.Rejected()) != 2 {
		t.Fatalf("ledger must append, got %d entries", len(barrier.Rejected()))
	}
}

// TestConcurrentDeletionAndPublish exercises the interleaving under the race
// detector: many callbacks racing with deletions must never admit a model that
// still contains an erased individual.
func TestConcurrentDeletionAndPublish(t *testing.T) {
	barrier, deletes, _ := newTestBarrier()
	manifest := netsec.NewManifest("ind-1", "ind-2", "ind-3", "ind-4")

	const tasks = 16
	for i := 0; i < tasks; i++ {
		if _, err := barrier.StartTask(
			"concurrent-"+strconv.Itoa(i), manifest,
		); err != nil {
			t.Fatalf("StartTask %d: %v", i, err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for _, id := range manifest.Inputs() {
			deletes.Record(id)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < tasks; i++ {
			barrier.Publish(netsec.Model{
				TaskID:         "concurrent-" + strconv.Itoa(i),
				Version:        "v1",
				ManifestDigest: manifest.Digest(),
				Inputs:         manifest.Inputs(),
			})
		}
	}()
	wg.Wait()

	// Each task attempted exactly one publish, so every task is terminal. A
	// task is published only if its publish won the race before any deletion;
	// otherwise it is rejected and the ledger carries the evidence. The two
	// populations must partition the task set and agree with the ledger size.
	published, rejected := 0, 0
	for i := 0; i < tasks; i++ {
		state, err := barrier.Task("concurrent-" + strconv.Itoa(i))
		if err != nil {
			t.Fatalf("Task %d: %v", i, err)
		}
		switch state.State() {
		case "published":
			published++
		case "rejected":
			rejected++
		default:
			t.Fatalf("task %d unexpected state %q", i, state.State())
		}
	}
	if published+rejected != tasks {
		t.Fatalf("published=%d rejected=%d do not cover %d tasks", published, rejected, tasks)
	}
	if len(barrier.Rejected()) != rejected {
		t.Fatalf("ledger size = %d, want %d rejected tasks", len(barrier.Rejected()), rejected)
	}

	// After all deletions landed, nothing pinned on the old manifest may ever
	// publish again; a rebuilt task over the emptied manifest is the only path.
	empty := netsec.NewManifest()
	rebuilt, err := barrier.Rebuild("concurrent-0", "concurrent-rebuilt", empty)
	if err != nil {
		t.Fatalf("Rebuild after drain: %v", err)
	}
	decision := barrier.Publish(netsec.Model{
		TaskID:         rebuilt.ID(),
		Version:        "v2",
		ManifestDigest: empty.Digest(),
		Inputs:         empty.Inputs(),
	})
	if !decision.Allowed {
		t.Fatalf("rebuilt empty-manifest model rejected: %+v", decision)
	}
}
