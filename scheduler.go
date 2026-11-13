package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// WriteRequest is one concurrent source write.
type WriteRequest struct {
	Page PageID
	Data []byte
	Kind WriteKind
}

// WriteOutcome is the result of a concurrent write in request order.
type WriteOutcome struct {
	Request    WriteRequest
	Generation Generation
	Err        error
}

// ConcurrentWrites releases all writes through a common start barrier.  Writes
// to the same page are serialized by Memory, so the resulting generations are
// strictly increasing even though the callers run concurrently.
func ConcurrentWrites(mem *Memory, requests ...WriteRequest) []WriteOutcome {
	return ConcurrentWritesContext(context.Background(), mem, requests...)
}

// ConcurrentWritesContext is ConcurrentWrites with cancellation.  A cancelled
// request that has not been admitted returns ctx.Err(); an admitted write is
// allowed to complete so freeze quiescence remains well-defined.
func ConcurrentWritesContext(ctx context.Context, mem *Memory, requests ...WriteRequest) []WriteOutcome {
	out := make([]WriteOutcome, len(requests))
	if mem == nil {
		for i, req := range requests {
			out[i] = WriteOutcome{Request: req, Err: errors.New("netsec: nil memory")}
		}
		return out
	}
	if ctx == nil {
		ctx = context.Background()
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(len(requests))
	for i := range requests {
		i := i
		req := requests[i]
		go func() {
			defer wg.Done()
			select {
			case <-start:
			case <-ctx.Done():
				out[i] = WriteOutcome{Request: req, Err: ctx.Err()}
				return
			}
			kind := req.Kind
			if kind == "" {
				kind = WriteVCPU
			}
			data := append([]byte(nil), req.Data...)
			gen, err := mem.WritePageKind(req.Page, data, kind)
			out[i] = WriteOutcome{Request: req, Generation: gen, Err: err}
		}()
	}
	close(start)
	wg.Wait()
	return out
}

type scheduledWrite struct {
	trigger    EventName
	phase      EventPhase
	occurrence uint64
	request    WriteRequest
	async      bool

	done chan struct{}
	once sync.Once
}

type scheduledResult struct {
	gen Generation
	err error
}

// WriteScheduler drives source writes from named state-machine events.  It can
// run a write asynchronously (the default), producing a genuine race with the
// surrounding copy/acknowledge steps, or synchronously at the event boundary.
type WriteScheduler struct {
	mu     sync.Mutex
	mem    *Memory
	items  []*scheduledWrite
	result map[*scheduledWrite]scheduledResult
	wg     sync.WaitGroup
}

// NewWriteScheduler creates a scheduler for one source image.
func NewWriteScheduler(mem *Memory) *WriteScheduler {
	return &WriteScheduler{mem: mem, result: make(map[*scheduledWrite]scheduledResult)}
}

func (s *WriteScheduler) add(item *scheduledWrite) *scheduledWrite {
	item.done = make(chan struct{})
	s.mu.Lock()
	s.items = append(s.items, item)
	s.wg.Add(1)
	s.mu.Unlock()
	return item
}

// Schedule queues an asynchronous write at the first matching event.
// occurrence is one-based; zero means any occurrence.
func (s *WriteScheduler) Schedule(trigger EventName, phase EventPhase, occurrence uint64, page PageID, data []byte, kind WriteKind) *ScheduledWrite {
	item := &scheduledWrite{
		trigger: trigger, phase: phase, occurrence: occurrence,
		request: WriteRequest{Page: page, Data: append([]byte(nil), data...), Kind: kind},
		async:   true,
	}
	s.add(item)
	return &ScheduledWrite{s: s, item: item}
}

// ScheduleSync queues a write that runs inline at the event boundary.
func (s *WriteScheduler) ScheduleSync(trigger EventName, phase EventPhase, occurrence uint64, page PageID, data []byte, kind WriteKind) *ScheduledWrite {
	item := &scheduledWrite{
		trigger: trigger, phase: phase, occurrence: occurrence,
		request: WriteRequest{Page: page, Data: append([]byte(nil), data...), Kind: kind},
		async:   false,
	}
	s.add(item)
	return &ScheduledWrite{s: s, item: item}
}

// Handle implements FaultInjector.  It never fails the triggering event.
func (s *WriteScheduler) Handle(ev Event) error {
	s.mu.Lock()
	var matches []*scheduledWrite
	remaining := s.items[:0]
	for _, item := range s.items {
		if item.trigger != ev.Name || item.phase != ev.Phase {
			remaining = append(remaining, item)
			continue
		}
		if item.occurrence != 0 && item.occurrence != ev.Occurrence {
			remaining = append(remaining, item)
			continue
		}
		matches = append(matches, item)
	}
	s.items = remaining
	s.mu.Unlock()
	for _, item := range matches {
		s.run(item)
	}
	return nil
}

func (s *WriteScheduler) run(item *scheduledWrite) {
	run := func() {
		defer s.wg.Done()
		if s.mem == nil {
			s.mu.Lock()
			s.result[item] = scheduledResult{err: errors.New("netsec: nil scheduler memory")}
			s.mu.Unlock()
			item.once.Do(func() { close(item.done) })
			return
		}
		gen, err := s.mem.WritePageKind(item.request.Page, item.request.Data, item.request.Kind)
		s.mu.Lock()
		s.result[item] = scheduledResult{gen: gen, err: err}
		s.mu.Unlock()
		item.once.Do(func() { close(item.done) })
	}
	if item.async {
		go run()
		return
	}
	run()
}

// Wait blocks until all scheduled writes have run or ctx expires.
func (s *WriteScheduler) Wait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ch := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(ch)
	}()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Pending returns the number of writes not yet triggered.
