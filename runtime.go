package detrun

import (
	"errors"
	"fmt"
	"time"
)

// ExternalFunc is injected by the caller (in tests: the test) and performs an
// external request in record mode. Replay mode never calls it; recorded
// results are injected instead.
type ExternalFunc func(req string) (string, error)

// TaskFunc is a task body. Tasks are injected by the caller.
type TaskFunc func(t *Task) error

// ErrAborted is delivered to tasks when the runtime stops early (e.g. after
// a replay divergence) so blocked goroutines can unwind instead of leaking.
var ErrAborted = errors.New("detrun: runtime aborted")

type mode int

const (
	modeRecord mode = iota
	modeReplay
)

type stepKind int

const (
	stepSleep stepKind = iota
	stepCall
	stepDone
)

type stepReq struct {
	kind  stepKind
	sleep time.Duration
	req   string
}

type stepResp struct {
	resp string
	err  error
}

type taskState struct {
	id      int
	fn      TaskFunc
	wake    time.Duration
	asleep  bool
	done    bool
	pending stepResp
	reqCh   chan stepReq
	respCh  chan stepResp
	quit    <-chan struct{}
	err     error
}

// Runtime is a deterministic, cooperative scheduler with a virtual clock.
// It either records a run or replays a previous recording.
type Runtime struct {
	mode   mode
	ext    ExternalFunc
	rec    *Recorder
	rep    *Replayer
	now    time.Duration
	tasks  []*taskState
	quit   chan struct{}
	ran    bool
	runErr error
}

// NewRecord creates a runtime that records its run. ext performs external
// requests; it may be nil if tasks never call Task.Call.
func NewRecord(ext ExternalFunc) *Runtime {
	return &Runtime{mode: modeRecord, ext: ext, rec: &Recorder{}, quit: make(chan struct{})}
}

// NewReplay creates a runtime that replays the given recording. External
// results and failures come from the log; no external func is involved.
func NewReplay(log []Event) *Runtime {
	return &Runtime{mode: modeReplay, rep: NewReplayer(log), quit: make(chan struct{})}
}

// Go registers a task. Tasks must be registered before Run. Registration
// order defines task ids and is part of the deterministic schedule.
func (r *Runtime) Go(fn TaskFunc) int {
	id := len(r.tasks)
	r.tasks = append(r.tasks, &taskState{id: id, fn: fn})
	return id
}

// Now returns the current virtual time. Only valid after Run has finished.
func (r *Runtime) Now() time.Duration { return r.now }

// Log returns the recorded events. Only meaningful in record mode after Run.
func (r *Runtime) Log() []Event { return r.rec.Events() }

// TaskError returns the error returned by task id, if any.
func (r *Runtime) TaskError(id int) error { return r.tasks[id].err }

// Run executes all registered tasks to completion. In replay mode it returns
// a *DivergenceError pinpointing the first mismatching event, including when
// the run produces extra events or the recording has an unconsumed tail.
func (r *Runtime) Run() error {
	if r.ran {
		return errors.New("detrun: Run called twice")
	}
	r.ran = true

	for _, ts := range r.tasks {
		ts.reqCh = make(chan stepReq)
		ts.respCh = make(chan stepResp)
		ts.quit = r.quit
		go r.runTask(ts)
	}

	remaining := len(r.tasks)
	for remaining > 0 {
		ts := r.pickRunnable()
		if ts == nil {
			if err := r.advanceClock(); err != nil {
				return r.abort(err)
			}
			continue
		}
		if err := r.emit(Event{Kind: KindSchedule, Task: ts.id}); err != nil {
			return r.abort(err)
		}
		// Resume the task; it runs alone until its next yield point.
		ts.respCh <- ts.pending
		req := <-ts.reqCh
		switch req.kind {
		case stepDone:
			ts.done = true
			remaining--
		case stepSleep:
			ts.asleep = true
			ts.wake = r.now + req.sleep
			ts.pending = stepResp{}
		case stepCall:
			resp, err := r.doCall(ts.id, req.req)
			if err != nil && r.mode == modeReplay && IsDivergence(err) {
				return r.abort(err)
			}
			ts.pending = stepResp{resp: resp, err: err}
		}
	}

	if r.mode == modeReplay {
		if err := r.rep.finish(); err != nil {
			return r.abort(err)
		}
	}
	close(r.quit)
	return nil
}

