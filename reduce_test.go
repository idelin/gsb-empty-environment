package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
	"time"
)

var magic = []byte("BUG!")

func containsMagic(sample []byte) Verdict {
	if bytes.Contains(sample, magic) {
		return VerdictSameFailure
	}
	return VerdictPass
}

// structPred accepts only well-formed samples (length-prefixed payload
// plus matching CRC-32) that still contain the trigger, so derived
// fields cannot be deleted and must be recomputed after every edit.
func structPred(sample []byte) Verdict {
	if len(sample) < 2+4 {
		return VerdictOtherFailure
	}
	n := int(binary.BigEndian.Uint16(sample[:2]))
	if len(sample) != 2+n+4 {
		return VerdictOtherFailure
	}
	payload := sample[2 : 2+n]
	if binary.BigEndian.Uint32(sample[2+n:]) != crc32.ChecksumIEEE(payload) {
		return VerdictOtherFailure
	}
	if bytes.Contains(payload, magic) {
		return VerdictSameFailure
	}
	return VerdictPass
}

// sampleTree models a length-prefixed payload followed by a checksum,
// with junk blobs surrounding the crashing trigger.
func sampleTree() *Node {
	return &Node{ID: "root", Children: []*Node{
		{ID: "hdrlen", LengthOf: "payload", Width: 2},
		{ID: "payload", Children: []*Node{
			{ID: "junk1", Data: bytes.Repeat([]byte("A"), 64)},
			{ID: "trigger", Data: []byte("xxBUG!yy")},
			{ID: "junk2", Data: bytes.Repeat([]byte("B"), 64)},
		}},
		{ID: "cksum", ChecksumOf: "payload", Width: 4},
	}}
}

func TestReduceToOneMinimal(t *testing.T) {
	res, err := Reduce(sampleTree(), structPred, Options{})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !res.Minimized || res.BudgetExhausted {
		t.Fatalf("expected minimized result, got minimized=%v exhausted=%v", res.Minimized, res.BudgetExhausted)
	}
	if !bytes.Contains(res.Sample, magic) {
		t.Fatalf("result lost the failure trigger: %q", res.Sample)
	}
	if findNode(res.Root, "junk1") != nil || findNode(res.Root, "junk2") != nil {
		t.Fatalf("junk nodes survived reduction: %q", res.Sample)
	}
	// Layout: 2-byte length + payload + 4-byte CRC-32.
	if got, want := len(res.Sample), 2+8+4; got != want {
		t.Fatalf("result size = %d, want %d: %q", got, want, res.Sample)
	}
	payload := res.Sample[2 : len(res.Sample)-4]
	if got := binary.BigEndian.Uint16(res.Sample[:2]); got != uint16(len(payload)) {
		t.Fatalf("length field = %d, payload is %d bytes", got, len(payload))
	}
	if got := binary.BigEndian.Uint32(res.Sample[len(res.Sample)-4:]); got != crc32.ChecksumIEEE(payload) {
		t.Fatalf("checksum field = %#x, want %#x", got, crc32.ChecksumIEEE(payload))
	}
	// Independently verify 1-minimality: no single edit of the result
	// still fails.
	for _, cand := range candidates(res.Root) {
		next := Clone(res.Root)
		if cand.op == "delete" {
			removeNode(next, cand.target)
		} else {
			findNode(next, cand.target).Data = cand.shrink
		}
		enc, err := Encode(next)
		if err != nil {
			continue
		}
		if structPred(enc) == VerdictSameFailure {
			t.Fatalf("result is not 1-minimal: %s of %q still fails", cand.op, cand.target)
		}
	}
	// Every recorded predicate call must be accounted for.
	calls := 0
	for _, tr := range res.Trials {
		if tr.Verdict != VerdictInvalid {
			calls++
		}
	}
	if calls != res.Calls {
		t.Fatalf("Calls = %d, but %d non-invalid trials recorded", res.Calls, calls)
	}
}

func TestCallbackTimeout(t *testing.T) {
	original := mustEncode(t, sampleTree())
	slow := func(sample []byte) Verdict {
		if !bytes.Equal(sample, original) { // only trial candidates are slow
			time.Sleep(50 * time.Millisecond)
		}
		return containsMagic(sample)
	}
	start := time.Now()
	res, err := Reduce(sampleTree(), slow, Options{MaxCalls: 4, Timeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("reduction blocked on slow callback: %v", elapsed)
	}
	if !bytes.Equal(res.Sample, original) {
		t.Fatalf("timed-out candidates were kept: sample shrank to %d bytes", len(res.Sample))
	}
	timeouts := 0
	for _, tr := range res.Trials {
		if tr.Verdict == VerdictTimeout {
			timeouts++
			if tr.Kept {
				t.Fatalf("trial %d kept despite timeout", tr.Seq)
			}
		}
	}
	if timeouts == 0 {
		t.Fatal("expected timeout verdicts in trial log")
	}
}

func TestDependencyCycle(t *testing.T) {
	root := &Node{ID: "root", Children: []*Node{
		{ID: "a", LengthOf: "b", Width: 1},
		{ID: "b", ChecksumOf: "a", Width: 4},
	}}
	if _, err := Encode(root); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("Encode error = %v, want ErrDependencyCycle", err)
	}
	if _, err := Reduce(root, containsMagic, Options{}); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("Reduce error = %v, want ErrDependencyCycle", err)
	}
}

