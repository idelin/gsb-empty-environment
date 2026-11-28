package zerobubble

import (
	"container/heap"
	"fmt"
	"sort"
)

const infinity = 1e300

type taskDef struct {
	id       TaskID
	deps     []TaskID
	duration float64
}

type taskSlot struct {
	task Task
	done bool
}

type endEvent struct {
	at float64
	id TaskID
}

type endHeap []endEvent

func (h endHeap) Len() int { return len(h) }
func (h endHeap) Less(i, j int) bool {
	if nearlyEqual(h[i].at, h[j].at) {
		return endPriority(h[i].id.Kind) < endPriority(h[j].id.Kind)
	}
	return h[i].at < h[j].at
}
func (h endHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *endHeap) Push(x any)   { *h = append(*h, x.(endEvent)) }
func (h *endHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

type wakeHeap []float64

func (h wakeHeap) Len() int           { return len(h) }
func (h wakeHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h wakeHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *wakeHeap) Push(x any)        { *h = append(*h, x.(float64)) }
func (h *wakeHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func endPriority(k TaskKind) int {
	switch k {
	case Weight:
		return 0
	case Backward:
		return 1
	default:
		return 2
	}
}

type buildState struct {
	rc   ResolvedConfig
	mode Mode

	defs  map[TaskID]taskDef
	slots map[TaskID]taskSlot

	stageFree []float64
	mem       []int64
	phase     [][]uint8 // 0=new, 1=activation held, 2=weight memory held, 3=done

	events    endHeap
	wakes     wakeHeap
	current   float64
	scheduled int
	peak      []int64
	peakAt    []float64
}

// Build1F1B constructs a standard 1F1B schedule. Its backward task represents
// the whole backward pass (duration TB+TW), so no W task is emitted.
func Build1F1B(cfg Config) (*Schedule, error) {
	return build(cfg, Mode1F1B)
}

// BuildZeroBubble constructs a split F/B/W schedule. Ready B tasks run first,
// ready F tasks run next when memory permits, and ready W tasks fill any
// remaining idle slot. When an F is memory-blocked, the oldest ready W is
// forced immediately to release its retained activation memory.
func BuildZeroBubble(cfg Config) (*Schedule, error) {
	return build(cfg, ModeZeroBubble)
}

// Build is a mode-dispatching convenience function.
func Build(cfg Config, mode Mode) (*Schedule, error) {
	switch mode {
	case Mode1F1B:
		return Build1F1B(cfg)
	case ModeZeroBubble:
		return BuildZeroBubble(cfg)
	default:
		return nil, fmt.Errorf("unknown schedule mode %q", mode)
	}
}

func build(cfg Config, mode Mode) (*Schedule, error) {
	rc, err := cfg.Resolve()
	if err != nil {
		return nil, err
	}
	b := &buildState{
		rc:        rc,
		mode:      mode,
		defs:      make(map[TaskID]taskDef),
		slots:     make(map[TaskID]taskSlot),
		stageFree: make([]float64, rc.P),
		mem:       make([]int64, rc.P),
		phase:     make([][]uint8, rc.P),
		peak:      make([]int64, rc.P),
		peakAt:    make([]float64, rc.P),
	}
	for s := range b.phase {
		b.phase[s] = make([]uint8, rc.M)
	}
	heap.Init(&b.events)
	heap.Init(&b.wakes)
	b.makeDefinitions()

	if err := b.run(); err != nil {
		return nil, err
	}

	tasks := make([]Task, 0, len(b.slots))
	for _, slot := range b.slots {
		tasks = append(tasks, slot.task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].ID.Stage != tasks[j].ID.Stage {
			return tasks[i].ID.Stage < tasks[j].ID.Stage
		}
		if !nearlyEqual(tasks[i].Start, tasks[j].Start) {
			return tasks[i].Start < tasks[j].Start
		}
		return tasks[i].ID.Kind < tasks[j].ID.Kind
	})

	sched := &Schedule{
		Mode:         mode,
		Config:       cfg,
		Tasks:        tasks,
		PeakMemory:   append([]int64(nil), b.peak...),
		PeakMemoryAt: append([]float64(nil), b.peakAt...),
	}
	for _, task := range tasks {
		if task.End > sched.Makespan {
			sched.Makespan = task.End
		}
		sched.Work += task.Duration()
	}
	sched.Capacity = float64(rc.P) * sched.Makespan
	sched.BubbleTime = sched.Capacity - sched.Work
	if sched.BubbleTime < 0 && sched.BubbleTime > -timeEpsilon {
		sched.BubbleTime = 0
	}
	if sched.Capacity > 0 {
		sched.BubbleRate = sched.BubbleTime / sched.Capacity
		sched.Utilization = sched.Work / sched.Capacity
	}
	for stage, peak := range sched.PeakMemory {
		if peak > sched.MaxPeakMemory {
			sched.MaxPeakMemory = peak
			sched.MaxPeakMemoryStage = stage
			sched.MaxPeakMemoryAt = sched.PeakMemoryAt[stage]
		}
	}
	sched.Comms = b.makeComms(sched)

	report := Audit(sched)
	if !report.OK {
		return nil, fmt.Errorf("generated %s schedule failed validation: %s", mode, report.Error())
	}
	return sched, nil
}

func (b *buildState) makeDefinitions() {
	rc := b.rc
	for s := 0; s < rc.P; s++ {
		for m := 0; m < rc.M; m++ {
			f := TaskID{Kind: Forward, Microbatch: m, Stage: s}
			var fdeps []TaskID
			if s > 0 {
				fdeps = []TaskID{{Kind: Forward, Microbatch: m, Stage: s - 1}}
			}
			b.defs[f] = taskDef{id: f, deps: fdeps, duration: rc.Stages[s].TF}

			bk := TaskID{Kind: Backward, Microbatch: m, Stage: s}
			bdeps := []TaskID{f}
			if s+1 < rc.P {
				bdeps = append(bdeps, TaskID{Kind: Backward, Microbatch: m, Stage: s + 1})
			}
			bdur := rc.Stages[s].TB
			if b.mode == Mode1F1B {
				bdur += rc.Stages[s].TW
			}
			b.defs[bk] = taskDef{id: bk, deps: bdeps, duration: bdur}

			if b.mode == ModeZeroBubble {
				w := TaskID{Kind: Weight, Microbatch: m, Stage: s}
				b.defs[w] = taskDef{
					id:       w,
					deps:     []TaskID{f, bk},
					duration: rc.Stages[s].TW,
				}
			}
		}
	}
}

func (b *buildState) run() error {
	for {
		if err := b.finishEventsThrough(b.current); err != nil {
			return err
		}
		if err := b.scheduleReady(b.current); err != nil {
			return err
		}
		if b.scheduled == len(b.defs) {
			return nil
		}
		if b.events.Len() == 0 && b.wakes.Len() == 0 {
			var pending []string
			for id := range b.defs {
				if _, ok := b.slots[id]; !ok {
					pending = append(pending, id.String())
				}
			}
			sort.Strings(pending)
			if len(pending) > 12 {
				pending = pending[:12]
			}
			return fmt.Errorf("schedule stalled at t=%.6f with %d/%d tasks; pending=%v stageFree=%v mem=%v phase=%v",
				b.current, b.scheduled, len(b.defs), pending, b.stageFree, b.mem, b.phase)
		}
		next := infinity
		if b.events.Len() > 0 {
			next = b.events[0].at
		}
		if b.wakes.Len() > 0 && b.wakes[0] < next {
			next = b.wakes[0]
		}
		if next < b.current-timeEpsilon {
			return fmt.Errorf("internal event ordering error at t=%.6f (event %.6f)", b.current, next)
		}
		if nearlyEqual(next, b.current) {
			return fmt.Errorf("internal zero-time event at t=%.6f", b.current)
		}
		b.current = next
	}
}

func (b *buildState) finishEventsThrough(t float64) error {
	for b.wakes.Len() > 0 && b.wakes[0] <= t+timeEpsilon {
		heap.Pop(&b.wakes)
	}
	var ending []endEvent
	for b.events.Len() > 0 && b.events[0].at <= t+timeEpsilon {
		ending = append(ending, heap.Pop(&b.events).(endEvent))
	}
	if len(ending) == 0 {
		return nil
	}
	sort.SliceStable(ending, func(i, j int) bool {
		return endPriority(ending[i].id.Kind) < endPriority(ending[j].id.Kind)
	})
	for _, ev := range ending {
		slot := b.slots[ev.id]
		if slot.done {
			return fmt.Errorf("task %s completed twice", ev.id)
		}
		s := ev.id.Stage
		m := ev.id.Microbatch
		switch ev.id.Kind {
		case Backward:
			if b.phase[s][m] != 1 {
				return fmt.Errorf("task %s completed in bad phase %d", ev.id, b.phase[s][m])
			}
			b.mem[s] -= b.rc.Stages[s].MemF
			if b.mode == ModeZeroBubble {
				b.mem[s] += b.rc.Stages[s].MemW
				b.phase[s][m] = 2
			} else {
				b.phase[s][m] = 3
			}
		case Weight:
			if b.phase[s][m] != 2 {
				return fmt.Errorf("task %s completed in bad phase %d", ev.id, b.phase[s][m])
			}
			b.mem[s] -= b.rc.Stages[s].MemW
			b.phase[s][m] = 3
		case Forward:
			// A forward completion does not change the memory lifecycle.
		}
		if b.mem[s] < 0 {
			return fmt.Errorf("stage %d: negative memory %d after %s", s, b.mem[s], ev.id)
		}
		if b.mem[s] > b.rc.Stages[s].MemLimit {
			return fmt.Errorf("stage %d: memory %d exceeds limit %d after %s",
				s, b.mem[s], b.rc.Stages[s].MemLimit, ev.id)
		}
		b.notePeak(s, t)
		slot.done = true
		b.slots[ev.id] = slot
	}
	return nil
}

func (b *buildState) scheduleReady(t float64) error {
	for s := 0; s < b.rc.P; s++ {
		if b.stageFree[s] > t+timeEpsilon {
			continue
		}
		id, ok, err := b.chooseTask(s, t)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := b.startTask(id, t); err != nil {
			return err
		}
	}
	return nil
}

func (b *buildState) chooseTask(s int, t float64) (TaskID, bool, error) {
	bReady, bMemoryBlocked := b.readyBackwardCandidates(s, t)
	if len(bReady) != 0 {
		return bReady[0], true, nil
	}
	if len(bMemoryBlocked) != 0 {
		if w, ok := b.firstReadyWeight(s, t); ok {
			return w, true, nil
		}
		return TaskID{}, false, nil
	}

	fID := TaskID{Kind: Forward, Microbatch: b.firstUnscheduledForward(s), Stage: s}
	hasF := fID.Microbatch >= 0
	if hasF {
		if b.forwardReady(fID, t) {
			if b.forwardFits(s) {
				return fID, true, nil
			}
			if w, ok := b.firstReadyWeight(s, t); ok {
				return w, true, nil
			}
			return TaskID{}, false, nil
		}
	}

	if b.mode != ModeZeroBubble {
		return TaskID{}, false, nil
	}
	w, hasW := b.firstReadyWeight(s, t)
	if !hasW {
		return TaskID{}, false, nil
	}
	return w, true, nil
}

// forwardFits reserves both the forward allocation and the memory retained
// after B. The latter is normally smaller than MemF, but the max form keeps
// the admission rule safe when an unusual configuration has MemW > MemF.
func (b *buildState) forwardFits(s int) bool {
	need := b.rc.Stages[s].MemF
	if b.mode == ModeZeroBubble && b.rc.Stages[s].MemW > need {
		need = b.rc.Stages[s].MemW
	}
	return b.mem[s]+need <= b.rc.Stages[s].MemLimit
}

func (b *buildState) readyBackwardCandidates(s int, t float64) (ready, memoryBlocked []TaskID) {
	for m := 0; m < b.rc.M; m++ {
		bID := TaskID{Kind: Backward, Microbatch: m, Stage: s}
		if _, exists := b.slots[bID]; exists {
			continue
		}
		if !b.done(TaskID{Kind: Forward, Microbatch: m, Stage: s}, t) {
			continue
		}
		if s+1 < b.rc.P && !b.done(TaskID{Kind: Backward, Microbatch: m, Stage: s + 1}, t-b.rc.TComm) {
			continue
		}
		post := b.mem[s] - b.rc.Stages[s].MemF
		if b.mode == ModeZeroBubble {
			post += b.rc.Stages[s].MemW
		}
		if post <= b.rc.Stages[s].MemLimit {
			ready = append(ready, bID)
		} else {
			memoryBlocked = append(memoryBlocked, bID)
		}
	}
	return ready, memoryBlocked
}

func (b *buildState) firstReadyWeight(s int, t float64) (TaskID, bool) {
	if b.mode != ModeZeroBubble {
		return TaskID{}, false
	}
	for m := 0; m < b.rc.M; m++ {
		w := TaskID{Kind: Weight, Microbatch: m, Stage: s}
		if _, exists := b.slots[w]; exists {
			continue
		}
		if !b.done(TaskID{Kind: Backward, Microbatch: m, Stage: s}, t) {
			continue
		}
		return w, true
	}
	return TaskID{}, false
}

func (b *buildState) firstUnscheduledForward(s int) int {
	for m := 0; m < b.rc.M; m++ {
		id := TaskID{Kind: Forward, Microbatch: m, Stage: s}
		if _, exists := b.slots[id]; !exists {
			return m
		}
	}
	return -1
}

func (b *buildState) forwardReady(id TaskID, t float64) bool {
	if id.Stage == 0 {
		return true
	}
	up := TaskID{Kind: Forward, Microbatch: id.Microbatch, Stage: id.Stage - 1}
	return b.done(up, t-b.rc.TComm)
}

func (b *buildState) startTask(id TaskID, start float64) error {
	def, ok := b.defs[id]
	if !ok {
		return fmt.Errorf("unknown task %s", id)
	}
	s := id.Stage
	m := id.Microbatch
	end := start + def.duration
	switch id.Kind {
	case Forward:
		if b.phase[s][m] != 0 {
			return fmt.Errorf("task %s started in phase %d", id, b.phase[s][m])
		}
		b.mem[s] += b.rc.Stages[s].MemF
		if b.mem[s] > b.rc.Stages[s].MemLimit {
			return fmt.Errorf("stage %d: starting %s needs %d memory, limit %d",
				s, id, b.mem[s], b.rc.Stages[s].MemLimit)
		}
		b.phase[s][m] = 1
	case Backward:
		if b.phase[s][m] != 1 {
			return fmt.Errorf("task %s started in phase %d", id, b.phase[s][m])
		}
	case Weight:
		if b.phase[s][m] != 2 {
			return fmt.Errorf("task %s started in phase %d", id, b.phase[s][m])
		}
	}

	task := Task{ID: id, Start: start, End: end, Deps: append([]TaskID(nil), def.deps...)}
	b.slots[id] = taskSlot{task: task}
	b.stageFree[s] = end
	b.scheduled++
	heap.Push(&b.events, endEvent{at: end, id: id})
	if b.rc.TComm > 0 && ((id.Kind == Forward && s+1 < b.rc.P) ||
		(id.Kind == Backward && s > 0)) {
		heap.Push(&b.wakes, end+b.rc.TComm)
	}
	b.notePeak(s, start)
	return nil
}

func (b *buildState) done(id TaskID, by float64) bool {
	slot, ok := b.slots[id]
	return ok && slot.done && slot.task.End <= by+timeEpsilon
}

func (b *buildState) notePeak(s int, t float64) {
	if b.mem[s] > b.peak[s] {
		b.peak[s] = b.mem[s]
		b.peakAt[s] = t
	}
}

func (b *buildState) makeComms(sched *Schedule) []Comm {
	comm := make([]Comm, 0, (b.rc.P-1)*b.rc.M*2)
	if b.rc.P > 1 {
		for m := 0; m < b.rc.M; m++ {
			for s := 1; s < b.rc.P; s++ {
				producer := TaskID{Kind: Forward, Microbatch: m, Stage: s - 1}
				consumer := TaskID{Kind: Forward, Microbatch: m, Stage: s}
				comm = append(comm, b.newComm(
					fmt.Sprintf("act-m%d-s%d-s%d", m, s-1, s),
					ActivationComm, m, s-1, s, producer, consumer,
				))
			}
			for s := 0; s+1 < b.rc.P; s++ {
				producer := TaskID{Kind: Backward, Microbatch: m, Stage: s + 1}
				consumer := TaskID{Kind: Backward, Microbatch: m, Stage: s}
				comm = append(comm, b.newComm(
					fmt.Sprintf("grad-m%d-s%d-s%d", m, s+1, s),
					GradientComm, m, s+1, s, producer, consumer,
				))
			}
		}
	}
	sort.Slice(comm, func(i, j int) bool {
		if comm[i].Kind != comm[j].Kind {
			return comm[i].Kind < comm[j].Kind
		}
		if comm[i].Microbatch != comm[j].Microbatch {
			return comm[i].Microbatch < comm[j].Microbatch
		}
		if comm[i].FromStage != comm[j].FromStage {
			return comm[i].FromStage < comm[j].FromStage
		}
		return comm[i].ToStage < comm[j].ToStage
	})
	return comm
}

func (b *buildState) newComm(id string, kind CommKind, m, from, to int, producer, consumer TaskID) Comm {
	end := b.slots[producer].task.End
	return Comm{
		ID:         id,
		Kind:       kind,
		Microbatch: m,
		FromStage:  from,
		ToStage:    to,
		Producer:   producer,
		Consumer:   consumer,
		SendStart:  end,
		SendEnd:    end + b.rc.TComm,
		RecvStart:  end,
		RecvEnd:    end + b.rc.TComm,
	}
}
