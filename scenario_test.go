package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func digest(b []byte) [32]byte { return sha256.Sum256(b) }

func TestSelfDecryptingPayloadHasCurrentIdentity(t *testing.T) {
	source := Source{Name: "packed-sample.bin", Digest: digest([]byte("packed sample"))}
	tracker := NewTracker()

	must(t, tracker.Map(1, source, PermRead|PermWrite))

	ciphertext := bytes.Repeat([]byte{0xA5}, PageSize)
	must(t, tracker.Write(1, 0, ciphertext))

	decrypted := []byte{
		0x55, 0x48, 0x89, 0xE5, 0x48, 0x83, 0xEC, 0x20,
		0x48, 0x8D, 0x3D, 0x00, 0x00, 0x00, 0x00, 0xC3,
	}
	must(t, tracker.Write(1, 16, decrypted))
	must(t, tracker.Protect(1, PermRead|PermExecute))

	exec, err := tracker.Execute(1, 16, len(decrypted))
	must(t, err)
	must(t, exec.Verify())

	if exec.PhysicalID == 0 {
		t.Fatal("execution has no physical page identity")
	}
	if exec.Generation != 1 || exec.VirtualGeneration != 1 {
		t.Fatalf("virtual generation = %d/%d, want 1", exec.Generation, exec.VirtualGeneration)
	}
	if exec.PhysicalGeneration != 2 {
		t.Fatalf("physical generation = %d, want 2", exec.PhysicalGeneration)
	}
	if exec.Digest != digest(exec.Snapshot) {
		t.Fatal("reported digest does not cover the full current page snapshot")
	}
	if !bytes.Equal(exec.Code, decrypted) {
		t.Fatalf("executed bytes = %x, want %x", exec.Code, decrypted)
	}
	if !bytes.Equal(exec.Snapshot[16:16+len(decrypted)], decrypted) {
		t.Fatal("executed bytes are not all covered by the contemporary snapshot")
	}
	if exec.SourceDigest != source.Digest || exec.SourceName != source.Name || exec.FileSource != source.Name {
		t.Fatalf("source provenance lost: %+v", exec.Source)
	}
	if len(exec.WriteChain) != 2 || len(exec.WriteIntervals) != 2 {
		t.Fatalf("provenance = %d writes, %d intervals, want 2", len(exec.WriteChain), len(exec.WriteIntervals))
	}
	if exec.WriteIntervals[0] != (WriteInterval{Offset: 0, Length: PageSize}) {
		t.Fatalf("first write interval = %+v", exec.WriteIntervals[0])
	}
	if exec.WriteIntervals[1] != (WriteInterval{Offset: 16, Length: len(decrypted)}) {
		t.Fatalf("second write interval = %+v", exec.WriteIntervals[1])
	}
	if !bytes.Equal(exec.WriteChain[1].Bytes, decrypted) {
		t.Fatal("write provenance does not retain the decrypted bytes")
	}
	if exec.Verdict != VerdictUnknown || exec.Detection.Evaluated {
		t.Fatalf("unpacking alone must not create a verdict: %+v", exec.Detection)
	}
}

func TestSharedPhysicalPageAliasesAndWXAggregate(t *testing.T) {
	tracker := NewTracker()
	source := Source{Name: "alias.bin", Digest: digest([]byte("alias"))}

	must(t, tracker.Map(10, source, PermRead|PermWrite))
	first := []byte{0x90, 0x90, 0x90, 0x90, 0xC3}
	must(t, tracker.Write(10, 0, first))
	must(t, tracker.Remap(11, 10))

	state10, ok := tracker.Inspect(10)
	if !ok {
		t.Fatal("page 10 missing")
	}
	state11, ok := tracker.Inspect(11)
	if !ok {
		t.Fatal("page 11 missing")
	}
	if state10.PhysicalID != state11.PhysicalID {
		t.Fatalf("aliases have different physical pages: %d vs %d", state10.PhysicalID, state11.PhysicalID)
	}
	if got := tracker.PhysicalAliases(state10.PhysicalID); len(got) != 2 {
		t.Fatalf("physical aliases = %v, want two", got)
	}

	// The first alias is still writable, so making the second executable
	// would violate aggregate W^X.
	wantErr(t, tracker.Protect(11, PermRead|PermExecute), ErrWXConflict)

	must(t, tracker.Protect(10, PermNone))
	second := []byte{0xCC, 0xCC}
	must(t, tracker.Write(11, 8, second))
	must(t, tracker.Protect(11, PermRead|PermExecute))

	exec11, err := tracker.Execute(11, 0, 10)
	must(t, err)
	must(t, exec11.Verify())
	if !bytes.Equal(exec11.Snapshot[:5], first) || !bytes.Equal(exec11.Snapshot[8:10], second) {
		t.Fatal("alias did not observe writes made through its sibling")
	}
	if len(exec11.WriteChain) != 2 {
		t.Fatalf("shared write provenance = %d records, want 2", len(exec11.WriteChain))
	}
	if exec11.WriteChain[0].PageID != 10 || exec11.WriteChain[1].PageID != 11 {
		t.Fatalf("provenance lost writing alias: %+v", exec11.WriteChain)
	}

	// A second alias can independently enter execute state over the same
	// physical page and receives its own virtual generation.
	must(t, tracker.Remap(12, exec11.PhysicalID, PermRead|PermExecute))
	exec12, err := tracker.Execute(12, 0, 10)
	must(t, err)
	must(t, exec12.Verify())
	if exec12.PhysicalID != exec11.PhysicalID {
		t.Fatal("remapped alias points at a different physical page")
	}
	if exec12.PageID != 12 || exec12.VirtualGeneration == 0 {
		t.Fatal("remapped alias has no independent virtual identity")
	}
	if exec12.VirtualGeneration == exec11.VirtualGeneration {
		t.Fatal("aliases share a virtual generation token")
	}
	if exec12.Digest != exec11.Digest {
		t.Fatal("aliases of the same unchanged physical page disagree on identity")
	}

	// Adding W to any alias while another alias is executable must fail.
	wantErr(t, tracker.Protect(10, PermWrite), ErrWXConflict)
}

