// Demo: a root task fans out subtasks that sleep for injected durations;
// some are injected to fail. Idle workers steal from the busy spawner.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	"worksteal"
)

func main() {
	sched := worksteal.New(4)
	rng := rand.New(rand.NewSource(1))

	const children = 40
	spawned := make(chan struct{})
	root := worksteal.Task(func(w *worksteal.Worker) error {
		defer close(spawned)
		for i := 0; i < children; i++ {
			i := i
			err := w.Submit(func(w *worksteal.Worker) error {
				time.Sleep(time.Duration(rng.Intn(5)) * time.Millisecond)
				if i%7 == 0 { // injected failure
					return errors.New("injected failure")
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	if err := sched.Submit(root); err != nil {
		panic(err)
	}
	<-spawned // let the root finish fanning out before shutting down
	sched.Close()

	stats := sched.Stats()
	fmt.Printf("submitted=%d completed=%d failed=%d stolen=%d\n",
		stats.Submitted, stats.Completed, stats.Failed, stats.Stolen)
	fmt.Printf("conserved=%v (submitted == completed+failed)\n",
		stats.Submitted == stats.Completed+stats.Failed)
}
