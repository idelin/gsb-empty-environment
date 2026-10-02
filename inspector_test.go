package checksumsync

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func seedTables(n int) (src, tgt *Table) {
	src, tgt = NewTable(), NewTable()
	for i := 0; i < n; i++ {
		v := fmt.Sprintf("name-%d|balance-%d", i, i*10)
		src.Upsert(uint64(i), v)
		tgt.Upsert(uint64(i), v)
	}
	return src, tgt
}

func TestCompareDetectsTamperedFieldAndLocatesPK(t *testing.T) {
	src, tgt := seedTables(100000)

	// Silently modify a single field of one row in the target.
	const victim = uint64(42424)
	tgt.Upsert(victim, "name-42424|balance-999999")

	rep := Compare(src, tgt)
	if rep.Consistent() {
		t.Fatal("tampering was not detected")
	}
	if len(rep.Mismatches) != 1 {
		t.Fatalf("expected exactly 1 mismatch, got %d: %v", len(rep.Mismatches), rep.Mismatches)
	}
	m := rep.Mismatches[0]
	if m.Kind != Modified || m.PK != victim {
		t.Fatalf("expected MODIFIED pk=%d, got %s", victim, m)
	}
	if len(rep.BadBuckets) != 1 || rep.BadBuckets[0] != m.Bucket {
		t.Fatalf("bad bucket not identified correctly: %v", rep.BadBuckets)
	}
	t.Logf("cycle took %s, located %s", rep.Duration, m)
}

func TestCompareDetectsLostAndPhantomRows(t *testing.T) {
	src, tgt := seedTables(50000)
	tgt.Delete(123)          // lost change
	tgt.Upsert(999999, "x")  // phantom row in target

	rep := Compare(src, tgt)
	if len(rep.Mismatches) != 2 {
		t.Fatalf("expected 2 mismatches, got %v", rep.Mismatches)
	}
	kinds := map[uint64]MismatchKind{}
	for _, m := range rep.Mismatches {
		kinds[m.PK] = m.Kind
	}
	if kinds[123] != MissingInTarget {
		t.Fatalf("pk 123 should be MISSING_IN_TARGET, got %v", kinds)
	}
	if kinds[999999] != ExtraInTarget {
		t.Fatalf("pk 999999 should be EXTRA_IN_TARGET, got %v", kinds)
	}
}

// End-to-end: changes flow continuously while the inspector patrols; a
// tampered row must be pinpointed within milliseconds.
func TestInspectorDetectsTamperDuringContinuousSync(t *testing.T) {
	src, tgt := NewTable(), NewTable()

	stopSync := make(chan struct{})
	syncDone := make(chan struct{})
	go func() { // replication stream: apply every change to both ends
		defer close(syncDone)
		rng := rand.New(rand.NewSource(1))
		for i := 0; ; i++ {
			select {
			case <-stopSync:
				return
			default:
			}
			pk := uint64(rng.Intn(200000))
			val := fmt.Sprintf("v-%d", rng.Int())
			src.Upsert(pk, val)
			tgt.Upsert(pk, val) // near-zero replication lag
		}
	}()

	detected := make(chan Mismatch, 1)
	in := NewInspector(src, tgt, 2*time.Millisecond)
	in.OnDetect = func(rep *Report) {
		for _, m := range rep.Mismatches {
			select {
			case detected <- m:
			default:
			}
		}
	}
	go in.Run()

	// Let the stream warm up, then tamper one row in the target only.
	time.Sleep(50 * time.Millisecond)
	const victim = uint64(77777)
	src.Upsert(victim, "balance=100")
	tgt.Upsert(victim, "balance=100")
	tamperedAt := time.Now()
	tgt.Upsert(victim, "balance=0") // the attack: silent single-field change

	select {
	case m := <-detected:
		latency := time.Since(tamperedAt)
		if m.PK != victim || m.Kind != Modified {
			t.Fatalf("wrong mismatch located: %s", m)
		}
		t.Logf("tampering on pk=%d detected in %s (%d patrol cycles run)",
			m.PK, latency, in.Cycles())
	case <-time.After(5 * time.Second):
		t.Fatal("tampering was not detected within 5s")
	}

	in.Stop()
	close(stopSync)
	<-syncDone
}

// After the tampered row is repaired, the inspector must report consistency.
func TestInspectorRecoversAfterRepair(t *testing.T) {
	src, tgt := seedTables(10000)
	tgt.Upsert(5, "corrupted")
	if rep := Compare(src, tgt); rep.Consistent() {
		t.Fatal("expected divergence")
	}
	v, _ := src.Get(5)
	tgt.Upsert(5, v) // repair
	if rep := Compare(src, tgt); !rep.Consistent() {
		t.Fatalf("expected consistency after repair, got %v", rep.Mismatches)
	}
}
