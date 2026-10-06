package main

import "fmt"

// run simulates the system tick by tick, printing the occupancy timeline.
func run(title string, sys *System, maxTicks int) string {
	fmt.Println("=== " + title + " ===")
	fmt.Println("tick order: 1) depart (downstream->upstream)  2) arrive  3) start work")
	fmt.Println("legend: J1:2 = busy, 2 ticks left | J1! = blocked (done, downstream full) | . = idle")
	status := "RUNNING"
	for t := 0; t < maxTicks; t++ {
		sys.Events = nil
		sys.Tick(t)
		fmt.Println(sys.Row(t))
		for _, e := range sys.Events {
			fmt.Println("      * " + e)
		}
		status = sys.Status()
		if status != "RUNNING" {
			break
		}
	}
	fmt.Printf(">>> %s after %d exited job(s)\n\n", status, len(sys.Exited))
	return status
}

// Scenario 1: line of three stations, S2 is slow. Finished jobs pile up
// upstream of S2 (blocking); when S2 completes, the departure phase frees
// the whole chain in a single tick (cascading unblocking).
func scenarioCascade() *System {
	line := []int{0, 1, 2}
	jobs := map[int][]*Job{}
	for i, id := range []string{"J1", "J2", "J3", "J4"} {
		jobs[i] = []*Job{{ID: id, Route: line}}
	}
	return &System{
		Stations: []*Station{
			NewStation("S1", 1, 1),
			NewStation("S2", 4, 1), // bottleneck
			NewStation("S3", 1, 1),
		},
		Arrivals: jobs,
	}
}

// Scenario 2: two stations on a cyclic route. J1 sits at S1 wanting S2,
// J2 sits at S2 wanting S1; both finish at the same tick, each finds the
// other station occupied, and both block forever: deadlock.
func scenarioDeadlock() *System {
	sys := &System{
		Stations: []*Station{
			NewStation("S1", 1, 1),
			NewStation("S2", 1, 1),
		},
		Arrivals: map[int][]*Job{},
	}
	j1 := &Job{ID: "J1", Route: []int{0, 1, 0, 1}}
	j2 := &Job{ID: "J2", Route: []int{1, 0, 1, 0}}
	sys.Stations[0].Machines[0] = &Machine{State: Loaded, Job: j1}
	sys.Stations[1].Machines[0] = &Machine{State: Loaded, Job: j2}
	return sys
}

func main() {
	run("scenario 1: blocking and cascading unblocking", scenarioCascade(), 40)
	run("scenario 2: circular wait deadlock", scenarioDeadlock(), 10)
}
