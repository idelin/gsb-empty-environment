// Package deter is a deterministic task runtime with record and replay.
//
// Tasks are plain functions injected by the caller. They interact with the
// outside world only through Env, so every effect — scheduling choices,
// virtual clock advancement and external call results (including injected
// failures) — is captured in an ordered event log while recording.
//
// Replaying the same log against the same task functions drives the
// scheduler from the recording and verifies, step by step, that the tasks
// issue exactly the recorded requests. The first mismatch is reported as a
// *Divergence carrying the exact step index. The runtime never reads the
// real clock, so wall time cannot mask divergent execution traces.
package deter

import (
	"errors"
	"fmt"
	"time"
)

// Op identifies the kind of operation a task asked the runtime to perform.
type Op string

const (
	OpSpawn Op = "spawn" // a task spawned a child task (Arg = child name)
	OpSleep Op = "sleep" // a task slept on the virtual clock (Arg = duration)
	OpCall  Op = "call"  // a task performed an external call (Arg = name+arg)
	OpDone  Op = "done"  // a task returned
)

// Event is one recorded step of an execution. The event sequence fully
// describes a run: which task was scheduled (Task), when on the virtual
// clock (At), what it requested (Op, Arg) and, for external calls, which
// result or injected failure was delivered to it (Result, Err).
type Event struct {
	Task   int
	Op     Op
	Arg    string
	Result string
	Err    bool
	At     time.Duration
}

func (e Event) String() string {
	s := fmt.Sprintf("task=%d op=%s arg=%q at=%s", e.Task, e.Op, e.Arg, e.At)
	if e.Op == OpCall {
		s += fmt.Sprintf(" result=%q err=%v", e.Result, e.Err)
	}
	return s
}

// TaskFunc is a unit of work. Task functions are injected by the caller.
type TaskFunc func(*Env)

// CallHandler resolves external calls while recording. It is injected by the
// caller and is the source of external results and failures. It is never
// invoked during replay; replayed tasks receive the recorded outcomes.
type CallHandler func(name, arg string) (string, error)

// Divergence reports the first step at which a replayed execution disagrees
// with the recording.
type Divergence struct {
	Step     int
	Expected string
	Actual   string
}

func (d *Divergence) Error() string {
	return fmt.Sprintf("replay divergence at step %d: expected %s, got %s",
		d.Step, d.Expected, d.Actual)
}

type mode int

const (
	record mode = iota
	replay
)

// request is sent from a task goroutine to the scheduler.
type request struct {
	op   Op
	arg  string // serialized argument, recorded in the event
	d    time.Duration
	name string // call name (OpCall)
	carg string // call argument (OpCall)
	fn   TaskFunc
}

// response is sent from the scheduler back to the requesting task.
type response struct {
	result string
	isErr  bool
}

type task struct {
	id       int
	name     string
	chReq    chan request
	chResp   chan response
	pending  response // delivered on next resume
	sleeping bool
	wakeAt   time.Duration
	done     bool
}

// Runtime executes tasks deterministically, one step at a time, on a virtual
// clock. Exactly one task goroutine is unblocked at any moment; all
// synchronization uses unbuffered channels, which also establishes the
// happens-before edges that keep shared task state race-free.
type Runtime struct {
	mode     mode
	handler  CallHandler
	recorded []Event // replay source
	log      []Event // record sink
	step     int
	now      time.Duration
	tasks    []*task
	ready    []int // FIFO queue of runnable task IDs
}

// NewRecord returns a Runtime that records its execution. handler resolves
// external calls and may inject failures; it may be nil if tasks never call.
func NewRecord(handler CallHandler) *Runtime {
	return &Runtime{mode: record, handler: handler}
}

// NewReplay returns a Runtime that replays recorded and verifies every step.
func NewReplay(recorded []Event) *Runtime {
	return &Runtime{mode: replay, recorded: recorded}
}

// Now reports the virtual clock. It never reads the real clock.
func (r *Runtime) Now() time.Duration { return r.now }

// Env is the capability handed to a task; all runtime interaction goes
// through it so every effect can be recorded and replayed.
type Env struct {
	rt   *Runtime
	task *task
}

func (e *Env) exchange(req request) response {
	e.task.chReq <- req
	return <-e.task.chResp
}

// Sleep blocks the task until the virtual clock has advanced by d.
func (e *Env) Sleep(d time.Duration) {
	e.exchange(request{op: OpSleep, arg: d.String(), d: d})
}

// Call performs an external call. While recording, the injected CallHandler
// produces the result or failure; during replay the recorded outcome is
// delivered instead.
func (e *Env) Call(name, arg string) (string, error) {
	resp := e.exchange(request{op: OpCall, arg: name + " " + arg, name: name, carg: arg})
	if resp.isErr {
		return "", errors.New(resp.result)
	}
	return resp.result, nil
}

// Spawn starts a child task. The child becomes runnable after the parent.
func (e *Env) Spawn(name string, fn TaskFunc) {
	e.exchange(request{op: OpSpawn, arg: name, fn: fn})
}

// Now reports the virtual clock as seen by the task.
func (e *Env) Now() time.Duration { return e.rt.now }

