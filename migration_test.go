package netsec

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"
	"time"
)

func newRig(t *testing.T, d *EventDispatcher) (*Memory, *Target, *Migrator, *Signer) {
	t.Helper()
	src := testSource("vm-a", "src-instance")
	target := testTarget("vm-a", "dst-instance")
	signer := testSigner(t)
	opts := []MigratorOption{}
	if d != nil {
		opts = append(opts, WithMigrationInjector(d))
	}
	m, err := NewMigrator(src, target, signer, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return src, target, m, signer
}

func TestEndToEndMigration(t *testing.T) {
	src, target, m, _ := newRig(t, nil)
	mustWrite(t, src, 0, []byte("boot-sector"))
	mustWrite(t, src, 1, []byte("kernel"))
	if _, err := src.WriteDevicePage(2, []byte("dma-ring")); err != nil {
		t.Fatal(err)
	}

	pre, err := m.RunPreCopy(4)
	if err != nil {
		t.Fatal(err)
	}
	if !pre.Converged {
		t.Fatalf("pre-copy did not converge: %+v", pre)
	}
	device := DeviceState{
		Devices:       []string{"nic0", "nvme0"},
		DMARegions:    []DMARegion{{Device: "nic0", Start: 0x1000, Length: 0x100}},
		PolicyVersion: 7,
		KeyEpoch:      11,
		State:         []byte("keys-v11"),
	}
	h, err := m.Cutover(device)
	if err != nil {
		t.Fatal(err)
	}
	if h.ManifestRoot != h.Manifest.Root() {
		t.Fatal("manifest root mismatch")
	}
	if err := VerifyReceipt(ed25519.PublicKey(h.Receipt.Signature[:0]), h.Receipt, h.Manifest); !errors.Is(err, ErrUntrustedHost) {
		t.Fatalf("empty key verification = %v", err)
	}
	if err := VerifyReceipt(m.signer.PublicKey(), h.Receipt, h.Manifest); err != nil {
		t.Fatalf("valid receipt: %v", err)
	}

	// Source remains frozen before the target confirms startup.
	if _, err := src.WritePage(3, []byte("late")); !errors.Is(err, ErrFrozen) {
		t.Fatalf("write before confirmation = %v", err)
	}
	if err := m.ReleaseMemory(); !errors.Is(err, ErrNotTerminated) {
		t.Fatalf("memory reuse before confirmation = %v", err)
	}
	if err := src.Thaw(); !errors.Is(err, ErrSealed) {
		t.Fatalf("direct thaw after cutover = %v", err)
	}

	conf, err := m.StartTarget()
	if err != nil {
		t.Fatal(err)
	}
	if target.State() != TargetLive {
		t.Fatalf("target state = %s", target.State())
	}
	if !bytes.Equal(mustTargetPage(t, target, 1), []byte("kernel")) {
		t.Fatal("target page content mismatch")
	}
	if err := m.ConfirmStartup(conf); err != nil {
		t.Fatal(err)
	}
	if !src.Terminated() || src.State() != MemoryTerminated {
		t.Fatalf("source not terminated: %s", src.State())
	}
	if _, err := src.WritePage(3, []byte("after")); !errors.Is(err, ErrTerminated) {
		t.Fatalf("write after termination = %v", err)
	}
	if err := m.ReleaseMemory(); err != nil {
		t.Fatalf("release after confirmation: %v", err)
	}
	if src.State() != MemoryReleased {
		t.Fatalf("source state = %s", src.State())
	}

	// A stale receipt cannot start a second time.
	if _, err := target.Start(h.Receipt, h.Manifest); err != nil {
		t.Fatalf("idempotent target start: %v", err)
	}
}

func TestAllSixtyFourPagesMigrate(t *testing.T) {
	src, target, m, _ := newRig(t, nil)
	for i := 0; i < MaxPages; i++ {
		data := bytes.Repeat([]byte{byte(i)}, i+1)
		if _, err := src.WritePage(PageID(i), data); err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
	}
	pre, err := m.RunPreCopy(2)
	if err != nil {
		t.Fatal(err)
	}
	if !pre.Converged {
		t.Fatalf("64-page pre-copy = %+v", pre)
	}
	h, err := m.Cutover(DeviceState{PolicyVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if h.Manifest.Pages[63].Generation != 1 {
		t.Fatalf("last page generation = %d", h.Manifest.Pages[63].Generation)
	}
	if _, err := m.StartTarget(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxPages; i++ {
		got := mustTargetPage(t, target, PageID(i))
		want := bytes.Repeat([]byte{byte(i)}, i+1)
		if !bytes.Equal(got, want) {
			t.Fatalf("page %d content mismatch", i)
		}
	}
}

func mustTargetPage(t *testing.T, target *Target, page PageID) []byte {
	t.Helper()
	data, err := target.ReadPage(page)
	if err != nil {
		t.Fatalf("read target page %d: %v", page, err)
	}
	return data
}

func TestReceiptBindingAndTamperRejection(t *testing.T) {
	src, target, m, _ := newRig(t, nil)
	mustWrite(t, src, 0, []byte("v1"))
	if _, err := m.RunPreCopy(1); err != nil {
		t.Fatal(err)
	}
	h, err := m.Cutover(DeviceState{PolicyVersion: 1, KeyEpoch: 1})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		make func() (Receipt, Manifest)
	}{
		{
			name: "signature",
			make: func() (Receipt, Manifest) {
				r := h.Receipt.Clone()
				r.Signature[0] ^= 0xff
				return r, h.Manifest
			},
		},
		{
			name: "vm",
			make: func() (Receipt, Manifest) {
				r := h.Receipt.Clone()
				r.VM = "vm-b"
				return r, h.Manifest
			},
		},
		{
			name: "source-instance",
			make: func() (Receipt, Manifest) {
				r := h.Receipt.Clone()
				r.SourceInstance = "other-source"
				return r, h.Manifest
			},
		},
		{
			name: "target-instance",
			make: func() (Receipt, Manifest) {
				r := h.Receipt.Clone()
				r.TargetInstance = "other-target"
				return r, h.Manifest
			},
		},
		{
			name: "epoch",
			make: func() (Receipt, Manifest) {
				r := h.Receipt.Clone()
				r.FinalEpoch++
				return r, h.Manifest
			},
		},
		{
			name: "page-generation",
			make: func() (Receipt, Manifest) {
				r := h.Receipt.Clone()
				r.Pages[0]++
				return r, h.Manifest
			},
		},
		{
			name: "device-digest",
			make: func() (Receipt, Manifest) {
				r := h.Receipt.Clone()
				r.DeviceDigest[0] ^= 0xff
				return r, h.Manifest
			},
		},
		{
			name: "manifest-root",
			make: func() (Receipt, Manifest) {
				r := h.Receipt.Clone()
				r.ManifestRoot[0] ^= 0xff
				return r, h.Manifest
			},
		},
		{
			name: "tampered-manifest",
			make: func() (Receipt, Manifest) {
				mm := h.Manifest
				mm.Pages[0].Generation++
				return h.Receipt, mm
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, mm := tc.make()
			if _, err := target.Start(r, mm); err == nil {
				t.Fatal("tampered receipt was accepted")
			}
			if target.Started() {
				t.Fatal("target became live after a rejected receipt")
			}
			if _, err := target.ReadPage(0); !errors.Is(err, ErrQuarantineHidden) {
				t.Fatalf("quarantine visible after rejection: %v", err)
			}
		})
	}

	conf, err := target.Start(h.Receipt, h.Manifest)
	if err != nil {
		t.Fatalf("valid receipt after rejections: %v", err)
	}
	conf2, err := target.Start(h.Receipt, h.Manifest)
	if err != nil || *conf != *conf2 {
		t.Fatalf("idempotent start: conf=%+v err=%v", conf2, err)
	}
}

func TestTargetDoesNotAdoptMigratorSignerAfterExplicitTrust(t *testing.T) {
	src := testSource("vm-a", "src-instance")
	target := testTarget("vm-a", "dst-instance")
	other, err := NewSigner(bytes.Repeat([]byte{0x99}, 32))
	if err != nil {
		t.Fatal(err)
	}
	target.TrustHost(other.PublicKey())
	m, err := NewMigrator(src, target, testSigner(t))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, src, 0, []byte("x"))
	if _, err := m.RunPreCopy(1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cutover(DeviceState{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.StartTarget(); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("target accepted a receipt from an unprovisioned signer: %v", err)
	}
	if target.Started() {
		t.Fatal("target started under the wrong host key")
	}
	if err := m.Abort("wrong key"); err != nil {
		t.Fatal(err)
	}
}

func TestReceiptLossKeepsSourceFrozenThenAbortThaws(t *testing.T) {
	d := NewEventDispatcher(DropReceipt())
	src, target, m, _ := newRig(t, d)
	mustWrite(t, src, 4, []byte("secret"))
	if _, err := m.RunPreCopy(1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cutover(DeviceState{PolicyVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.StartTarget(); !errors.Is(err, ErrInjectedFault) {
		t.Fatalf("lost receipt error = %v", err)
	}
	if target.Started() {
		t.Fatal("target started despite lost receipt")
	}
	if !src.Frozen() {
		t.Fatal("source was not kept frozen")
	}
	if _, err := target.ReadPage(4); !errors.Is(err, ErrQuarantineHidden) {
		t.Fatalf("quarantine became visible: %v", err)
	}
	if err := m.Abort("receipt lost"); err != nil {
		t.Fatal(err)
	}
	if src.Frozen() {
		t.Fatal("abort did not thaw source")
	}
	if target.State() != TargetAborted {
		t.Fatalf("target state after abort = %s", target.State())
	}
	if _, err := target.ReadPage(4); err == nil {
		t.Fatal("aborted quarantine exposed data")
	}
	if _, err := src.WritePage(4, []byte("continued")); err != nil {
		t.Fatalf("source did not resume: %v", err)
	}
}

func TestConfirmationLossReconcilesRunningTarget(t *testing.T) {
	d := NewEventDispatcher(DropStartupConfirmation())
	src, target, m, _ := newRig(t, d)
	mustWrite(t, src, 5, []byte("state"))
	if _, err := m.RunPreCopy(1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cutover(DeviceState{}); err != nil {
		t.Fatal(err)
	}
	conf, err := m.StartTarget()
	if !errors.Is(err, ErrInjectedFault) || conf == nil {
		t.Fatalf("confirmation loss = conf:%v err:%v", conf, err)
	}
	if !target.Started() {
		t.Fatal("target should already be live")
	}
	if !src.Frozen() {
		t.Fatal("source thawed without confirmation")
	}
	if err := m.Abort("lost ack"); !errors.Is(err, ErrTargetStarted) {
		t.Fatalf("abort after target start = %v", err)
	}
	if _, err := m.ReconcileStartup(); err != nil {
		t.Fatal(err)
	}
	if !src.Terminated() {
		t.Fatal("source not terminated after reconciliation")
	}
}

func TestForgedConfirmationCannotTerminateSource(t *testing.T) {
	src, _, m, _ := newRig(t, nil)
	mustWrite(t, src, 0, []byte("x"))
	if _, err := m.RunPreCopy(1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cutover(DeviceState{}); err != nil {
		t.Fatal(err)
	}
	h, err := m.Handoff()
	if err != nil {
		t.Fatal(err)
	}
	// The target never started; a fabricated confirmation must not terminate
	// the frozen source.
	forged := StartupConfirmation{
		VM: h.Manifest.VM, SourceInstance: h.Manifest.SourceInstance,
		TargetInstance: h.Manifest.TargetInstance, FinalEpoch: h.Manifest.Epoch,
		Pages: h.Manifest.PageGenerations(), DeviceDigest: h.Manifest.DeviceDigest,
		ManifestRoot: h.Manifest.Root(), ReceiptDigest: h.Receipt.Digest(),
	}
	if err := m.ConfirmStartup(&forged); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("forged confirmation = %v", err)
	}
	if !src.Frozen() || src.Terminated() {
		t.Fatalf("source state changed: %s", src.State())
	}
}

func TestConcurrentAbortAndTargetStartNeverSplitBrain(t *testing.T) {
	for i := 0; i < 50; i++ {
		src, target, m, _ := newRig(t, nil)
		mustWrite(t, src, 0, []byte("payload"))
		if _, err := m.RunPreCopy(1); err != nil {
			t.Fatal(err)
		}
		h, err := m.Cutover(DeviceState{})
		if err != nil {
			t.Fatal(err)
		}

		abortDone := make(chan error, 1)
		startDone := make(chan error, 1)
		start := make(chan struct{})
		go func() {
			<-start
			abortDone <- m.Abort("race")
		}()
		go func() {
			<-start
			_, err := target.Start(h.Receipt, h.Manifest)
			startDone <- err
		}()
		close(start)
		abortErr := <-abortDone
		startErr := <-startDone

		if target.Started() {
			if !src.Frozen() {
				t.Fatal("split brain: live target with thawed source")
			}
			if !errors.Is(abortErr, ErrTargetStarted) {
				t.Fatalf("abort after concurrent start = %v", abortErr)
			}
			if startErr != nil {
				t.Fatalf("start failed while target became live: %v", startErr)
			}
		} else {
			if src.Frozen() {
				t.Fatalf("aborted migration left source frozen: abort=%v start=%v", abortErr, startErr)
			}
			if !errors.Is(startErr, ErrTargetAborted) {
				t.Fatalf("start after abort = %v", startErr)
			}
			if abortErr != nil {
				t.Fatalf("abort failed: %v", abortErr)
			}
		}
	}
}

func TestAbortAndReleaseFaultsFailClosedAndRetry(t *testing.T) {
	d := NewEventDispatcher(nil)
	src, target, m, _ := newRig(t, d)
	mustWrite(t, src, 1, []byte("state"))
	if _, err := m.RunPreCopy(1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cutover(DeviceState{}); err != nil {
		t.Fatal(err)
	}

	d.SetFault(NewScriptedFaults(FaultAt{
		Name: EventThaw, Phase: EventPhaseBefore, Occurrence: 1, Err: errors.New("thaw fault"),
	}))
	if err := m.Abort("faulted abort"); err == nil {
		t.Fatal("injected thaw fault was not reported")
	}
	if !src.Frozen() {
		t.Fatal("source thawed despite a failed abort")
	}
	if target.State() != TargetAborted {
		t.Fatalf("target state = %s", target.State())
	}
	if err := m.Abort("retry"); err != nil {
		t.Fatalf("abort retry: %v", err)
	}
	if src.Frozen() {
		t.Fatal("source remained frozen after successful abort retry")
	}

	// A fresh migration reaches termination, then a release fault is retried.
	src2 := testSource("vm-b", "src-b")
	dst2 := testTarget("vm-b", "dst-b")
	d2 := NewEventDispatcher(nil)
	m2, err := NewMigrator(src2, dst2, testSigner(t), WithMigrationInjector(d2))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, src2, 0, []byte("x"))
	if err := func() error {
		if _, err := m2.RunPreCopy(1); err != nil {
			return err
		}
		if _, err := m2.Cutover(DeviceState{}); err != nil {
			return err
		}
		conf, err := m2.StartTarget()
		if err != nil {
			return err
		}
		return m2.ConfirmStartup(conf)
	}(); err != nil {
		t.Fatal(err)
	}
	d2.SetFault(NewScriptedFaults(FaultAt{
		Name: EventSourceReleased, Phase: EventPhaseBefore, Occurrence: 1, Err: errors.New("release fault"),
	}))
	if err := m2.ReleaseMemory(); err == nil {
		t.Fatal("injected release fault was not reported")
	}
	if src2.State() != MemoryTerminated {
		t.Fatalf("source state after failed release = %s", src2.State())
	}
	d2.SetFault(nil)
	if err := m2.ReleaseMemory(); err != nil {
		t.Fatalf("release retry: %v", err)
	}
	if src2.State() != MemoryReleased {
		t.Fatalf("source state after release = %s", src2.State())
	}
}

func TestEveryProtocolEventIsObservable(t *testing.T) {
	var mu sync.Mutex
	seen := make(map[EventName]bool)
	d := NewEventDispatcher(nil, func(ev Event) {
		mu.Lock()
		seen[ev.Name] = true
		mu.Unlock()
	})

	src, _, m, _ := newRig(t, d)
	sched := NewWriteScheduler(src)
	sched.ScheduleSync(EventPageCopyBegin, EventPhaseBefore, 1, 1, []byte("race"), WriteDMA)
	d.SetFault(FaultFunc(sched.Handle))
	mustWrite(t, src, 0, []byte("start"))
	if _, err := m.RunPreCopy(2); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cutover(DeviceState{}); err != nil {
		t.Fatal(err)
	}
	conf, err := m.StartTarget()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmStartup(conf); err != nil {
		t.Fatal(err)
	}
	if err := m.ReleaseMemory(); err != nil {
		t.Fatal(err)
	}

	// Abort/thaw events need a second, non-terminated migration.
	d.SetFault(nil)
	src2 := testSource("vm-c", "src-c")
	dst2 := testTarget("vm-c", "dst-c")
	m2, err := NewMigrator(src2, dst2, testSigner(t), WithMigrationInjector(d))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, src2, 0, []byte("abort-me"))
	if _, err := m2.RunPreCopy(1); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Cutover(DeviceState{}); err != nil {
		t.Fatal(err)
	}
	if err := m2.Abort("coverage"); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, name := range ProtocolEvents() {
		if !seen[name] {
			t.Errorf("event %s was never emitted", name)
		}
	}
}

func TestWriteDuringCopyKeepsDirty(t *testing.T) {
	d := NewEventDispatcher(nil)
	src, target, m, _ := newRig(t, d)
	mustWrite(t, src, 0, []byte("v1"))
	sched := NewWriteScheduler(src)
	sched.ScheduleSync(EventPageCopyBegin, EventPhaseBefore, 1, 0, []byte("v2"), WriteVCPU)
	// The scheduler shares the same dispatcher.
	d.SetFault(FaultFunc(sched.Handle))

	round, err := m.Round()
	if err != nil {
		t.Fatal(err)
	}
	if round.Cleared != 0 {
		t.Fatalf("old snapshot cleared dirty: %+v", round)
	}
	if round.RemainingDirty != 1 {
		t.Fatalf("dirty set lost: %+v", round)
	}
	info, _ := src.PageInfo(0)
	if info.Generation != 2 || !info.Dirty {
		t.Fatalf("source page after race = %+v", info)
	}

	pre, err := m.RunPreCopy(2)
	if err != nil {
		t.Fatal(err)
	}
	if !pre.Converged {
		t.Fatalf("did not converge after write during copy: %+v", pre)
	}
	if _, err := m.Cutover(DeviceState{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.StartTarget(); err != nil {
		t.Fatal(err)
	}
	if got := mustTargetPage(t, target, 0); !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("target started with %q, want v2", got)
	}
	if gen, ok := target.StagedGeneration(0); !ok || gen != 2 {
		t.Fatalf("target generation = %d, %v", gen, ok)
	}
}

func TestAsyncScheduledWritesConverge(t *testing.T) {
	d := NewEventDispatcher(nil)
	src, target, m, _ := newRig(t, d)
	mustWrite(t, src, 1, []byte("base"))
	sched := NewWriteScheduler(src)
	w1 := sched.Schedule(EventPageCopyDone, EventPhaseAfter, 1, 1, []byte("race-1"), WriteVCPU)
	w2 := sched.Schedule(EventPageCopyDone, EventPhaseAfter, 1, 2, []byte("race-2"), WriteDMA)
	d.SetFault(FaultFunc(sched.Handle))

	pre, err := m.RunPreCopy(4)
	if err != nil {
		t.Fatal(err)
	}
	if !pre.Converged {
		t.Fatalf("async writes did not converge: %+v", pre)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sched.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := w1.Wait(ctx); err != nil && !errors.Is(err, ErrFrozen) {
		t.Fatalf("scheduled write 1: %v", err)
	}
	if _, err := w2.Wait(ctx); err != nil && !errors.Is(err, ErrFrozen) {
		t.Fatalf("scheduled write 2: %v", err)
	}
	if _, err := m.Cutover(DeviceState{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.StartTarget(); err != nil {
		t.Fatal(err)
	}
	if got := mustTargetPage(t, target, 1); !bytes.Equal(got, []byte("race-1")) {
		t.Fatalf("target page 1 = %q", got)
	}
	if got := mustTargetPage(t, target, 2); !bytes.Equal(got, []byte("race-2")) {
		t.Fatalf("target page 2 = %q", got)
	}
}

func TestDuplicateRoundsAndRecordsAreIdempotent(t *testing.T) {
	src, target, m, _ := newRig(t, nil)
	mustWrite(t, src, 3, []byte("same"))
	round, err := m.Round()
	if err != nil {
		t.Fatal(err)
	}
	if round.Copied != 1 || round.Cleared != 1 {
		t.Fatalf("first round = %+v", round)
	}
	rec := PageRecord{Page: 3, VM: "vm-a", Generation: 1, Digest: DigestBytes([]byte("same")), Data: []byte("same")}
	if err := target.StagePage(rec); err != nil {
		t.Fatalf("idempotent duplicate = %v", err)
	}
	rec.Data = []byte("different")
	rec.Digest = DigestBytes(rec.Data)
	if err := target.StagePage(rec); !errors.Is(err, ErrConflictingPage) {
		t.Fatalf("same generation different bytes = %v", err)
	}
	round2, err := m.Round()
	if err != nil {
		t.Fatal(err)
	}
	if round2.Copied != 0 || round2.Epoch <= round.Epoch {
		t.Fatalf("duplicate round = %+v", round2)
	}
	if target.QuarantineCount() != 1 {
		t.Fatalf("duplicates changed quarantine count to %d", target.QuarantineCount())
	}
}

func TestOldSnapshotCannotRegressTargetOrClearNewDirty(t *testing.T) {
	src, target, m, _ := newRig(t, nil)
	mustWrite(t, src, 2, []byte("old"))
	snap, err := src.SnapshotDirty()
	if err != nil {
		t.Fatal(err)
	}
	old := PageRecord{Page: 2, VM: "vm-a", Generation: snap.Pages[0].Generation, Digest: snap.Pages[0].Digest, Data: snap.Pages[0].Data}
	mustWrite(t, src, 2, []byte("new"))
	// Replay an old source snapshot after the source has advanced.
	if err := target.StagePage(old); err != nil {
		t.Fatalf("initial old snapshot stage: %v", err)
	}
	if cleared, _ := src.AcknowledgeCopied(2, 1); cleared {
		t.Fatal("old acknowledged generation cleared newer dirty bit")
	}
	info, _ := src.PageInfo(2)
	if !info.Dirty || info.Generation != 2 {
		t.Fatalf("source dirty state lost: %+v", info)
	}
	round, err := m.Round()
	if err != nil {
		t.Fatal(err)
	}
	if round.Cleared != 1 || round.RemainingDirty != 0 {
		t.Fatalf("converging round = %+v", round)
	}
	if err := target.StagePage(old); !errors.Is(err, ErrStalePage) {
		t.Fatalf("stale snapshot rollback accepted after newer copy: %v", err)
	}
	if gen, _ := target.StagedGeneration(2); gen != 2 {
		t.Fatalf("target generation regressed to %d", gen)
	}
}

func TestStaleTargetStartupYieldsShortestWitness(t *testing.T) {
	target := testTarget("vm-a", "dst-instance")
	signer := testSigner(t)
	target.TrustHost(signer.PublicKey())

	// The target only has generation 1.
	rec := PageRecord{Page: 0, VM: "vm-a", Generation: 1, Digest: DigestBytes([]byte("old")), Data: []byte("old")}
	if err := target.StagePage(rec); err != nil {
		t.Fatal(err)
	}
	// The source froze at generation 2.
	var pages [MaxPages]PageManifestEntry
	pages[0] = PageManifestEntry{Page: 0, Generation: 2, Digest: DigestBytes([]byte("new"))}
	manifest := Manifest{
		VM: "vm-a", SourceInstance: "src-instance", TargetInstance: "dst-instance",
		Epoch: 9, Pages: pages, DeviceDigest: DigestBytes([]byte("device")),
	}
	receipt, err := signer.BuildReceipt(manifest)
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.Start(receipt, manifest)
	if !errors.Is(err, ErrStaleStartup) {
		t.Fatalf("start error = %v", err)
	}
	stale, ok := WitnessFromError(err)
	if !ok {
		t.Fatal("no stale startup witness")
	}
	w := stale.ShortestWitness()
	if w.Page != 0 || w.TargetGeneration != 1 || w.ExpectedGeneration != 2 {
		t.Fatalf("witness = %+v", w)
	}
	if target.Started() {
		t.Fatal("stale target became visible")
	}
	if _, err := target.ReadPage(0); !errors.Is(err, ErrQuarantineHidden) {
		t.Fatalf("stale quarantine visible: %v", err)
	}
}

func TestCrossVMManifestAndPageReplayRejected(t *testing.T) {
	src, _, m, signer := newRig(t, nil)
	mustWrite(t, src, 0, []byte("vm-a-secret"))
	if _, err := m.RunPreCopy(1); err != nil {
		t.Fatal(err)
	}
	h, err := m.Cutover(DeviceState{PolicyVersion: 2, KeyEpoch: 3})
	if err != nil {
		t.Fatal(err)
	}

	other := testTarget("vm-b", "dst-b")
	other.TrustHost(signer.PublicKey())
	rec := PageRecord{Page: 0, VM: "vm-a", Generation: 1, Digest: DigestBytes([]byte("vm-a-secret")), Data: []byte("vm-a-secret")}
	if err := other.StagePage(rec); !errors.Is(err, ErrVMNotOwner) {
		t.Fatalf("cross-VM page accepted: %v", err)
	}
	if _, err := other.Start(h.Receipt, h.Manifest); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("cross-VM receipt replay = %v", err)
	}
	if other.Started() {
		t.Fatal("cross-VM target started")
	}

	// Even a correctly signed manifest for VM A cannot be relabeled for VM B
	// because the root covers the VM identity and instance pair.
	mm := h.Manifest
	mm.VM = "vm-b"
	r2, err := signer.BuildReceipt(mm)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Start(r2, mm); !errors.Is(err, ErrInvalidManifest) && !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("relabeled manifest = %v", err)
	}
}

func TestDeviceStateDigestBinding(t *testing.T) {
	a := DeviceState{
		Devices: []string{"nic0", "nvme0"}, PolicyVersion: 1, KeyEpoch: 4,
		DMARegions: []DMARegion{{Device: "nic0", Start: 0x8000, Length: 0x1000}},
		State:      []byte("policy-A"),
	}
	b := a
	b.KeyEpoch = 3
	b.State = []byte("policy-rollback")
	if a.Digest() == b.Digest() {
		t.Fatal("policy/key rollback produced the same digest")
	}
	if a.Digest() != a.Clone().Digest() {
		t.Fatal("device digest not deterministic")
	}
	// Ordering of equivalent descriptions must not change the digest.
	reordered := a
	reordered.Devices = []string{"nvme0", "nic0"}
	if a.Digest() != reordered.Digest() {
		t.Fatal("device digest depends on list order")
	}
}

func TestDeviceStateProviderRunsAfterFreeze(t *testing.T) {
	src, _, m, _ := newRig(t, nil)
	mustWrite(t, src, 0, []byte("x"))
	if _, err := m.RunPreCopy(1); err != nil {
		t.Fatal(err)
	}
	var sawFrozen bool
	h, err := m.CutoverWithDeviceState(func() DeviceState {
		sawFrozen = src.Frozen()
		return DeviceState{PolicyVersion: 9, KeyEpoch: 9}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawFrozen {
		t.Fatal("device state was captured before the source freeze")
	}
	if h.Manifest.DeviceDigest != (DeviceState{PolicyVersion: 9, KeyEpoch: 9}).Digest() {
		t.Fatal("manifest did not bind the post-freeze device state")
	}
}

func TestManifestRootIsDeterministicAndAllPageBinding(t *testing.T) {
	var pages [MaxPages]PageManifestEntry
	for i := 0; i < 4; i++ {
		pages[i] = PageManifestEntry{Page: PageID(i), Generation: Generation(i + 1), Digest: DigestBytes([]byte{byte(i)})}
	}
	m := Manifest{
		VM: "vm-a", SourceInstance: "s", TargetInstance: "t",
		Epoch: 5, Pages: pages, DeviceDigest: DigestBytes([]byte("dev")),
	}
	if m.Root() != m.Root() {
		t.Fatal("manifest root is not deterministic")
	}
	changed := m
	changed.Pages[3].Generation++
	if m.Root() == changed.Root() {
		t.Fatal("manifest root did not bind last page generation")
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := m
	invalid.Pages[0].Page = 9
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("invalid page index = %v", err)
	}
}

func TestFaultInjectionAtEveryProtocolEvent(t *testing.T) {
	points := []struct {
		name  EventName
		phase EventPhase
	}{
		{EventRoundSnapshot, EventPhaseBefore},
		{EventPageCopyBegin, EventPhaseBefore},
		{EventPageCopyDone, EventPhaseAfter},
		{EventPageStage, EventPhaseBefore},
		{EventPageReadback, EventPhaseBefore},
		{EventDirtyAck, EventPhaseBefore},
		{EventFreezeBegin, EventPhaseBefore},
		{EventFreezeWait, EventPhaseBefore},
		{EventFreezeDone, EventPhaseAfter},
		{EventFinalPageCopy, EventPhaseBefore},
		{EventDeviceState, EventPhaseAfter},
		{EventManifestBuild, EventPhaseBefore},
		{EventReceiptSign, EventPhaseAfter},
		{EventReceiptDelivery, EventPhaseBefore},
		{EventReceiptVerify, EventPhaseBefore},
		{EventTargetStartCheck, EventPhaseBefore},
		{EventTargetStart, EventPhaseBefore},
		{EventTargetVisible, EventPhaseAfter},
		{EventStartupConfirmation, EventPhaseAfter},
		{EventSourceConfirm, EventPhaseBefore},
		{EventSourceTerminated, EventPhaseAfter},
	}
	for _, pt := range points {
		pt := pt
		t.Run(string(pt.name)+"/"+pt.phase.String(), func(t *testing.T) {
			fault := NewScriptedFaults(FaultAt{Name: pt.name, Phase: pt.phase, Occurrence: 1, Err: errors.New("boom")})
			d := NewEventDispatcher(fault)
			src, target, m, _ := newRig(t, d)
			mustWrite(t, src, 0, []byte("payload"))

			_, err := m.Migrate(DeviceState{PolicyVersion: 1, KeyEpoch: 2}, 1)
			if err == nil && !target.Started() {
				t.Fatalf("fault at %s/%s was not observed", pt.name, pt.phase)
			}

			if target.Started() {
				// Some post-start notifications are allowed to fail after
				// the target is live.  Recovery must reconcile, not thaw.
				if _, recErr := m.ReconcileStartup(); recErr != nil {
					t.Fatalf("reconcile after fault: %v", recErr)
				}
				if !src.Terminated() {
					t.Fatal("source remained reusable after a live target started")
				}
				assertTargetMatchesManifest(t, target, m)
				return
			}
			if err == nil {
				t.Fatalf("fault at %s/%s did not fail and target did not start", pt.name, pt.phase)
			}

			if m.State() != MigrationAborted {
				if err := m.Abort("fault recovery"); err != nil && !errors.Is(err, ErrTargetAborted) {
					t.Fatalf("abort after fault: %v", err)
				}
			}
			if src.Frozen() {
				t.Fatal("source remained frozen after aborted hand-off")
			}
			if target.State() == TargetLive {
				t.Fatal("failed hand-off left a live target")
			}
			if _, err := target.ReadPage(0); err == nil {
				t.Fatal("failed hand-off exposed quarantine")
			}
		})
	}
}

func assertTargetMatchesManifest(t *testing.T, target *Target, m *Migrator) {
	t.Helper()
	h, err := m.Handoff()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxPages; i++ {
		entry := h.Manifest.Pages[i]
		if entry.Generation == 0 {
			continue
		}
		data, err := target.ReadPage(PageID(i))
		if err != nil {
			t.Fatalf("live target page %d: %v", i, err)
		}
		if DigestBytes(data) != entry.Digest {
			t.Fatalf("live target page %d digest mismatch", i)
		}
	}
}

func TestWriteEventFaults(t *testing.T) {
	t.Run("before", func(t *testing.T) {
		fault := NewScriptedFaults(FaultAt{Name: EventWriteBegin, Phase: EventPhaseBefore, Occurrence: 1})
		m := testSource("vm-a", "src-a", WithMemoryInjector(NewEventDispatcher(fault)))
		if _, err := m.WritePage(0, []byte("never")); !errors.Is(err, ErrInjectedFault) {
			t.Fatalf("write error = %v", err)
		}
		info, _ := m.PageInfo(0)
		if info.Valid || info.Generation != 0 {
			t.Fatalf("failed admitted write changed state: %+v", info)
		}
		// The in-flight counter must have been released, otherwise a freeze
		// would hang forever.
		done := make(chan error, 1)
		go func() {
			_, err := m.Freeze()
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("freeze blocked after a failed write")
		}
	})
	t.Run("after", func(t *testing.T) {
		fault := NewScriptedFaults(FaultAt{Name: EventWriteCommit, Phase: EventPhaseAfter, Occurrence: 1})
		m := testSource("vm-a", "src-a", WithMemoryInjector(NewEventDispatcher(fault)))
		gen, err := m.WritePage(0, []byte("applied"))
		if !errors.Is(err, ErrInjectedFault) || gen != 1 {
			t.Fatalf("post-commit fault = gen:%d err:%v", gen, err)
		}
		info, _ := m.PageInfo(0)
		if !info.Valid || info.Generation != 1 || !info.Dirty {
			t.Fatalf("post-commit state = %+v", info)
		}
	})
}

func TestWriteBarrierFreezeRaceAcrossPages(t *testing.T) {
	var gate syncOnceGate
	src := testSource("vm-a", "src-a", WithWriteBarrier(func(WriteAdmission) { gate.Wait() }))
	const n = 16
	reqs := make([]WriteRequest, n)
	for i := range reqs {
		reqs[i] = WriteRequest{Page: PageID(i), Data: []byte{byte(i)}}
	}
	outCh := make(chan []WriteOutcome, 1)
	go func() { outCh <- ConcurrentWrites(src, reqs...) }()
	gate.Release()
	freezeCh := make(chan FreezePoint, 1)
	go func() {
		fp, _ := src.Freeze()
		freezeCh <- fp
	}()
	// Release admitted writes only after the freeze gate is visibly closed.
	waitFor(t, "freeze gate", src.Frozen)
	gate.Open()
	out := <-outCh
	fp := <-freezeCh
	for i, o := range out {
		if o.Err == nil {
			info := fp.All[i]
			if info.Generation != o.Generation || !info.Dirty {
				t.Fatalf("page %d freeze state %+v does not include write gen %d", i, info, o.Generation)
			}
		} else if !errors.Is(o.Err, ErrFrozen) {
			t.Fatalf("write %d: %v", i, o.Err)
		}
	}
}

// syncOnceGate blocks all callers until Open is called.
type syncOnceGate struct {
	once sync.Once
	ch   chan struct{}
	init sync.Once
}

func (g *syncOnceGate) channel() chan struct{} {
	g.init.Do(func() { g.ch = make(chan struct{}) })
	return g.ch
}

func (g *syncOnceGate) Wait() {
	<-g.channel()
}

func (g *syncOnceGate) Release() { g.Open() }
func (g *syncOnceGate) Open() {
	g.once.Do(func() { close(g.channel()) })
}