func TestSharedReferences(t *testing.T) {
	// Two checksum nodes (anonymous, so not deletion targets) share one
	// blob. Deleting the blob must be rejected as invalid without
	// spending predicate budget.
	tree := &Node{ID: "root", Children: []*Node{
		{ChecksumOf: "shared", Width: 4},
		{ChecksumOf: "shared", Width: 4},
		{ID: "shared", Data: []byte("SHARED-data")},
		{ID: "trigger", Data: magic},
	}}
	pred := func(sample []byte) Verdict {
		if bytes.Contains(sample, magic) && bytes.Contains(sample, []byte("SHARED")) {
			return VerdictSameFailure
		}
		return VerdictPass
	}
	res, err := Reduce(tree, pred, Options{})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	invalid := 0
	for _, tr := range res.Trials {
		if tr.Verdict == VerdictInvalid {
			invalid++
			if tr.Op != "delete" || tr.Target != "shared" {
				t.Fatalf("unexpected invalid trial: %+v", tr)
			}
			if tr.Kept {
				t.Fatal("invalid candidate was kept")
			}
		}
	}
	if invalid == 0 {
		t.Fatal("deleting a shared node was not recorded as invalid")
	}
	if !bytes.Contains(res.Sample, []byte("SHARED")) {
		t.Fatalf("shared node was removed despite live references: %q", res.Sample)
	}
}

func TestSyntaxErrorNotConfusedWithVuln(t *testing.T) {
	// The harness reports a different failure class (e.g. a parse
	// error) once the trigger is gone; the reducer must not keep it.
	pred := func(sample []byte) Verdict {
		if bytes.Contains(sample, magic) {
			return VerdictSameFailure
		}
		return VerdictOtherFailure
	}
	res, err := Reduce(sampleTree(), pred, Options{})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !bytes.Contains(res.Sample, magic) {
		t.Fatalf("reducer accepted an other-failure candidate: %q", res.Sample)
	}
	for _, tr := range res.Trials {
		if tr.Kept && tr.Verdict != VerdictSameFailure {
			t.Fatalf("trial %d kept with verdict %v", tr.Seq, tr.Verdict)
		}
	}
}

func TestBudgetExhausted(t *testing.T) {
	res, err := Reduce(sampleTree(), containsMagic, Options{MaxCalls: 2})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if res.Minimized {
		t.Fatal("result marked minimized despite exhausted budget")
	}
	if !res.BudgetExhausted {
		t.Fatal("BudgetExhausted not set")
	}
	if res.Calls > 2 {
		t.Fatalf("predicate budget exceeded: %d calls", res.Calls)
	}
	if !bytes.Contains(res.Sample, magic) {
		t.Fatalf("partial result no longer fails: %q", res.Sample)
	}
}

func TestInitialNotFailing(t *testing.T) {
	passing := func([]byte) Verdict { return VerdictPass }
	_, err := Reduce(sampleTree(), passing, Options{})
	if !errors.Is(err, ErrNotFailing) {
		t.Fatalf("Reduce error = %v, want ErrNotFailing", err)
	}
}

func TestOffsetRecomputed(t *testing.T) {
	tree := &Node{ID: "root", Children: []*Node{
		{ID: "off", OffsetOf: "trigger", Width: 2},
		{ID: "filler", Data: bytes.Repeat([]byte("F"), 10)},
		{ID: "trigger", Data: magic},
	}}
	// The predicate only accepts samples whose offset field points at
	// the trigger, so the offset node itself cannot be deleted and any
	// deletion before it must be compensated by recomputation.
	pred := func(sample []byte) Verdict {
		if len(sample) < 2 {
			return VerdictOtherFailure
		}
		off := int(binary.BigEndian.Uint16(sample[:2]))
		if off < 2 || off+len(magic) > len(sample) {
			return VerdictOtherFailure
		}
		if bytes.Equal(sample[off:off+len(magic)], magic) {
			return VerdictSameFailure
		}
		return VerdictPass
	}
	res, err := Reduce(tree, pred, Options{})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if got := binary.BigEndian.Uint16(res.Sample[:2]); got != 2 {
		t.Fatalf("offset field = %d after filler deletion, want 2", got)
	}
}

func mustEncode(t *testing.T, root *Node) []byte {
	t.Helper()
	b, err := Encode(root)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return b
}
