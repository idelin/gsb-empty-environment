package recvq

import (
	"bytes"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestSynthesizeDeterministic(t *testing.T) {
	a := Synthesize(7, 100, []string{"a", "b", "c"})
	b := Synthesize(7, 100, []string{"a", "b", "c"})
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed must produce the same workload")
	}
	high := 0
	for _, rec := range a {
		if rec.Priority == High {
			high++
		}
	}
	if high == 0 {
		t.Fatal("synthesized workload must contain high-priority records")
	}
}

func TestReplayMatchesLiveRun(t *testing.T) {
	sink, log := EventSink()
	r := NewReceiver(8, DropIncoming, sink)
	recs := Synthesize(5, 400, []string{"a", "b", "c"})
	runToQuiescence(r, recs, 2, Drain, 20*time.Microsecond)

	var buf bytes.Buffer
	if err := WriteEvents(&buf, *log); err != nil {
		t.Fatal(err)
	}
	replayed, inQueue, err := Replay(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if inQueue != 0 {
		t.Fatalf("replay left %d in queue", inQueue)
	}
	live := r.Stats()
	if !reflect.DeepEqual(replayed, live) {
		t.Fatalf("replayed stats %v != live stats %v", replayed, live)
	}
}

func TestReplayFixture(t *testing.T) {
	f, err := os.Open("testdata/telemetry.jsonl")
	if err != nil {
		t.Skip("fixture not generated yet:", err)
	}
	defer f.Close()
	stats, inQueue, err := Replay(f)
	if err != nil {
		t.Fatal(err)
	}
	if inQueue != 0 {
		t.Fatalf("fixture ends with %d records still queued", inQueue)
	}
	if stats.Arrived == 0 || stats.Processed == 0 {
		t.Fatalf("fixture looks empty: %s", stats)
	}
	if !stats.Conserved(inQueue) {
		t.Fatalf("fixture not conserved: %s", stats)
	}
	t.Logf("fixture replayed: %s", stats)
}

func TestReplayDetectsConservationViolation(t *testing.T) {
	// A process event with no matching enqueue is impossible in a
	// conserved system.
	bad := []byte(`{"seq":1,"type":"process","record":"r1","tenant":"a","priority":"normal"}` + "\n")
	if _, _, err := Replay(bytes.NewReader(bad)); err == nil {
		t.Fatal("replay must reject a stream that leaves the queue negative")
	}
}

func TestReplayRejectsUnknownReason(t *testing.T) {
	bad := []byte(`{"seq":1,"type":"drop","record":"r1","tenant":"a","priority":"normal","reason":"mystery"}` + "\n")
	if _, _, err := Replay(bytes.NewReader(bad)); err == nil {
		t.Fatal("replay must reject unknown drop reasons")
	}
}