func (s *WriteScheduler) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

// ScheduledWrite is a handle for one scheduled write.
type ScheduledWrite struct {
	s    *WriteScheduler
	item *scheduledWrite
}

// Wait waits for this write and returns its generation/error.
func (w *ScheduledWrite) Wait(ctx context.Context) (Generation, error) {
	if w == nil || w.s == nil || w.item == nil {
		return 0, errors.New("netsec: nil scheduled write")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-w.item.done:
		w.s.mu.Lock()
		res := w.s.result[w.item]
		w.s.mu.Unlock()
		return res.gen, res.err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// WaitWithTimeout is a convenience helper.
func (w *ScheduledWrite) WaitWithTimeout(d time.Duration) (Generation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return w.Wait(ctx)
}

// ChainFaults returns an injector that runs each fault in order.
func ChainFaults(faults ...FaultInjector) FaultInjector {
	return FaultFunc(func(ev Event) error {
		var errs []error
		for _, f := range faults {
			if f == nil {
				continue
			}
			if err := f.Inject(ev); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	})
}

// TriggerOnce is a small helper for tests that need an action at one specific
// even occurrence.
func TriggerOnce(name EventName, phase EventPhase, occurrence uint64, fn func(Event) error) FaultInjector {
	var once sync.Once
	return FaultFunc(func(ev Event) error {
		if ev.Name != name || ev.Phase != phase || (occurrence != 0 && ev.Occurrence != occurrence) {
			return nil
		}
		var err error
		once.Do(func() { err = fn(ev) })
		return err
	})
}

// DescribeEvent renders an event for test diagnostics.
func DescribeEvent(ev Event) string {
	return fmt.Sprintf("#%d %s/%s page=%d gen=%d%s", ev.Seq, ev.Name, ev.Phase, ev.Fields.Page, ev.Fields.Generation, eventReason(ev))
}

func eventReason(ev Event) string {
	if ev.Fields.Reason == "" {
		return ""
	}
	return " reason=" + ev.Fields.Reason
}
