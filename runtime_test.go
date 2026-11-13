package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestFramesKeepTheirPinnedGenerationAfterPublication(t *testing.T) {
	old := makeUnit(t, "u", 1, 0, "A")
	newer := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{old}, nil)
	mb := mustManifest(t, []*UnitVersion{newer}, ma)

	p, _ := NewPublisher(ma)
	rt := NewRuntime(p)
	oldFrame, err := rt.Enter("u")
	if err != nil {
		t.Fatal(err)
	}
	oldFrame.SetPC(0x20)
	oldFrame.SetReturnPC(0x30)

	if err := p.RegisterThread(&FrameThread{Name: "old-thread", Frame: oldFrame}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Publish(context.Background(), mb); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := p.UnregisterThread("old-thread"); err != nil {
		t.Fatal(err)
	}
	newFrame, err := rt.Enter("u")
	if err != nil {
		t.Fatal(err)
	}

	oldMD, err := rt.MetadataAt(oldFrame, 0x10)
	if err != nil {
		t.Fatal(err)
	}
	newMD, err := rt.MetadataAt(newFrame, 0x10)
	if err != nil {
		t.Fatal(err)
	}
	if oldMD.Generation != 1 || oldMD.ManifestRoot != ma.Root() {
		t.Fatalf("old frame generation/root = %d/%s", oldMD.Generation, oldMD.ManifestRoot)
	}
	if newMD.Generation != 2 || newMD.ManifestRoot != mb.Root() {
		t.Fatalf("new frame generation/root = %d/%s", newMD.Generation, newMD.ManifestRoot)
	}
	if oldMD.StackMap.FrameSize != 8 || newMD.StackMap.FrameSize != 16 {
		t.Fatalf("stack maps mixed: old=%d new=%d", oldMD.StackMap.FrameSize, newMD.StackMap.FrameSize)
	}
	if !oldMD.HasException || oldMD.Exception.HandlerPC != 0x30 {
		t.Fatalf("old exception metadata = %+v", oldMD.Exception)
	}
	if !newMD.HasException || newMD.Exception.HandlerPC != 0x50 {
		t.Fatalf("new exception metadata = %+v", newMD.Exception)
	}
	if !oldMD.HasCallTarget || oldMD.CallTarget.Offset != 1 {
		t.Fatalf("old call target = %+v", oldMD.CallTarget)
	}
	if !newMD.HasCallTarget || newMD.CallTarget.Offset != 2 {
		t.Fatalf("new call target = %+v", newMD.CallTarget)
	}
	if oldMD.Digests.StackMap != old.Digests().StackMap ||
		oldMD.Digests.ExceptionTable != old.Digests().ExceptionTable ||
		oldMD.Digests.CallTargets != old.Digests().CallTargets {
		t.Fatal("old frame metadata does not match its pinned version")
	}
	if newMD.Digests.Code != newer.Digests().Code {
		t.Fatal("new frame does not use the new code digest")
	}

	if err := p.TryReclaim(ma); !errors.Is(err, ErrLeaseOutstanding) {
		t.Fatalf("reclaim with live old frame = %v", err)
	}
	if err := oldFrame.Exit(); err != nil {
		t.Fatal(err)
	}
	if err := p.TryReclaim(ma); err != nil {
		t.Fatalf("reclaim after old frame exit: %v", err)
	}
	if _, err := oldFrame.MetadataAt(0x10); !errors.Is(err, ErrFrameExited) {
		t.Fatalf("exited frame metadata error = %v", err)
	}
	_ = newFrame.Exit()
}

func TestRecursiveFramesAndOSRStayOnOldGeneration(t *testing.T) {
	old := makeUnit(t, "u", 1, 0, "A")
	newer := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{old}, nil)
	mb := mustManifest(t, []*UnitVersion{newer}, ma)

	p, _ := NewPublisher(ma)
	rt := NewRuntime(p)
	root, err := rt.Enter("u")
	if err != nil {
		t.Fatal(err)
	}
	root.SetPC(0x20)
	root.SetReturnPC(0x30)
	if _, err := p.Publish(context.Background(), mb); err != nil {
		t.Fatal(err)
	}

	recursive, err := rt.Call(root, "u")
	if err != nil {
		t.Fatal(err)
	}
	osr, err := rt.OSRFrom(root, 0x10)
	if err != nil {
		t.Fatal(err)
	}
	externalOSR, err := rt.EnterOSR("u", 0x10)
	if err != nil {
		t.Fatal(err)
	}

	for name, f := range map[string]*Frame{"recursive": recursive, "osr": osr} {
		md, err := f.MetadataAt(0x10)
		if err != nil {
			t.Fatal(err)
		}
		if md.Generation != 1 || md.ManifestRoot != ma.Root() {
			t.Fatalf("%s frame generation/root = %d/%s", name, md.Generation, md.ManifestRoot)
		}
		if md.StackMap.FrameSize != 8 {
			t.Fatalf("%s stack map frame size = %d, want old 8", name, md.StackMap.FrameSize)
		}
	}
	extMD, err := externalOSR.MetadataAt(0x10)
	if err != nil {
		t.Fatal(err)
	}
	if extMD.Generation != 2 || extMD.ManifestRoot != mb.Root() {
		t.Fatalf("external OSR generation/root = %d/%s", extMD.Generation, extMD.ManifestRoot)
	}
	if got := p.LeaseCounts(ma).Frames; got != 3 {
		t.Fatalf("old frame leases = %d, want 3", got)
	}

	_ = recursive.Exit()
	_ = osr.Exit()
	if err := p.TryReclaim(ma); !errors.Is(err, ErrLeaseOutstanding) {
		t.Fatalf("reclaim with root old frame = %v", err)
	}
	_ = root.Exit()
	if err := p.TryReclaim(ma); err != nil {
		t.Fatalf("reclaim after all old frames exit: %v", err)
	}
	_ = externalOSR.Exit()
}

