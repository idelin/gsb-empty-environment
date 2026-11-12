package forkwatch

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func digestHex(t *testing.T, s string) Digest {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad digest %q: %v", s, err)
	}
	return Digest(b)
}

type stubVerifier struct {
	mu    sync.Mutex
	calls int
}

func (s *stubVerifier) Verify(payload, signature []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if bytes.Contains(payload, []byte("bad-signature")) {
		return errors.New("signature mismatch")
	}
	return nil
}

func (s *stubVerifier) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func newMessage(generation, sequence uint64, parent, content Digest) SignedMessage {
	payload := []byte(fmt.Sprintf("g%d:s%d:%s", generation, sequence, content.String()))
	return SignedMessage{
		Generation:    generation,
		Sequence:      sequence,
		ParentDigest:  parent,
		ContentDigest: content,
		Payload:       payload,
		Signature:     append([]byte("sig:"), content...),
	}
}

func findStatus(t *testing.T, m *Monitor, content Digest) Status {
	t.Helper()
	for _, rs := range m.Records() {
		if rs.Record.ContentDigest.Equal(content) {
			return rs.Status
		}
	}
	t.Fatalf("record %s not found", content)
	return StatusUnknown
}

func snapshotsEqual(a, b Snapshot) bool {
	if len(a.Records) != len(b.Records) {
		return false
	}
	for i := range a.Records {
		ar, br := a.Records[i], b.Records[i]
		if ar.Generation != br.Generation || ar.Sequence != br.Sequence {
			return false
		}
		if !ar.ContentDigest.Equal(br.ContentDigest) || !ar.ParentDigest.Equal(br.ParentDigest) {
			return false
		}
	}
	return true
}

func TestObserveRejectsInvalidSignatureAndDigest(t *testing.T) {
	verifier := &stubVerifier{}
	m := NewMonitor("shard-a", verifier)

	content := digestHex(t, "aa")
	bad := newMessage(1, 0, nil, content)
	bad.Payload = []byte("bad-signature")
	if err := m.Observe(bad); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
	if len(m.Records()) != 0 {
		t.Fatalf("rejected message must not be stored")
	}

	noDigest := newMessage(1, 0, nil, nil)
	if err := m.Observe(noDigest); !errors.Is(err, ErrInvalidDigest) {
		t.Fatalf("expected ErrInvalidDigest, got %v", err)
	}
}

func TestRootAndLinkedChain(t *testing.T) {
	m := NewMonitor("shard-a", &stubVerifier{})
	root := digestHex(t, "01")
	child := digestHex(t, "02")

	if err := m.Observe(newMessage(1, 0, nil, root)); err != nil {
		t.Fatal(err)
	}
	if got := m.Status(root); got != StatusLinked {
		t.Fatalf("root status = %s, want linked", got)
	}

	if err := m.Observe(newMessage(1, 1, root, child)); err != nil {
		t.Fatal(err)
	}
	if got := m.Status(child); got != StatusLinked {
		t.Fatalf("child status = %s, want linked", got)
	}
	if m.HasConflict() {
		t.Fatal("linear chain must not report conflicts")
	}
}

func TestMissingParentIsOnlyPending(t *testing.T) {
	m := NewMonitor("shard-a", &stubVerifier{})
	missing := digestHex(t, "99")
	orphan := digestHex(t, "03")

	if err := m.Observe(newMessage(1, 5, missing, orphan)); err != nil {
		t.Fatal(err)
	}
	if got := m.Status(orphan); got != StatusPending {
		t.Fatalf("orphan status = %s, want pending", got)
	}
	if m.HasConflict() {
		t.Fatal("a missing parent is not fork evidence")
	}
	pending := m.Pending()
	if len(pending) != 1 || !pending[0].ContentDigest.Equal(orphan) {
		t.Fatalf("pending = %v, want only %s", pending, orphan)
	}

	// The parent arrives later: pending resolves to linked without replay.
	if err := m.Observe(newMessage(1, 4, digestHex(t, "98"), missing)); err != nil {
		t.Fatal(err)
	}
	if got := m.Status(missing); got != StatusPending {
		t.Fatalf("newly arrived parent is itself pending, got %s", got)
	}
	if got := m.Status(orphan); got != StatusLinked {
		t.Fatalf("orphan status after parent arrival = %s, want linked", got)
	}
}

