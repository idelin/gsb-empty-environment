package desim

import (
	"reflect"
	"testing"
)

func recorder(s *Scheduler, got *[]string) {
	s.Register("rec", func(s *Scheduler, d interface{}) {
		*got = append(*got, d.(string))
	})
}

func TestSameTickCategoryAndSeqOrder(t *testing.T) {
	s := NewScheduler(nil)
	var got []string
	recorder(s, &got)

	// Scheduled in scrambled order; execution must follow
	// (category, seq), not insertion convenience.
	s.ScheduleAt(10, CatLink, "rec", "link#1")
	s.ScheduleAt(10, CatTimer, "rec", "timer#1")
	s.ScheduleAt(10, CatArrival, "rec", "arrival#1")
	s.ScheduleAt(10, CatTimer, "rec", "timer#2")
	s.ScheduleAt(10, CatLink, "rec", "link#2")
	s.ScheduleAt(10, CatArrival, "rec", "arrival#2")
	s.ScheduleAt(9, CatLink, "rec", "earlier-tick")
	s.Run()

	want := []string{
		"earlier-tick",
		"timer#1", "timer#2",
		"arrival#1", "arrival#2",
		"link#1", "link#2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order mismatch:\n got  %v\n want %v", got, want)
	}
	if s.Now() != 10 {
		t.Fatalf("now = %d, want 10", s.Now())
	}
}

func TestSameTickInsertionFromHandler(t *testing.T) {
	s := NewScheduler(nil)
	var got []string
	s.Register("spawn", func(s *Scheduler, d interface{}) {
		name := d.(string)
		got = append(got, name)
		if name == "first" {
			// New same-tick event gets a higher seq, so it runs
			// after the already-queued "second".
			s.ScheduleAt(s.Now(), CatTimer, "spawn", "spawned")
		}
	})
	s.ScheduleAt(5, CatTimer, "spawn", "first")
	s.ScheduleAt(5, CatTimer, "spawn", "second")
	s.Run()

	want := []string{"first", "second", "spawned"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCancelPreventsFiring(t *testing.T) {
	s := NewScheduler(nil)
	var got []string
	recorder(s, &got)

	h := s.ScheduleAt(10, CatTimer, "rec", "doomed")
	if !h.Valid() {
		t.Fatal("handle should be valid")
	}
	if !s.Cancel(h) {
		t.Fatal("first cancel should succeed")
	}
	if s.Cancel(h) {
		t.Fatal("second cancel should fail")
	}
	s.ScheduleAt(10, CatTimer, "rec", "lives")
	s.Run()

	want := []string{"lives"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestStaleHandleCannotCancelRecycledSlot(t *testing.T) {
	s := NewScheduler(nil)
	var got []string
	recorder(s, &got)

	old := s.ScheduleAt(5, CatTimer, "rec", "old")
	s.Run() // fires "old", slot is recycled with a bumped generation

	fresh := s.ScheduleAt(7, CatTimer, "rec", "fresh")
	if fresh.slot != old.slot {
		t.Skip("slot was not recycled; nothing to prove")
	}
	if s.Cancel(old) {
		t.Fatal("stale handle must not cancel the recycled slot")
	}
	s.Run()

	want := []string{"old", "fresh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCancelZeroHandle(t *testing.T) {
	s := NewScheduler(nil)
	if s.Cancel(Handle{}) {
		t.Fatal("cancelling a zero handle should fail")
	}
}

func TestRunUntilBoundary(t *testing.T) {
	s := NewScheduler(nil)
	var got []string
	recorder(s, &got)

	s.ScheduleAt(3, CatTimer, "rec", "at-3")
	s.ScheduleAt(5, CatTimer, "rec", "at-5")
	s.ScheduleAt(8, CatTimer, "rec", "at-8")
	s.RunUntil(5)

	want := []string{"at-3", "at-5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if s.Now() != 5 {
		t.Fatalf("now = %d, want 5", s.Now())
	}
	if s.Pending() != 1 {
		t.Fatalf("pending = %d, want 1", s.Pending())
	}
}
