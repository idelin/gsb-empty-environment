package compaction

import (
	"context"
	"sync"
	"time"
)

// Scheduler runs background compaction workers against an Engine.
type Scheduler struct {
	engine *Engine

	mu      sync.Mutex
	workers int
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool

	// Interval is the idle poll interval. Defaults to 5ms.
	Interval time.Duration
	// MaxConcurrent caps simultaneous compaction jobs. Zero means one worker.
	MaxConcurrent int

	// OnJob, when set, is invoked after each compaction finishes.
	OnJob func(result *CompactionResult, err error)
}

// NewScheduler builds a scheduler bound to e.
func NewScheduler(e *Engine) *Scheduler {
	return &Scheduler{engine: e, Interval: 5 * time.Millisecond, MaxConcurrent: 1}
}

// Start launches MaxConcurrent background workers. Repeated calls are no-ops.
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.started = true
	n := s.MaxConcurrent
	if n < 1 {
		n = 1
	}
	s.workers = n
	for i := 0; i < n; i++ {
		s.wg.Add(1)
		go s.worker(ctx)
	}
}

// Close stops the workers and waits for in-flight compactions to finish.
func (s *Scheduler) Close() {
	s.mu.Lock()
	cancel := s.cancel
	started := s.started
	s.started = false
	s.mu.Unlock()
	if !started {
		return
	}
	cancel()
	s.wg.Wait()
}

func (s *Scheduler) worker(ctx context.Context) {
	defer s.wg.Done()
	interval := s.Interval
	if interval <= 0 {
		interval = 5 * time.Millisecond
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		job, err := s.engine.PickCompaction()
		if err == ErrNoCompaction {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		if err != nil {
			continue
		}
		result, runErr := s.engine.RunCompaction(job)
		if s.OnJob != nil {
			s.OnJob(result, runErr)
		}
	}
}

// RunOnce picks and executes a single eligible compaction. It returns
// ErrNoCompaction when nothing is eligible.
func (s *Scheduler) RunOnce() (*CompactionResult, error) {
	job, err := s.engine.PickCompaction()
	if err != nil {
		return nil, err
	}
	return s.engine.RunCompaction(job)
}

// RunUntilStable repeatedly compacts until no eligible job remains. It returns
// the number of compactions executed.
func (s *Scheduler) RunUntilStable() (int, error) {
	count := 0
	for {
		result, err := s.RunOnce()
		if err == ErrNoCompaction {
			return count, nil
		}
		if err != nil {
			return count, err
		}
		if result != nil {
			count++
		}
	}
}
