package desim

import (
	"bytes"
	"reflect"
	"testing"
)

func TestCheckpointRestoreMatchesContinuousRun(t *testing.T) {
	// Continuous reference run.
	cont := testWorld()
	cont.Sched.RunUntil(1000)

	// Run to tick 40, checkpoint, continue the original.
	w := testWorld()
	w.Sched.RunUntil(40)
	var buf bytes.Buffer
	if err := w.Save(&buf); err != nil {
		t.Fatalf("save: %v", err)
	}
	w.Sched.RunUntil(1000)

	// Restore from the checkpoint and continue.
	restored, err := LoadWorld(&buf)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if restored.Sched.Now() != 40 {
		t.Fatalf("restored now = %d, want 40", restored.Sched.Now())
	}
	restored.Sched.RunUntil(1000)

	if !reflect.DeepEqual(restored.Sn.Log, cont.Sn.Log) {
		t.Fatalf("restored trajectory differs:\n got  %v\n want %v",
			restored.Sn.Log, cont.Sn.Log)
	}
	if !reflect.DeepEqual(w.Sn.Log, cont.Sn.Log) {
		t.Fatalf("checkpointed original diverged from continuous run")
	}
	if restored.Q.Dropped != cont.Q.Dropped || restored.Src.Remaining != cont.Src.Remaining {
		t.Fatalf("restored counters differ: dropped %d/%d remaining %d/%d",
			restored.Q.Dropped, cont.Q.Dropped,
			restored.Src.Remaining, cont.Src.Remaining)
	}
}

func TestCheckpointPreservesCancellation(t *testing.T) {
	s := NewScheduler(nil)
	fired := 0
	s.Register("t", func(s *Scheduler, d interface{}) { fired++ })

	h := s.ScheduleAt(10, CatTimer, "t", nil)
	if !s.Cancel(h) {
		t.Fatal("cancel failed")
	}
	s.ScheduleAt(10, CatTimer, "t", nil)

	b, err := s.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	r := NewScheduler(nil)
	r.Register("t", func(s *Scheduler, d interface{}) { fired++ })
	if err := r.Unmarshal(b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	r.Run()

	if fired != 1 {
		t.Fatalf("fired = %d, want 1 (cancelled event must stay dead across checkpoint)", fired)
	}
}

func TestCheckpointSeqContinuity(t *testing.T) {
	// After restore, new events must keep the global seq order:
	// a same-tick event scheduled post-restore runs after one that
	// was already pending in the checkpoint.
	s := NewScheduler(nil)
	var got []string
	recorder(s, &got)
	s.ScheduleAt(10, CatTimer, "rec", "pending")
	s.ScheduleAt(1, CatTimer, "rec", "early")
	s.Step() // fires "early", now = 1

	b, err := s.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := NewScheduler(nil)
	recorder(r, &got)
	if err := r.Unmarshal(b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	r.ScheduleAt(10, CatTimer, "rec", "post-restore")
	r.Run()

	want := []string{"early", "pending", "post-restore"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
