package netsec_test

import (
	"context"
	"testing"
	"time"

	ns "example.org/netsec/q0113"
)

func TestDeterministicSchedulerFIFOAndEDF(t *testing.T) {
	run := func(mode ns.ScheduleMode) []string {
		f := newFixture()
		_, r1 := f.request(t, "c1", "r1", ns.Impersonation, deadlineNow(f))
		_, r2 := f.request(t, "c2", "r2", ns.Impersonation, deadlineNow(f))
		scheduler := ns.NewDeterministicScheduler(f.clock, mode)
		var order []string
		t1, err := f.host.SpawnAsync(r1, ns.NoIdentity, func(context.Context, *ns.TaskContext) error {
			order = append(order, "r1")
			return nil
		})
		if err != nil {
			t.Fatalf("SpawnAsync r1: %v", err)
		}
		t2, err := f.host.SpawnAsync(r2, ns.NoIdentity, func(context.Context, *ns.TaskContext) error {
			order = append(order, "r2")
			return nil
		}, ns.WithTaskDeadline(f.clock.Now().Add(10*time.Second)))
		if err != nil {
			t.Fatalf("SpawnAsync r2: %v", err)
		}
		if err := scheduler.Submit(t1); err != nil {
			t.Fatalf("Submit t1: %v", err)
		}
		if err := scheduler.Submit(t2); err != nil {
			t.Fatalf("Submit t2: %v", err)
		}
		events := scheduler.Run(context.Background())
		if len(events) != 2 {
			t.Fatalf("events = %d", len(events))
		}
		return order
	}

	fifo := run(ns.ScheduleFIFO)
	if fifo[0] != "r1" || fifo[1] != "r2" {
		t.Fatalf("FIFO order = %v", fifo)
	}
	edf := run(ns.ScheduleEarliestDeadline)
	if edf[0] != "r2" || edf[1] != "r1" {
		t.Fatalf("EDF order = %v", edf)
	}
}

func TestSchedulerCanBeHostExecutor(t *testing.T) {
	f := newFixture()
	scheduler := ns.NewDeterministicScheduler(f.clock)
	host := ns.NewHost(ns.Identity{Name: "service"}, ns.WithClock(f.clock), ns.WithExecutor(scheduler))
	auth := host.Authenticator()
	conn, err := auth.OpenConnection("c1")
	if err != nil {
		t.Fatalf("OpenConnection: %v", err)
	}
	req, err := auth.BeginRequest(conn, "r1", ns.Impersonation, deadlineNow(f))
	if err != nil {
		t.Fatalf("BeginRequest: %v", err)
	}
	ran := false
	task, err := host.SpawnAsync(req, ns.NoIdentity, func(context.Context, *ns.TaskContext) error {
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("SpawnAsync: %v", err)
	}
	if ran {
		t.Fatal("executor ran callback during SpawnAsync")
	}
	if scheduler.Len() != 1 {
		t.Fatalf("queued tasks = %d", scheduler.Len())
	}
	scheduler.Run(context.Background())
	if !ran || !task.Done() {
		t.Fatalf("executor did not run task: ran=%v done=%v", ran, task.Done())
	}
}