func TestUnmapThenReuseSamePageIDCreatesNewGeneration(t *testing.T) {
	tracker := NewTracker()
	sourceA := Source{Name: "a.bin", Digest: digest([]byte("a"))}
	sourceB := Source{Name: "b.bin", Digest: digest([]byte("b"))}

	must(t, tracker.Map(20, sourceA, PermRead|PermWrite))
	must(t, tracker.Write(20, 0, []byte{1, 2, 3, 4}))
	must(t, tracker.Protect(20, PermRead|PermExecute))
	oldExec, err := tracker.Execute(20, 0, 4)
	must(t, err)

	must(t, tracker.Unmap(20))
	wantErr(t, tracker.Unmap(20), ErrNotMapped)
	if _, err := tracker.Execute(20, 0, 1); !errors.Is(err, ErrNotMapped) {
		t.Fatalf("execute after unmap error = %v, want ErrNotMapped", err)
	}

	must(t, tracker.Map(20, sourceB, PermRead|PermWrite))
	reused, ok := tracker.Inspect(20)
	if !ok {
		t.Fatal("reused page missing")
	}
	if reused.Generation != oldExec.Generation+1 {
		t.Fatalf("reused generation = %d, want %d", reused.Generation, oldExec.Generation+1)
	}
	if reused.PhysicalID == oldExec.PhysicalID {
		t.Fatal("reuse silently retained the old physical page")
	}
	if reused.PhysicalGeneration != 0 || reused.HasIdentity {
		t.Fatalf("fresh mapping leaked old state: %+v", reused)
	}
	if reused.Source.Digest != sourceB.Digest {
		t.Fatal("fresh mapping did not receive the new source")
	}
	snapshot, ok := tracker.Snapshot(20)
	if !ok || !bytes.Equal(snapshot, make([]byte, PageSize)) {
		t.Fatal("fresh mapping retained the previous bytes")
	}

	must(t, tracker.Protect(20, PermRead|PermExecute))
	freshExec, err := tracker.Execute(20, 0, PageSize)
	must(t, err)
	if len(freshExec.WriteChain) != 0 {
		t.Fatal("fresh generation inherited the old write chain")
	}
	if freshExec.Digest != digest(make([]byte, PageSize)) {
		t.Fatal("fresh generation digest covers stale bytes")
	}

	// A second Map on an active ID is also a new generation, never a reuse.
	must(t, tracker.Map(20, sourceA, PermRead|PermWrite))
	again, _ := tracker.Inspect(20)
	if again.Generation != reused.Generation+1 {
		t.Fatalf("second Map generation = %d, want %d", again.Generation, reused.Generation+1)
	}
	if again.PhysicalID == reused.PhysicalID {
		t.Fatal("second Map reused the physical page")
	}
}

