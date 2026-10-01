// Command demo runs a scripted simulation of the lease kernel:
//
//	Scenario 1: normal lifecycle — the leader stops writing at the
//	            9.5s safe boundary (TTL 10s - max drift 500ms).
//	Scenario 2: a +1s clock jump is injected at t=5s; the safety
//	            self-check triggers an emergency early demotion and
//	            a second node takes over without split-brain.
package main

import (
	"fmt"
	"time"

	"leaseguard/lease"
)

var sim *lease.SimClock

func logf(format string, args ...any) {
	elapsed := sim.Now().Sub(epoch).Round(time.Millisecond)
	fmt.Printf("[t=%6s] %s\n", elapsed, fmt.Sprintf(format, args...))
}

var epoch = time.Unix(1_700_000_000, 0)

func tryWrite(m *lease.Manager, node, op string) {
	if err := m.Write(op); err != nil {
		logf("%s WRITE %-12q -> REJECTED (%v)", node, op, err)
		return
	}
	logf("%s WRITE %-12q -> OK", node, op)
}

func main() {
	cfg := lease.Config{
		LeaseDuration: 10 * time.Second,
		MaxClockDrift: 500 * time.Millisecond,
		MaxRTT:        200 * time.Millisecond,
		CheckInterval: 100 * time.Millisecond,
	}
	fmt.Printf("config: TTL=%s maxDrift=%s maxRTT=%s => safe window=%s\n\n",
		cfg.LeaseDuration, cfg.MaxClockDrift, cfg.MaxRTT, cfg.SafeWindow())

	scenario1(cfg)
	scenario2(cfg)
}

// step advances the simulated clock in CheckInterval-sized steps,
// running the watchdog self-check after each step.
func step(m *lease.Manager, cfg lease.Config, d time.Duration) {
	for d > 0 {
		s := cfg.CheckInterval
		if s > d {
			s = d
		}
		sim.Advance(s)
		m.SelfCheck()
		d -= s
	}
}

func scenario1(cfg lease.Config) {
	fmt.Println("=== Scenario 1: safe boundary at TTL - drift - RTT = 9.3s ===")
	sim = lease.NewSimClock(epoch)
	coord := lease.NewCoordinator(sim, cfg)
	a := lease.NewManager("node-A", sim, coord, cfg)
	defer a.Stop()

	if err := a.Acquire(); err != nil {
		panic(err)
	}
	logf("node-A acquired lease (TTL=%s)", cfg.LeaseDuration)

	step(a, cfg, 5*time.Second)
	tryWrite(a, "node-A", "set x=1")

	step(a, cfg, 4300*time.Millisecond) // t=9.3s: safe window over
	logf("safe period elapsed (TTL - maxDrift - maxRTT)")
	tryWrite(a, "node-A", "set x=2")

	st := a.Status()
	logf("node-A demoted=%v reason=%q", st.Demoted, st.Reason)
	fmt.Println()
}

func scenario2(cfg lease.Config) {
	fmt.Println("=== Scenario 2: inject +1s clock jump at t=5s ===")
	sim = lease.NewSimClock(epoch)
	coord := lease.NewCoordinator(sim, cfg)
	a := lease.NewManager("node-A", sim, coord, cfg)
	defer a.Stop()
	b := lease.NewManager("node-B", sim, coord, cfg)
	defer b.Stop()

	if err := a.Acquire(); err != nil {
		panic(err)
	}
	logf("node-A acquired lease")
	if err := b.Acquire(); err != nil {
		logf("node-B acquire -> %v", err)
	}

	step(a, cfg, 5*time.Second)
	tryWrite(a, "node-A", "set y=1")

	logf("!! injecting clock fault: local clock jumps forward +1s (max drift 500ms)")
	sim.Jump(1 * time.Second)
	a.SelfCheck()

	st := a.Status()
	logf("node-A emergency demotion: %q", st.Reason)
	tryWrite(a, "node-A", "set y=2") // stale leader must be fenced

	if err := b.Acquire(); err != nil {
		panic(err)
	}
	logf("node-B acquired lease after A released it")
	tryWrite(b, "node-B", "set y=3")

	if a.CanWrite() && b.CanWrite() {
		logf("FATAL: split-brain, two leaders!")
	} else {
		logf("safety check: exactly one writable leader (A=%v, B=%v) — no split-brain",
			a.CanWrite(), b.CanWrite())
	}
}
