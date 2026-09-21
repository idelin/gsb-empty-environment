// Runnable demo for the dag scheduler: a diamond success case, a failure
// with blocked dependents alongside an independent branch, pre-run cycle
// detection, and a dynamically added dependency.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"taskdag/dag"
)

func main() {
	fmt.Println("== 1. diamond: a -> b,c -> d (d runs exactly once) ==")
	runDiamond()

	fmt.Println()
	fmt.Println("== 2. failure blocks dependents, independent branch continues ==")
	runFailure()

	fmt.Println()
	fmt.Println("== 3. cycle detected before anything runs ==")
	runCycle()

	fmt.Println()
	fmt.Println("== 4. dependency added dynamically while Run is executing ==")
	runDynamic()
}

func task(id string, d time.Duration, err error) dag.TaskFunc {
	return func(context.Context) error {
		fmt.Printf("  [%03dms] %s start\n", millis(), id)
		time.Sleep(d)
		if err != nil {
			fmt.Printf("  [%03dms] %s FAIL: %v\n", millis(), id, err)
			return err
		}
		fmt.Printf("  [%03dms] %s ok\n", millis(), id)
		return nil
	}
}

var start = time.Now()

func millis() int64 {
	return time.Since(start).Milliseconds()
}

func printReport(r *dag.Report) {
	for _, id := range r.IDs() {
		st, _ := r.StatusOf(id)
		line := fmt.Sprintf("  %-3s %s", id, st)
		if e := r.Err(id); e != nil {
			line += " (" + e.Error() + ")"
		}
		fmt.Println(line)
	}
}

func runDiamond() {
	s := dag.New()
	_ = s.Add("a", task("a", 20*time.Millisecond, nil))
	_ = s.Add("b", task("b", 20*time.Millisecond, nil), "a")
	_ = s.Add("c", task("c", 20*time.Millisecond, nil), "a")
	_ = s.Add("d", task("d", 10*time.Millisecond, nil), "b", "c")
	r, err := s.Run(context.Background())
	if err != nil {
		panic(err)
	}
	printReport(r)
}

func runFailure() {
	boom := errors.New("boom")
	s := dag.New()
	_ = s.Add("bad", task("bad", 10*time.Millisecond, boom))
	_ = s.Add("child", task("child", 10*time.Millisecond, nil), "bad")
	_ = s.Add("grand", task("grand", 10*time.Millisecond, nil), "child")
	_ = s.Add("solo", task("solo", 30*time.Millisecond, nil))
	r, err := s.Run(context.Background())
	if err != nil {
		panic(err)
	}
	printReport(r)
	fmt.Println("  failed:", r.Failed(), " blocked:", r.Blocked(), " succeeded:", r.Succeeded())
}

func runCycle() {
	s := dag.New()
	_ = s.Add("a", task("a", 0, nil), "c")
	_ = s.Add("b", task("b", 0, nil), "a")
	_ = s.Add("c", task("c", 0, nil), "b")
	_, err := s.Run(context.Background())
	fmt.Println("  Run error:", err)
}

func runDynamic() {
	s := dag.New()
	gate := make(chan struct{})
	_ = s.Add("a", func(context.Context) error {
		fmt.Printf("  [%03dms] a start (will be b's dynamically added dep)\n", millis())
		time.Sleep(40 * time.Millisecond)
		fmt.Printf("  [%03dms] a ok\n", millis())
		return nil
	})
	_ = s.Add("root", func(context.Context) error {
		fmt.Printf("  [%03dms] root start; adding edge b -> a\n", millis())
		if err := s.AddDependency("b", "a"); err != nil {
			fmt.Println("  AddDependency error:", err)
		}
		time.Sleep(20 * time.Millisecond)
		close(gate)
		fmt.Printf("  [%03dms] root ok\n", millis())
		return nil
	})
	_ = s.Add("b", task("b", 10*time.Millisecond, nil), "root")
	_, _ = s.Run(context.Background())
}