func TestModifyOneByteAndRestoreRefreshesIdentityAndCache(t *testing.T) {
	tracker := NewTracker()
	calls := 0
	must(t, tracker.SetRule(func(d [32]byte, snapshot []byte) bool {
		calls++
		return snapshot[0] == 0x42
	}))
	must(t, tracker.Map(30, "mutable.bin", PermRead|PermWrite))

	original := bytes.Repeat([]byte{0x01}, PageSize)
	must(t, tracker.Write(30, 0, original))
	must(t, tracker.Protect(30, PermRead|PermExecute))

	first, err := tracker.Execute(30, 0, 8)
	must(t, err)
	firstDigest := first.Digest
	if calls != 1 {
		t.Fatalf("rule calls = %d, want 1", calls)
	}
	if first.Verdict != VerdictBenign || first.Detection.Cached {
		t.Fatalf("first detection = %+v", first.Detection)
	}

	second, err := tracker.Execute(30, 0, 8)
	must(t, err)
	if calls != 1 {
		t.Fatalf("cache miss on unchanged identity: calls = %d", calls)
	}
	if !second.Detection.Cached || second.Verdict != VerdictBenign {
		t.Fatalf("cached detection = %+v", second.Detection)
	}

	// The model has no write-xor-execute violation: leave X before writing.
	must(t, tracker.Protect(30, PermRead|PermWrite))
	must(t, tracker.Write(30, 0, []byte{0x42}))
	must(t, tracker.Protect(30, PermRead|PermExecute))

	modified, err := tracker.Execute(30, 0, 8)
	must(t, err)
	if modified.Digest == firstDigest {
		t.Fatal("changing a byte did not change the execution digest")
	}
	if calls != 2 || modified.Verdict != VerdictMalicious {
		t.Fatalf("modified execution calls/verdict = %d/%s", calls, modified.Verdict)
	}
	if len(modified.WriteChain) != 1 || modified.WriteChain[0].Sequence != 2 {
		t.Fatalf("modified write chain = %+v", modified.WriteChain)
	}

	must(t, tracker.Protect(30, PermRead|PermWrite))
	must(t, tracker.Write(30, 0, []byte{0x01}))
	must(t, tracker.Protect(30, PermRead|PermExecute))

	restored, err := tracker.Execute(30, 0, 8)
	must(t, err)
	if restored.Digest != firstDigest {
		t.Fatal("restored byte did not restore the original digest")
	}
	if calls != 3 {
		t.Fatalf("restored generation reused a stale cache entry: calls = %d", calls)
	}
	if restored.DetectionKey.PhysicalGeneration != restored.PhysicalGeneration {
		t.Fatal("cache key does not include the physical generation")
	}
	if len(restored.WriteChain) != 1 || restored.WriteChain[0].Sequence != 3 {
		t.Fatalf("restored write chain = %+v", restored.WriteChain)
	}

	if _, err := tracker.Execute(30, 0, 8); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("unchanged restored generation missed cache: calls = %d", calls)
	}
}

func TestCrossPageExecutionHasPerPageIdentity(t *testing.T) {
	tracker := NewTracker()
	must(t, tracker.Map(40, Source{Name: "page-a", Digest: digest([]byte("a"))}, PermRead|PermWrite))
	must(t, tracker.Map(41, Source{Name: "page-b", Digest: digest([]byte("b"))}, PermRead|PermWrite))

	codeA := []byte{0x55, 0x48, 0x89, 0xE5, 0xC3}
	codeB := []byte{0x31, 0xC0, 0x48, 0x83, 0xC0, 0x01, 0xC3}
	must(t, tracker.Write(40, 10, codeA))
	must(t, tracker.Write(41, 30, codeB))
	must(t, tracker.Protect(40, PermRead|PermExecute))
	must(t, tracker.Protect(41, PermRead|PermExecute))

	execA, err := tracker.Execute(40, 10, len(codeA))
	must(t, err)
	execB, err := tracker.Execute(41, 30, len(codeB))
	must(t, err)
	must(t, execA.Verify())
	must(t, execB.Verify())

	if bytes.Equal(execA.Digest[:], execB.Digest[:]) {
		t.Fatal("different pages unexpectedly share one identity digest")
	}
	if execA.PhysicalID == execB.PhysicalID {
		t.Fatal("cross-page execution collapsed two physical pages")
	}
	combined := append(append([]byte(nil), execA.Code...), execB.Code...)
	if !bytes.Equal(combined, append(append([]byte(nil), codeA...), codeB...)) {
		t.Fatalf("cross-page execution bytes = %x", combined)
	}
	for i, b := range execA.Code {
		if execA.Snapshot[execA.Offset+i] != b {
			t.Fatal("executed byte is not covered by page A's contemporary digest")
		}
	}
	for i, b := range execB.Code {
		if execB.Snapshot[execB.Offset+i] != b {
			t.Fatal("executed byte is not covered by page B's contemporary digest")
		}
	}
}