func TestLateSamplerPinsRetiredManifestUntilReleased(t *testing.T) {
	old := makeUnit(t, "u", 1, 0, "A")
	newer := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{old}, nil)
	mb := mustManifest(t, []*UnitVersion{newer}, ma)

	p, _ := NewPublisher(ma)
	rt := NewRuntime(p)
	frame, err := rt.Enter("u")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Publish(context.Background(), mb); err != nil {
		t.Fatal(err)
	}

	// The sampler starts after the swap and still has to walk the retired
	// generation of the live old frame.
	late, err := p.PinSampler(ma)
	if err != nil {
		t.Fatalf("late sampler pin: %v", err)
	}
	if err := frame.Exit(); err != nil {
		t.Fatal(err)
	}
	err = p.TryReclaim(ma)
	if !errors.Is(err, ErrLeaseOutstanding) {
		t.Fatalf("reclaim with late sampler = %v", err)
	}
	if got := p.LeaseCounts(ma).Samplers; got != 1 {
		t.Fatalf("sampler leases = %d, want 1", got)
	}
	late.Release()
	if err := p.TryReclaim(ma); err != nil {
		t.Fatalf("reclaim after sampler release: %v", err)
	}
	if _, err := p.PinSampler(ma); !errors.Is(err, ErrManifestReclaimed) {
		t.Fatalf("sampler after reclaim error = %v", err)
	}
}

func TestSamplerWalkingFramesUsesEachFrameGeneration(t *testing.T) {
	old := makeUnit(t, "u", 1, 0, "A")
	newer := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{old}, nil)
	mb := mustManifest(t, []*UnitVersion{newer}, ma)

	p, _ := NewPublisher(ma)
	rt := NewRuntime(p)
	oldFrame, err := rt.Enter("u")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Publish(context.Background(), mb); err != nil {
		t.Fatal(err)
	}
	newFrame, err := rt.Enter("u")
	if err != nil {
		t.Fatal(err)
	}

	samples, err := rt.SampleAt(0x10)
	if err != nil {
		t.Fatalf("SampleAt: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("samples = %d, want 2", len(samples))
	}
	byFrame := map[uint64]FrameMetadata{}
	for _, s := range samples {
		byFrame[s.FrameID] = s.Metadata
	}
	if md := byFrame[oldFrame.ID()]; md.Generation != 1 || md.ManifestRoot != ma.Root() || md.StackMap.FrameSize != 8 {
		t.Fatalf("old-frame sample = gen %d root %s stack %d", md.Generation, md.ManifestRoot, md.StackMap.FrameSize)
	}
	if md := byFrame[newFrame.ID()]; md.Generation != 2 || md.ManifestRoot != mb.Root() || md.StackMap.FrameSize != 16 {
		t.Fatalf("new-frame sample = gen %d root %s stack %d", md.Generation, md.ManifestRoot, md.StackMap.FrameSize)
	}
	_ = oldFrame.Exit()
	if err := p.TryReclaim(ma); err != nil {
		t.Fatalf("sampler leaked a lease: %v", err)
	}
	_ = newFrame.Exit()
}

