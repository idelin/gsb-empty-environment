package main

import (
	"errors"
	"fmt"
	"time"

	"detrun"
)

// external simulates an outside service. It answers "resp:<req>" and fails
// on "B2". In record mode the runtime calls it; in replay mode the recorded
// outcomes are injected and this function is never touched.
func external(req string) (string, error) {
	if req == "B2" {
		return "", errors.New("injected failure: B2 rejected")
	}
	return "resp:" + req, nil
}

func workload(rt *detrun.Runtime, observe func(format string, args ...any)) {
	rt.Go(func(t *detrun.Task) error {
		if err := t.Sleep(5 * time.Millisecond); err != nil {
			return err
		}
		r1, err := t.Call("A1")
		observe("task A: A1 -> %q err=%v (virtual now=%s)", r1, err, t.Now())
		if err := t.Sleep(5 * time.Millisecond); err != nil {
			return err
		}
		r2, err := t.Call("A2")
		observe("task A: A2 -> %q err=%v (virtual now=%s)", r2, err, t.Now())
		return nil
	})
	rt.Go(func(t *detrun.Task) error {
		r1, err := t.Call("B1")
		observe("task B: B1 -> %q err=%v (virtual now=%s)", r1, err, t.Now())
		if err := t.Sleep(20 * time.Millisecond); err != nil {
			return err
		}
		r2, err := t.Call("B2")
		observe("task B: B2 -> %q err=%v (virtual now=%s)", r2, err, t.Now())
		return nil
	})
}

func main() {
	fmt.Println("== record ==")
	rec := detrun.NewRecord(external)
	workload(rec, func(format string, args ...any) { fmt.Printf("  "+format+"\n", args...) })
	if err := rec.Run(); err != nil {
		panic(err)
	}
	log := rec.Log()
	fmt.Printf("recorded %d events, virtual clock ended at %s\n", len(log), rec.Now())
	for _, e := range log {
		fmt.Printf("  #%02d %s\n", e.Seq, detrun.EventString(e))
	}

	fmt.Println("== replay (same recording, no external func) ==")
	rep := detrun.NewReplay(log)
	workload(rep, func(format string, args ...any) { fmt.Printf("  "+format+"\n", args...) })
	if err := rep.Run(); err != nil {
		panic(err)
	}
	fmt.Println("replay OK: identical trajectory and results")

	fmt.Println("== replay with a misbehaving task (one extra request) ==")
	bad := detrun.NewReplay(log)
	bad.Go(func(t *detrun.Task) error {
		if err := t.Sleep(5 * time.Millisecond); err != nil {
			return err
		}
		if _, err := t.Call("A1"); err != nil {
			return err
		}
		if err := t.Sleep(5 * time.Millisecond); err != nil {
			return err
		}
		_, err := t.Call("A2")
		return err
	})
	bad.Go(func(t *detrun.Task) error {
		if _, err := t.Call("B1"); err != nil {
			return err
		}
		if err := t.Sleep(20 * time.Millisecond); err != nil {
			return err
		}
		_, _ = t.Call("B2")    // recorded failure is injected here
		_, err := t.Call("B3") // extra request: not in the recording
		return err
	})
	if err := bad.Run(); err != nil {
		fmt.Printf("  divergence reported: %v\n", err)
	}
}