func TestSiblingWithSameParentIsConflict(t *testing.T) {
	m := NewMonitor("shard-a", &stubVerifier{})
	parent := digestHex(t, "10")
	left := digestHex(t, "20")
	right := digestHex(t, "21")

	for _, msg := range []SignedMessage{
		newMessage(2, 1, parent, left),
		newMessage(2, 1, parent, right),
	} {
		if err := m.Observe(msg); err != nil {
			t.Fatal(err)
		}
	}

	if !m.HasConflict() {
		t.Fatal("same parent with different content must be a conflict")
	}
	if got := m.Status(left); got != StatusConflict {
		t.Fatalf("left status = %s, want conflict", got)
	}
	if got := m.Status(right); got != StatusConflict {
		t.Fatalf("right status = %s, want conflict", got)
	}

	conflicts := m.Conflicts()
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(conflicts))
	}
	c := conflicts[0]
	if c.Generation != 2 || c.Sequence != 1 || !c.ParentDigest.Equal(parent) {
		t.Fatalf("conflict metadata wrong: %+v", c)
	}
	if len(c.ContentDigests) != 2 ||
		!c.ContentDigests[0].Equal(left) || !c.ContentDigests[1].Equal(right) {
		t.Fatalf("conflict digests = %v", c.ContentDigests)
	}
}

func TestConflictingRoots(t *testing.T) {
	m := NewMonitor("shard-a", &stubVerifier{})
	rootA := digestHex(t, "a0")
	rootB := digestHex(t, "b0")

	if err := m.Observe(newMessage(1, 0, nil, rootA)); err != nil {
		t.Fatal(err)
	}
	if err := m.Observe(newMessage(1, 0, nil, rootB)); err != nil {
		t.Fatal(err)
	}
	if !m.HasConflict() {
		t.Fatal("two distinct roots at the same position must conflict")
	}
	if c := m.Conflicts()[0]; len(c.ParentDigest) != 0 {
		t.Fatalf("root conflict must carry empty parent digest, got %x", c.ParentDigest)
	}
}

func TestDifferentSequenceSameParentIsNotConflict(t *testing.T) {
	m := NewMonitor("shard-a", &stubVerifier{})
	parent := digestHex(t, "10")
	first := digestHex(t, "11")
	second := digestHex(t, "12")

	if err := m.Observe(newMessage(1, 1, parent, first)); err != nil {
		t.Fatal(err)
	}
	if err := m.Observe(newMessage(1, 2, parent, second)); err != nil {
		t.Fatal(err)
	}
	if m.HasConflict() {
		t.Fatal("different sequence numbers are not competing messages")
	}
}

func TestOutOfOrderDeliveryConverges(t *testing.T) {
	// A three-message chain delivered in reverse order must converge to the
	// same derived state as in-order delivery.
	digests := []Digest{
		digestHex(t, "01"),
		digestHex(t, "02"),
		digestHex(t, "03"),
	}
	messages := []SignedMessage{
		newMessage(1, 0, nil, digests[0]),
		newMessage(1, 1, digests[0], digests[1]),
		newMessage(1, 2, digests[1], digests[2]),
	}

	orderly := NewMonitor("orderly", &stubVerifier{})
	for _, msg := range messages {
		if err := orderly.Observe(msg); err != nil {
			t.Fatal(err)
		}
	}

	reversed := NewMonitor("reversed", &stubVerifier{})
	for i := len(messages) - 1; i >= 0; i-- {
		if err := reversed.Observe(messages[i]); err != nil {
			t.Fatal(err)
		}
	}

	if reversed.HasConflict() || orderly.HasConflict() {
		t.Fatal("linear chain has no conflicts regardless of delivery order")
	}
	for _, digest := range digests {
		if got, want := reversed.Status(digest), orderly.Status(digest); got != want {
			t.Fatalf("status of %s: reversed=%s orderly=%s", digest, got, want)
		}
		if got := reversed.Status(digest); got != StatusLinked {
			t.Fatalf("status of %s = %s, want linked after convergence", digest, got)
		}
	}
}

