package buggy_test

import (
	"strings"
	"testing"

	"kvsfmt/buggy"
	"kvsfmt/format"
	"kvsfmt/fuzz"
	"kvsfmt/oracle"
)

// The harness must detect the known defect (CRC never verified).
func TestDetectsBuggyDecoder(t *testing.T) {
	fails := fuzz.RunCorpus(1, 100, buggy.Decode)
	if len(fails) == 0 {
		t.Fatal("harness failed to detect the known CRC defect")
	}
	for _, f := range fails {
		if f.Kind != "rejection" || f.Want != format.ClassCRC {
			t.Fatalf("unexpected failure: %s", f)
		}
	}
}

// The first detected failure must shrink to a 1-minimal reproducer
// that keeps the same error class, and replay from the recorded bytes.
func TestShrinkAndReplay(t *testing.T) {
	fails := fuzz.RunCorpus(1, 100, buggy.Decode)
	if len(fails) == 0 {
		t.Fatal("no failure to shrink")
	}
	f := fails[0]

	// Predicate preserves the failure category: still crc-invalid per
	// the independent oracle, still accepted by the buggy decoder.
	pred := func(b []byte) bool {
		if oracle.Classify(b) != string(format.ClassCRC) {
			return false
		}
		_, err := buggy.Decode(b)
		return err == nil
	}
	if !pred(f.Data) {
		t.Fatal("recorded failure bytes do not replay")
	}

	res := fuzz.Shrink(f.Data, pred)
	if !pred(res.Minimal) {
		t.Fatal("minimal bytes do not reproduce the failure")
	}
	if len(res.Minimal) > len(f.Data) {
		t.Fatal("shrinking grew the input")
	}

	// 1-minimality: deleting any single byte must not reproduce.
	for i := range res.Minimal {
		cand := append([]byte(nil), res.Minimal[:i]...)
		cand = append(cand, res.Minimal[i+1:]...)
		if pred(cand) {
			t.Fatalf("not 1-minimal: deleting byte %d still reproduces", i)
		}
	}
	// The check record must document the 1-minimality verification.
	joined := strings.Join(res.Log, "\n")
	if !strings.Contains(joined, "1-minimality verification:") {
		t.Fatal("shrink log lacks 1-minimality verification section")
	}
	if !strings.Contains(joined, "result is 1-minimal") {
		t.Fatal("shrink log does not confirm 1-minimality")
	}
}