func TestRuleGetsImmutableSnapshotAndCacheSharedByAliases(t *testing.T) {
	tracker := NewTracker()
	calls := 0
	var observed []byte
	must(t, tracker.SetRule(func(d [32]byte, snapshot []byte) bool {
		calls++
		observed = append([]byte(nil), snapshot...)
		snapshot[0] ^= 0xFF
		return true
	}))
	must(t, tracker.Map(50, "immutable.bin", PermRead|PermWrite))
	content := bytes.Repeat([]byte{0x11}, PageSize)
	must(t, tracker.Write(50, 0, content))
	must(t, tracker.Protect(50, PermRead|PermExecute))

	exec, err := tracker.Execute(50, 0, 4)
	must(t, err)
	if calls != 1 {
		t.Fatalf("rule calls = %d, want 1", calls)
	}
	if !bytes.Equal(observed, content) {
		t.Fatal("rule did not receive the current snapshot")
	}
	if !bytes.Equal(exec.Snapshot, content) {
		t.Fatal("rule mutation changed the execution identity snapshot")
	}
	current, ok := tracker.Snapshot(50)
	if !ok || !bytes.Equal(current, content) {
		t.Fatal("rule mutation changed the backing page")
	}

	// Two aliases share a cache entry because the key is physical identity,
	// physical generation, and digest, not the virtual page generation.
	must(t, tracker.Remap(51, 50))
	execAlias, err := tracker.Execute(51, 0, 4)
	must(t, err)
	if calls != 1 {
		t.Fatalf("alias did not share detection cache: calls = %d", calls)
	}
	if !execAlias.Detection.Cached {
		t.Fatalf("alias detection = %+v, want cached", execAlias.Detection)
	}
	if !bytes.Equal(execAlias.Snapshot, content) {
		t.Fatal("cached execution snapshot was not immutable")
	}
}

func TestPermissionsRangesAndEventAdapter(t *testing.T) {
	tracker := NewTracker()

	wantErr(t, tracker.Map(60, "bad", PermWrite|PermExecute), ErrWXConflict)
	wantErr(t, tracker.Map(60, struct{}{}, PermWrite), ErrInvalidSource)

	must(t, tracker.Map(60, "ok", PermRead|PermWrite))
	wantErr(t, tracker.Write(60, 60, []byte{1, 2, 3, 4, 5}), ErrOutOfRange)
	wantErr(t, tracker.Write(60, -1, []byte{1}), ErrOutOfRange)
	_, err := tracker.Execute(60, 0, 1)
	wantErr(t, err, ErrPermissionDenied)
	wantErr(t, tracker.Protect(60, PermWrite|PermExecute), ErrWXConflict)

	must(t, tracker.Map(61, "readonly", PermRead))
	wantErr(t, tracker.Write(61, 0, []byte{1}), ErrPermissionDenied)
	_, err = tracker.Execute(61, 0, 1)
	wantErr(t, err, ErrPermissionDenied)

	// Direct X mapping still requires a generated current identity.
	must(t, tracker.Map(62, "exec-only", PermRead|PermExecute))
	_, err = tracker.Execute(62, 64, 1)
	wantErr(t, err, ErrOutOfRange)
	_, err = tracker.Execute(62, 0, 0)
	wantErr(t, err, ErrOutOfRange)
	exec, err := tracker.Execute(62, 0, 1)
	must(t, err)
	if exec.Digest != digest(make([]byte, PageSize)) {
		t.Fatal("direct X mapping lacks an identity over its initial bytes")
	}

	// Tagged and typed events drive the same state machine.
	if _, err := tracker.Apply(Event{
		Kind:   EventMap,
		PageID: 63,
		Source: "event.bin",
		Perm:   PermRead | PermWrite,
		Data:   []byte{0x90, 0xC3},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.Apply(WriteEvent{PageID: 63, Offset: 2, Data: []byte{0xCC}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.Apply(ProtectEvent{PageID: 63, Perm: PermRead | PermExecute}); err != nil {
		t.Fatal(err)
	}
	eventExec, err := tracker.Apply(ExecuteEvent{PageID: 63, Offset: 0, Length: 4})
	must(t, err)
	if !bytes.Equal(eventExec.Code, []byte{0x90, 0xC3, 0xCC, 0x00}) {
		t.Fatalf("event execution = %x", eventExec.Code)
	}
}

func TestUnpackedCodeIsNotAutomaticallyMalicious(t *testing.T) {
	tracker := NewTracker()
	calls := 0
	must(t, tracker.SetRule(func(d [32]byte, snapshot []byte) bool {
		calls++
		return false
	}))
	must(t, tracker.Map(70, "unpacked.bin", PermRead|PermWrite))
	must(t, tracker.Write(70, 0, bytes.Repeat([]byte{0x90}, PageSize)))
	must(t, tracker.Protect(70, PermRead|PermExecute))

	exec, err := tracker.Execute(70, 0, PageSize)
	must(t, err)
	if calls != 1 || exec.Verdict != VerdictBenign {
		t.Fatalf("unpacked code verdict = %s after %d calls", exec.Verdict, calls)
	}
	if exec.Verdict == VerdictMalicious {
		t.Fatal("being unpacked was treated as a malicious conclusion")
	}
}