func TestDuplicateObserveIsIdempotent(t *testing.T) {
	verifier := &stubVerifier{}
	m := NewMonitor("shard-a", verifier)
	root := digestHex(t, "01")
	child := digestHex(t, "02")
	msg := newMessage(1, 1, root, child)

	for i := 0; i < 3; i++ {
		if err := m.Observe(msg); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.Records()) != 1 {
		t.Fatalf("stored records = %d, want 1", len(m.Records()))
	}
	if verifier.callCount() != 3 {
		t.Fatalf("verifier calls = %d, want 3 (duplicates are still verified)", verifier.callCount())
	}

	// Re-merging the monitor's own snapshot changes nothing.
	snap := m.Snapshot()
	if err := m.MergeSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	if len(m.Records()) != 1 {
		t.Fatalf("records after self-merge = %d, want 1", len(m.Records()))
	}
}

func TestRollbackSnapshotFromAnotherShard(t *testing.T) {
	// Two shards accepted different signed messages at the same session
	// position. Neither shard sees a fork locally; merging the rolled-back
	// snapshot surfaces the evidence.
	parent := digestHex(t, "50")
	candidateA := digestHex(t, "60")
	candidateB := digestHex(t, "61")

	shardA := NewMonitor("a", &stubVerifier{})
	shardB := NewMonitor("b", &stubVerifier{})
	if err := shardA.Observe(newMessage(3, 7, parent, candidateA)); err != nil {
		t.Fatal(err)
	}
	if err := shardB.Observe(newMessage(3, 7, parent, candidateB)); err != nil {
		t.Fatal(err)
	}
	if shardA.HasConflict() || shardB.HasConflict() {
		t.Fatal("a single branch per shard cannot conflict locally")
	}

	merged := NewMonitor("merged", &stubVerifier{})
	if err := merged.MergeSnapshot(shardA.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := merged.MergeSnapshot(shardB.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if !merged.HasConflict() {
		t.Fatal("rollback snapshot must produce conflict evidence after merge")
	}
	if got := findStatus(t, merged, candidateA); got != StatusConflict {
		t.Fatalf("candidateA = %s, want conflict", got)
	}
	if got := findStatus(t, merged, candidateB); got != StatusConflict {
		t.Fatalf("candidateB = %s, want conflict", got)
	}

	// Merging the same snapshots again is idempotent.
	before := len(merged.Records())
	if err := merged.MergeSnapshots(shardA.Snapshot(), shardB.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if len(merged.Records()) != before || len(merged.Conflicts()) != 1 {
		t.Fatal("repeated rollback merges must not change state")
	}
}

func TestMergeOrderIndependent(t *testing.T) {
	root := digestHex(t, "01")
	mid := digestHex(t, "02")
	forkA := digestHex(t, "03")
	forkB := digestHex(t, "04")

	shards := []Snapshot{
		{Shard: "a", Records: []Record{
			{Generation: 1, Sequence: 0, ContentDigest: root},
			{Generation: 1, Sequence: 1, ParentDigest: root, ContentDigest: mid},
			{Generation: 1, Sequence: 2, ParentDigest: mid, ContentDigest: forkA},
		}},
		{Shard: "b", Records: []Record{
			{Generation: 1, Sequence: 2, ParentDigest: mid, ContentDigest: forkB},
		}},
		{Shard: "c", Records: []Record{
			{Generation: 1, Sequence: 1, ParentDigest: root, ContentDigest: mid},
		}},
	}

	reference := MergeSnapshots(shards...)

	permutations := [][]int{
		{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0},
	}
	for _, perm := range permutations {
		var ordered []Snapshot
		for _, idx := range perm {
			ordered = append(ordered, shards[idx])
		}
		got := MergeSnapshots(ordered...)
		if !snapshotsEqual(got, reference) {
			t.Fatalf("permutation %v produced a different merge:\n got %v\nwant %v",
				perm, got.Records, reference.Records)
		}

		monitor := NewMonitor("check", &stubVerifier{})
		if err := monitor.MergeSnapshots(ordered...); err != nil {
			t.Fatal(err)
		}
		if len(monitor.Conflicts()) != 1 {
			t.Fatalf("permutation %v: conflicts = %d, want 1", perm, len(monitor.Conflicts()))
		}
		if findStatus(t, monitor, forkA) != StatusConflict ||
			findStatus(t, monitor, forkB) != StatusConflict {
			t.Fatalf("permutation %v: fork branches not both conflicting", perm)
		}
		if findStatus(t, monitor, mid) != StatusLinked ||
			findStatus(t, monitor, root) != StatusLinked {
			t.Fatalf("permutation %v: shared prefix must stay linked", perm)
		}
	}

	selfMerge := MergeSnapshots(reference, reference)
	if !snapshotsEqual(selfMerge, reference) {
		t.Fatal("merging a snapshot with itself must be the identity")
	}
}

func TestPendingResolvesAcrossShards(t *testing.T) {
	parent := digestHex(t, "70")
	child := digestHex(t, "71")

	holder := NewMonitor("holder", &stubVerifier{})
	if err := holder.Observe(newMessage(1, 9, parent, child)); err != nil {
		t.Fatal(err)
	}
	if got := holder.Status(child); got != StatusPending {
		t.Fatalf("child = %s, want pending before cross-shard merge", got)
	}

	other := NewMonitor("other", &stubVerifier{})
	if err := other.Observe(newMessage(1, 8, digestHex(t, "6f"), parent)); err != nil {
		t.Fatal(err)
	}

	if err := holder.MergeSnapshot(other.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if got := holder.Status(child); got != StatusLinked {
		t.Fatalf("child after parent merge = %s, want linked", got)
	}
	if len(holder.Pending()) != 1 {
		t.Fatalf("the parent's own missing ancestor stays pending, got %v", holder.Pending())
	}
}

func TestSnapshotJSONRoundTrip(t *testing.T) {
	parent := digestHex(t, "80")
	child := digestHex(t, "81")
	source := NewMonitor("json-shard", &stubVerifier{})
	if err := source.Observe(newMessage(1, 0, nil, parent)); err != nil {
		t.Fatal(err)
	}
	if err := source.Observe(newMessage(1, 1, parent, child)); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(source.Snapshot())
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	var restored Snapshot
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	reloaded := NewMonitor("reloaded", &stubVerifier{})
	if err := reloaded.MergeSnapshot(restored); err != nil {
		t.Fatal(err)
	}
	if !snapshotsEqual(reloaded.Snapshot(), source.Snapshot()) {
		t.Fatalf("reloaded snapshot differs:\n got %v\nwant %v",
			reloaded.Snapshot().Records, source.Snapshot().Records)
	}
	if got := reloaded.Status(child); got != StatusLinked {
		t.Fatalf("reloaded child status = %s, want linked", got)
	}
}

func TestConcurrentObserveAndMerge(t *testing.T) {
	m := NewMonitor("concurrent", &stubVerifier{})
	root := digestHex(t, "01")

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = m.Observe(newMessage(1, 0, nil, root))
				_ = m.Status(root)
				_ = m.Conflicts()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		snap := Snapshot{
			Shard:   "remote",
			Records: []Record{{Generation: 2, Sequence: 0, ContentDigest: digestHex(t, "02")}},
		}
		for i := 0; i < 100; i++ {
			_ = m.MergeSnapshot(snap)
			_ = m.Snapshot()
		}
	}()

	wg.Wait()

	if len(m.Records()) != 2 {
		t.Fatalf("records = %d, want 2 distinct messages", len(m.Records()))
	}
	if m.HasConflict() {
		t.Fatal("concurrent identical observations must not create conflicts")
	}
}

func TestUnknownDigestAndEmptyMergeRecords(t *testing.T) {
	m := NewMonitor("edge", &stubVerifier{})
	if got := m.Status(digestHex(t, "ff")); got != StatusUnknown {
		t.Fatalf("unknown digest status = %s, want unknown", got)
	}

	// Records without a content digest are skipped rather than poisoning state.
	snap := Snapshot{
		Shard: "remote",
		Records: []Record{
			{Generation: 1, Sequence: 0, ContentDigest: digestHex(t, "0a")},
			{Generation: 1, Sequence: 1, ContentDigest: nil},
			{Generation: 1, Sequence: 2, ContentDigest: digestHex(t, "0b")},
		},
	}
	if err := m.MergeSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	if len(m.Records()) != 2 {
		t.Fatalf("records = %d, want 2 (nil-digest record skipped)", len(m.Records()))
	}

	// Merging no snapshots yields an empty, non-nil record slice.
	empty := MergeSnapshots()
	if empty.Records == nil || len(empty.Records) != 0 {
		t.Fatalf("empty merge = %v, want empty non-nil slice", empty.Records)
	}
}

func TestAccessorsAndMultipleConflictOrdering(t *testing.T) {
	verifier := VerifierFunc(func(payload, signature []byte) error {
		if bytes.Contains(payload, []byte("bad-signature")) {
			return errors.New("nope")
		}
		return nil
	})
	if err := verifier.Verify([]byte("ok"), nil); err != nil {
		t.Fatalf("VerifierFunc accepted payload rejected: %v", err)
	}

	m := NewMonitor("named-shard", verifier)
	if got := m.Shard(); got != "named-shard" {
		t.Fatalf("shard = %q, want named-shard", got)
	}
	if got := StatusUnknown.String(); got != "unknown" {
		t.Fatalf("StatusUnknown.String() = %q", got)
	}

	// Two forks in different generations exercise conflict ordering.
	root1 := digestHex(t, "10")
	root2 := digestHex(t, "11")
	root3 := digestHex(t, "20")
	root4 := digestHex(t, "21")
	for _, msg := range []SignedMessage{
		newMessage(2, 0, nil, root3),
		newMessage(2, 0, nil, root4),
		newMessage(1, 0, nil, root1),
		newMessage(1, 0, nil, root2),
	} {
		if err := m.Observe(msg); err != nil {
			t.Fatal(err)
		}
	}

	conflicts := m.Conflicts()
	if len(conflicts) != 2 {
		t.Fatalf("conflicts = %d, want 2", len(conflicts))
	}
	if conflicts[0].Generation != 1 || conflicts[1].Generation != 2 {
		t.Fatalf("conflicts not generation-sorted: %+v", conflicts)
	}
	for _, status := range []Status{StatusLinked, StatusPending, StatusConflict, StatusUnknown} {
		if status.String() == "unknown" && status != StatusUnknown {
			t.Fatalf("unexpected unknown status: %d", status)
		}
	}

	// A second fork in the same generation exercises sequence ordering, and
	// a conflicting record is excluded from Pending even with a missing
	// parent.
	orphanedParent := digestHex(t, "30")
	sibA := digestHex(t, "31")
	sibB := digestHex(t, "32")
	for _, digest := range []Digest{sibA, sibB} {
		if err := m.Observe(newMessage(2, 4, orphanedParent, digest)); err != nil {
			t.Fatal(err)
		}
	}
	conflicts = m.Conflicts()
	if len(conflicts) != 3 || conflicts[1].Sequence != 0 || conflicts[2].Sequence != 4 {
		t.Fatalf("conflict ordering wrong: %+v", conflicts)
	}
	for _, rec := range m.Pending() {
		if rec.ContentDigest.Equal(sibA) || rec.ContentDigest.Equal(sibB) {
			t.Fatal("conflicting records must never be reported as pending")
		}
	}

	// Unequal-length and differing bytes for digest equality.
	if root1.Equal(digestHex(t, "1000")) {
		t.Fatal("digests of different lengths must be unequal")
	}
	if root1.Equal(digestHex(t, "11")) {
		t.Fatal("digests with different bytes must be unequal")
	}

	// Same generation/sequence but distinct parents produce separate
	// conflicts, exercising the parent-digest tiebreaker.
	parentX := digestHex(t, "40")
	parentY := digestHex(t, "41")
	pairs := []Digest{
		digestHex(t, "50"), digestHex(t, "51"),
		digestHex(t, "60"), digestHex(t, "61"),
	}
	if err := m.Observe(newMessage(4, 1, parentX, pairs[0])); err != nil {
		t.Fatal(err)
	}
	if err := m.Observe(newMessage(4, 1, parentX, pairs[1])); err != nil {
		t.Fatal(err)
	}
	if err := m.Observe(newMessage(4, 1, parentY, pairs[2])); err != nil {
		t.Fatal(err)
	}
	if err := m.Observe(newMessage(4, 1, parentY, pairs[3])); err != nil {
		t.Fatal(err)
	}
	conflicts = m.Conflicts()
	last := conflicts[len(conflicts)-2:]
	if last[0].Generation != 4 || last[1].Generation != 4 ||
		last[0].ParentDigest.Equal(last[1].ParentDigest) {
		t.Fatalf("parent tiebreaker ordering wrong: %+v", last)
	}
}
