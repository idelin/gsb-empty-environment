package kernel

import "fmt"

// Snapshot captures the state of the whole kernel after a specific event.
type Snapshot struct {
	// Bytes is the canonical encoding of all component memory.
	Bytes []byte
	// Hash is the SHA-256 digest of Bytes.
	Hash SnapshotHash
}

// Kernel is the deterministic event-replay debugging kernel. It records
// every external input, clock interrupt and the random seed into an event
// log while the system runs, and checkpoints a snapshot after each event.
type Kernel struct {
	sched *Scheduler
	sys   *System
	log   *EventLog
	snaps []Snapshot
}

// NewKernel creates a kernel whose randomness derives from seed.
func NewKernel(seed uint64) *Kernel {
	return &Kernel{
		sched: NewScheduler(),
		sys:   NewSystem(seed),
		log:   NewEventLog(seed),
	}
}

// SubmitExternal feeds an external input (packet arrival or clock
// interrupt) into the scheduler.
func (k *Kernel) SubmitExternal(ev Event) {
	if !ev.Kind.External() {
		panic("kernel: SubmitExternal called with internal event kind")
	}
	k.sched.Push(ev)
}

// Step applies the next scheduled event, appends it to the log and
// checkpoints a snapshot. It reports false when nothing is scheduled.
func (k *Kernel) Step() (Event, bool) {
	ev, ok := k.sched.Next()
	if !ok {
		return Event{}, false
	}
	k.sys.apply(ev, k.sched)
	k.log.Append(ev)
	b := k.snapshotBytes()
	k.snaps = append(k.snaps, Snapshot{Bytes: b, Hash: hashOf(b)})
	return ev, true
}

// Run drains the scheduler and returns the recorded log.
func (k *Kernel) Run() *EventLog {
	for {
		if _, ok := k.Step(); !ok {
			break
		}
	}
	return k.log
}

// Log returns the event log recorded so far.
func (k *Kernel) Log() *EventLog {
	return k.log
}

// SnapshotAt returns the snapshot taken right after the event at index
// was applied.
func (k *Kernel) SnapshotAt(index int) Snapshot {
	return k.snaps[index]
}

// Stats exposes the system's counters.
func (k *Kernel) Stats() Stats {
	return k.sys.Stats()
}

// Debugger provides side-effect-free time travel over a recorded log.
// Replaying never touches the kernel that produced the log: each jump
// builds a fresh kernel from the recorded seed and external inputs and
// lets the scheduler re-derive every internal (timeout) event.
type Debugger struct {
	log *EventLog
}

// NewDebugger binds a debugger to a recorded event log.
func NewDebugger(log *EventLog) *Debugger {
	return &Debugger{log: log}
}

// Goto jumps to the historical event at index (0-based) and returns a
// kernel positioned immediately after that event was applied. The replay
// re-derives internal events from the seed and external inputs only, and
// verifies that the regenerated sequence matches the recorded log prefix
// exactly; a mismatch means the run is not deterministic and is reported
// as an error.
func (d *Debugger) Goto(index int) (*Kernel, error) {
	if index < 0 || index >= d.log.Len() {
		return nil, fmt.Errorf("debugger: event index %d out of range [0,%d)", index, d.log.Len())
	}
	k := NewKernel(d.log.Seed)
	for _, ev := range d.log.Events {
		if ev.Kind.External() {
			k.SubmitExternal(ev)
		}
	}
	for k.log.Len() <= index {
		if _, ok := k.Step(); !ok {
			return nil, fmt.Errorf("debugger: replay exhausted before event %d", index)
		}
	}
	for i := 0; i <= index; i++ {
		if k.log.Events[i] != d.log.Events[i] {
			return nil, fmt.Errorf("debugger: replay diverged at event %d: recorded %v, replayed %v",
				i, d.log.Events[i], k.log.Events[i])
		}
	}
	return k, nil
}