func TestGenerationNumberReuseDoesNotAliasOldFrames(t *testing.T) {
	old := makeUnit(t, "u", 1, 0, "A")
	reuse1 := makeUnit(t, "u", 5, 1, "B")
	reuse2 := makeUnit(t, "u", 5, 5, "C")
	ma := mustManifest(t, []*UnitVersion{old}, nil)
	mb := mustManifest(t, []*UnitVersion{reuse1}, ma)
	mc := mustManifest(t, []*UnitVersion{reuse2}, mb)
	if mb.Root() == mc.Root() {
		t.Fatal("generation reuse collapsed two distinct manifests")
	}
	if reuse1.Generation() != reuse2.Generation() {
		t.Fatal("fixture did not reuse the generation number")
	}

	p, _ := NewPublisher(ma)
	rt := NewRuntime(p)
	if _, err := p.Publish(context.Background(), mb); err != nil {
		t.Fatal(err)
	}
	first, err := rt.Enter("u")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Publish(context.Background(), mc); err != nil {
		t.Fatal(err)
	}
	second, err := rt.Enter("u")
	if err != nil {
		t.Fatal(err)
	}

	firstMD, _ := first.MetadataAt(0x10)
	secondMD, _ := second.MetadataAt(0x10)
	if firstMD.Generation != 5 || secondMD.Generation != 5 {
		t.Fatalf("generations = %d/%d, want 5/5", firstMD.Generation, secondMD.Generation)
	}
	if firstMD.ContentRoot == secondMD.ContentRoot {
		t.Fatal("reused generation aliased content")
	}
	if firstMD.ManifestRoot != mb.Root() || secondMD.ManifestRoot != mc.Root() {
		t.Fatal("reused generation aliased manifests")
	}
	if firstMD.StackMap.FrameSize != 16 || secondMD.StackMap.FrameSize != 24 {
		t.Fatalf("reused generation metadata mixed: %d/%d", firstMD.StackMap.FrameSize, secondMD.StackMap.FrameSize)
	}
	if firstMD.CallTarget.Offset != 2 || secondMD.CallTarget.Offset != 3 {
		t.Fatalf("reused generation call targets mixed: %d/%d", firstMD.CallTarget.Offset, secondMD.CallTarget.Offset)
	}

	if err := p.TryReclaim(mb); !errors.Is(err, ErrLeaseOutstanding) {
		t.Fatalf("reclaim first reused generation = %v", err)
	}
	_ = first.Exit()
	if err := p.TryReclaim(mb); err != nil {
		t.Fatalf("reclaim first reused generation after exit: %v", err)
	}
	secondMD, _ = second.MetadataAt(0x10)
	if secondMD.StackMap.FrameSize != 24 {
		t.Fatal("live frame on second reused generation was disturbed")
	}
	_ = second.Exit()
}

func TestNewRuntimeAfterPublicationUsesNewEntryTable(t *testing.T) {
	old := makeUnit(t, "u", 1, 0, "A")
	newer := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{old}, nil)
	mb := mustManifest(t, []*UnitVersion{newer}, ma)
	p, _ := NewPublisher(ma)
	if _, err := p.Publish(context.Background(), mb); err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(p)
	f, err := rt.Enter("u")
	if err != nil {
		t.Fatal(err)
	}
	if f.Generation() != 2 || f.ManifestRoot() != mb.Root() {
		t.Fatalf("new runtime frame = gen %d root %s", f.Generation(), f.ManifestRoot())
	}
	_ = f.Exit()
}

func TestConcurrentEnterDuringPublishKeepsGenerationConsistent(t *testing.T) {
	old := makeUnit(t, "u", 1, 0, "A")
	newer := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{old}, nil)
	mb := mustManifest(t, []*UnitVersion{newer}, ma)
	p, _ := NewPublisher(ma)
	rt := NewRuntime(p)

	pinnedOld, err := rt.Enter("u")
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 64
	start := make(chan struct{})
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			f, err := rt.Enter("u")
			if err != nil {
				errs <- err
				return
			}
			defer f.Exit()
			md, err := f.MetadataAt(0x10)
			if err != nil {
				errs <- err
				return
			}
			switch md.Generation {
			case 1:
				if md.StackMap.FrameSize != 8 || md.CallTarget.Offset != 1 ||
					md.Digests.Code != old.Digests().Code {
					errs <- fmt.Errorf("old generation observed mixed content: %+v", md)
				}
			case 2:
				if md.StackMap.FrameSize != 16 || md.CallTarget.Offset != 2 ||
					md.Digests.Code != newer.Digests().Code {
					errs <- fmt.Errorf("new generation observed mixed content: %+v", md)
				}
			default:
				errs <- fmt.Errorf("unexpected generation %d", md.Generation)
			}
		}()
	}
	close(start)
	publishErr := make(chan error, 1)
	go func() {
		_, err := p.Publish(context.Background(), mb)
		publishErr <- err
	}()
	wg.Wait()
	if err := <-publishErr; err != nil {
		t.Fatalf("Publish: %v", err)
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	md, err := pinnedOld.MetadataAt(0x10)
	if err != nil {
		t.Fatal(err)
	}
	if md.Generation != 1 || md.StackMap.FrameSize != 8 || md.Digests.Code != old.Digests().Code {
		t.Fatal("pre-existing old frame changed generation")
	}
	_ = pinnedOld.Exit()
}