// Run executes the root task and everything it spawns to completion.
//
// In record mode it returns the recorded event log. In replay mode it
// returns a *Divergence (or another error) unless the execution matches the
// recording exactly, including its length: extra requests past the end of
// the recording and an unconsumed recorded tail are both reported.
func (r *Runtime) Run(name string, fn TaskFunc) ([]Event, error) {
	r.spawn(name, fn)
	for {
		if len(r.ready) == 0 && !r.wakeSleepers() {
			break
		}
		if r.mode == replay && r.step >= len(r.recorded) {
			// The recording is exhausted but tasks still want to run
			// (e.g. a truncated log or an extra request). Resume one
			// task so the divergence can name the offending request.
			return nil, r.extraRequest()
		}
		t, err := r.pick()
		if err != nil {
			return nil, err
		}
		t.chResp <- t.pending // unblock exactly this task
		req := <-t.chReq      // wait for its next request
		if err := r.apply(t, req); err != nil {
			return nil, err
		}
		r.step++
	}
	for _, t := range r.tasks {
		if !t.done {
			return nil, fmt.Errorf("deadlock: task %d (%s) is neither ready nor sleeping", t.id, t.name)
		}
	}
	if r.mode == replay && r.step < len(r.recorded) {
		return nil, &Divergence{
			Step:     r.step,
			Expected: fmt.Sprintf("recorded event {%s}", r.recorded[r.step]),
			Actual:   "all tasks finished (recording has an unconsumed tail)",
		}
	}
	return r.log, nil
}

// spawn registers a new task and starts its goroutine. The goroutine blocks
// until the scheduler resumes it, so task code never runs concurrently.
func (r *Runtime) spawn(name string, fn TaskFunc) {
	t := &task{
		id:     len(r.tasks),
		name:   name,
		chReq:  make(chan request),
		chResp: make(chan response),
	}
	r.tasks = append(r.tasks, t)
	r.ready = append(r.ready, t.id)
	go func() {
		env := &Env{rt: r, task: t}
		<-t.chResp // initial resume
		fn(env)
		t.chReq <- request{op: OpDone}
	}()
}

// pick chooses the next task to run. While recording it pops the FIFO ready
// queue (the choice is captured by the Task field of the next event). During
// replay it must schedule exactly the task the recording names.
func (r *Runtime) pick() (*task, error) {
	if r.mode == record {
		t := r.tasks[r.ready[0]]
		r.ready = r.ready[1:]
		return t, nil
	}
	ev := r.recorded[r.step]
	if ev.Task < 0 || ev.Task >= len(r.tasks) {
		return nil, &Divergence{
			Step:     r.step,
			Expected: fmt.Sprintf("schedule task %d", ev.Task),
			Actual:   "task does not exist",
		}
	}
	t := r.tasks[ev.Task]
	for i, id := range r.ready {
		if id == t.id {
			r.ready = append(r.ready[:i], r.ready[i+1:]...)
			return t, nil
		}
	}
	return nil, &Divergence{
		Step:     r.step,
		Expected: fmt.Sprintf("schedule task %d (%s)", t.id, t.name),
		Actual:   "task is not runnable",
	}
}

// apply handles one request from task t: it records or verifies the event,
// updates scheduler state and computes the response delivered on the next
// resume of t.
func (r *Runtime) apply(t *task, req request) error {
	ev := Event{Task: t.id, Op: req.op, Arg: req.arg, At: r.now}
	if r.mode == replay {
		recorded := r.recorded[r.step]
		if req.op != recorded.Op || req.arg != recorded.Arg {
			return &Divergence{
				Step:     r.step,
				Expected: fmt.Sprintf("task %d to issue %s %q", recorded.Task, recorded.Op, recorded.Arg),
				Actual:   fmt.Sprintf("task %d (%s) issued %s %q", t.id, t.name, req.op, req.arg),
			}
		}
		ev.Result = recorded.Result
		ev.Err = recorded.Err
	}

	var resp response
	switch req.op {
	case OpDone:
		t.done = true
	case OpSleep:
		t.sleeping = true
		t.wakeAt = r.now + req.d
	case OpSpawn:
		r.spawn(req.arg, req.fn)
		r.ready = append(r.ready, t.id)
	case OpCall:
		if r.mode == record {
			res, err := r.call(req)
			if err != nil {
				ev.Result, ev.Err = err.Error(), true
				resp = response{result: err.Error(), isErr: true}
			} else {
				ev.Result = res
				resp = response{result: res}
			}
		} else {
			resp = response{result: ev.Result, isErr: ev.Err}
		}
		r.ready = append(r.ready, t.id)
	}
	t.pending = resp
	if r.mode == record {
		r.log = append(r.log, ev)
	}
	return nil
}

func (r *Runtime) call(req request) (string, error) {
	if r.handler == nil {
		return "", fmt.Errorf("no call handler registered")
	}
	return r.handler(req.name, req.carg)
}

// extraRequest resumes a runnable task past the end of the recording so the
// returned divergence can pinpoint the request the recording does not cover.
func (r *Runtime) extraRequest() error {
	t := r.tasks[r.ready[0]]
	r.ready = r.ready[1:]
	t.chResp <- t.pending
	req := <-t.chReq
	return &Divergence{
		Step:     r.step,
		Expected: "end of recording",
		Actual:   fmt.Sprintf("task %d (%s) issued %s %q", t.id, t.name, req.op, req.arg),
	}
}

// wakeSleepers advances the virtual clock to the earliest pending wake-up
// and moves every task due at that instant to the ready queue. It reports
// whether any task was woken.
func (r *Runtime) wakeSleepers() bool {
	var next *task
	for _, t := range r.tasks {
		if t.sleeping && (next == nil || t.wakeAt < next.wakeAt) {
			next = t
		}
	}
	if next == nil {
		return false
	}
	r.now = next.wakeAt
	for _, t := range r.tasks {
		if t.sleeping && t.wakeAt <= r.now {
			t.sleeping = false
			r.ready = append(r.ready, t.id)
		}
	}
	return true
}
