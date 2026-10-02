// Demo: inject 100 batch tasks into the memory-aware scheduler and watch
// backpressure engage at the 80% watermark, GC-capable fibers get priority,
// and admission smoothly resume after memory is released.
package main

import (
	"fmt"
	"math/rand"
	"time"

	"memsched"
)

func main() {
	const (
		capacity    = int64(1000)
		normalTasks = 80
		gcTasks     = 20
	)
	mon := memsched.NewMonitor(capacity, 0.8, 0.6)
	sched := memsched.NewScheduler(mon)

	// Watch pressure transitions.
	start := time.Now()
	stop := make(chan struct{})
	go func() {
		tick := time.NewTicker(2 * time.Millisecond)
		defer tick.Stop()
		last := false
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				p := sched.Pressured()
				if p != last {
					state := "RESUMED  (admission reopens, token-bucket paced)"
					if p {
						state = "BACKPRESSURE (new fibers held, GC fibers prioritized)"
					}
					fmt.Printf("[t=%4dms] usage=%4d/%d (%5.1f%%) -> %s\n",
						time.Since(start)/time.Millisecond, mon.Used(), capacity,
						100*mon.Usage(), state)
					last = p
				}
			}
		}
	}()

	fmt.Printf("injecting %d batch tasks (capacity=%d, high=80%%, low=60%%)\n",
		normalTasks+gcTasks, capacity)
	for i := 0; i < normalTasks+gcTasks; i++ {
		i := i
		if i%5 == 4 { // every 5th task is GC-capable
			sched.Submit(&memsched.Task{
				Name:      fmt.Sprintf("gc-%d", i),
				GCCapable: true,
				MemDemand: 10,
				Run: func(f *memsched.Fiber) {
					f.Alloc(10)
					time.Sleep(time.Duration(rand.Intn(3)) * time.Millisecond)
					f.GC() // reclaim temp memory, relieving pressure
				},
			})
			continue
		}
		sched.Submit(&memsched.Task{
			Name:      fmt.Sprintf("batch-%d", i),
			MemDemand: 40,
			Run: func(f *memsched.Fiber) {
				f.Alloc(40) // temp objects
				time.Sleep(time.Duration(3+rand.Intn(8)) * time.Millisecond)
				f.Release(40)
			},
		})
	}

	sched.Wait()
	close(stop)
	admitted, completed, blocked, episodes, violations := sched.Stats()
	fmt.Printf("\ndone in %v\n", time.Since(start).Round(time.Millisecond))
	fmt.Printf("admitted=%d completed=%d blockedEvents=%d pressureEpisodes=%d violations=%d\n",
		admitted, completed, blocked, episodes, violations)
	fmt.Printf("peak memory=%d/%d (%.0f%%), leaked=%d\n",
		mon.MaxUsed(), capacity, 100*float64(mon.MaxUsed())/float64(capacity), mon.Used())
	sched.Close()
}
