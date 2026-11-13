package netsec

import (
	"context"
	"sync"
)

// Executor receives task handles from SpawnAsync. A deterministic scheduler is
// useful for tests; production hosts can provide a worker-pool implementation.
type Executor interface {
	Submit(*Task) error
}

type ScheduleMode uint8

const (
	ScheduleFIFO ScheduleMode = iota
	ScheduleEarliestDeadline
)

// ScheduleEvent is a deterministic record of one task execution.
type ScheduleEvent struct {
	TaskID    uint64
	RequestID string
	StartedAt Instant
	Err       error
}

// DeterministicScheduler executes submitted task handles in a stable order.
// It never launches goroutines, so the same submission order and manual clock
// always produce the same event trace.
type DeterministicScheduler struct {
	mu sync.Mutex

	clock  Clock
	mode   ScheduleMode
	queue  []*Task
	events []ScheduleEvent
	closed bool
}

func NewDeterministicScheduler(clock Clock, modes ...ScheduleMode) *DeterministicScheduler {
	mode := ScheduleFIFO
	if len(modes) > 0 {
		mode = modes[0]
	}
	if clock == nil {
		clock = NewSystemClock()
	}
	return &DeterministicScheduler{clock: clock, mode: mode}
}

func (s *DeterministicScheduler) Submit(task *Task) error {
	if s == nil {
		return ErrSchedulerClosed
	}
	if task == nil {
		return ErrInvalidToken
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSchedulerClosed
	}
	s.queue = append(s.queue, task)
	return nil
}

func (s *DeterministicScheduler) next() *Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil
	}
	index := 0
	if s.mode == ScheduleEarliestDeadline {
		for i := 1; i < len(s.queue); i++ {
			if s.queue[i].deadline.Before(s.queue[index].deadline) ||
				(s.queue[i].deadline == s.queue[index].deadline &&
					s.queue[i].id < s.queue[index].id) {
				index = i
			}
		}
	}
	task := s.queue[index]
	copy(s.queue[index:], s.queue[index+1:])
	s.queue = s.queue[:len(s.queue)-1]
	return task
}

// Step executes exactly one queued task and returns its event.
func (s *DeterministicScheduler) Step(ctx context.Context) (ScheduleEvent, bool) {
	task := s.next()
	if task == nil {
		return ScheduleEvent{}, false
	}
	startedAt := s.clock.Now()
	err := task.Run(ctx)
	event := ScheduleEvent{
		TaskID:    task.ID(),
		RequestID: task.RequestID(),
		StartedAt: startedAt,
		Err:       err,
	}
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	return event, true
}

// Run drains the scheduler in mode order.
func (s *DeterministicScheduler) Run(ctx context.Context) []ScheduleEvent {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			break
		}
		if _, ok := s.Step(ctx); !ok {
			break
		}
	}
	events := s.Events()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return events
}

func (s *DeterministicScheduler) Events() []ScheduleEvent {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ScheduleEvent(nil), s.events...)
}

func (s *DeterministicScheduler) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

func (s *DeterministicScheduler) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.queue = nil
	s.mu.Unlock()
}