func (r *Runtime) abort(err error) error {
	close(r.quit)
	r.runErr = err
	return err
}

func (r *Runtime) runTask(ts *taskState) {
	t := &Task{rt: r, ts: ts}
	select {
	case <-ts.respCh: // first resume
	case <-ts.quit:
		return
	}
	ts.err = ts.fn(t)
	select {
	case ts.reqCh <- stepReq{kind: stepDone}:
	case <-ts.quit:
	}
}

// pickRunnable returns the runnable task with the lowest id, or nil if every
// live task is asleep. The choice is fully deterministic.
func (r *Runtime) pickRunnable() *taskState {
	for _, ts := range r.tasks {
		if !ts.done && !ts.asleep {
			return ts
		}
	}
	return nil
}

// advanceClock jumps the virtual clock to the earliest pending wake-up. It
// never consults wall time.
func (r *Runtime) advanceClock() error {
	next := time.Duration(1<<63 - 1)
	for _, ts := range r.tasks {
		if !ts.done && ts.asleep && ts.wake < next {
			next = ts.wake
		}
	}
	if next == time.Duration(1<<63-1) {
		return errors.New("detrun: deadlock, no runnable or sleeping task")
	}
	r.now = next
	for _, ts := range r.tasks {
		if !ts.done && ts.asleep && ts.wake <= r.now {
			ts.asleep = false
		}
	}
	return r.emit(Event{Kind: KindClock, Task: -1})
}

func (r *Runtime) emit(e Event) error {
	e.Now = r.now
	if r.mode == modeRecord {
		r.rec.append(e)
		return nil
	}
	_, err := r.rep.expect(e)
	return err
}

func (r *Runtime) doCall(taskID int, req string) (string, error) {
	e := Event{Kind: KindExternal, Task: taskID, Now: r.now, Req: req}
	if r.mode == modeRecord {
		if r.ext == nil {
			return "", fmt.Errorf("detrun: task %d called %q but no ExternalFunc was injected", taskID, req)
		}
		resp, callErr := r.ext(req)
		e.Resp = resp
		if callErr != nil {
			e.Err = callErr.Error()
		}
		r.rec.append(e)
		return resp, callErr
	}
	recorded, err := r.rep.expect(e)
	if err != nil {
		return "", err
	}
	if recorded.Err != "" {
		return "", errors.New(recorded.Err)
	}
	return recorded.Resp, nil
}

// Task is the handle a TaskFunc uses to interact with the runtime.
type Task struct {
	rt *Runtime
	ts *taskState
}

// Now returns the current virtual time.
func (t *Task) Now() time.Duration { return t.rt.now }

// Sleep blocks the task until the virtual clock has advanced by d. No real
// time passes.
func (t *Task) Sleep(d time.Duration) error {
	t.ts.reqCh <- stepReq{kind: stepSleep, sleep: d}
	select {
	case <-t.ts.respCh:
		return nil
	case <-t.ts.quit:
		return ErrAborted
	}
}

// Call performs an external request. In record mode the injected
// ExternalFunc runs and the outcome is recorded; in replay mode the recorded
// response or failure is injected without touching the outside world.
func (t *Task) Call(req string) (string, error) {
	t.ts.reqCh <- stepReq{kind: stepCall, req: req}
	select {
	case resp := <-t.ts.respCh:
		return resp.resp, resp.err
	case <-t.ts.quit:
		return "", ErrAborted
	}
}
