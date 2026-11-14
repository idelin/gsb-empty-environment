package netsec

import (
	"context"
	"sync"
)

// TaskStore persists export tasks. Implementations must guarantee that Claim
// is atomic: concurrent claims for the same task can only succeed once.
type TaskStore interface {
	// Put stores a new task.
	Put(ctx context.Context, task *Task) error
	// Get returns an independent copy of a task, or ErrTaskNotFound.
	Get(ctx context.Context, id string) (*Task, error)
	// Claim atomically transitions a pending task to claimed. Reuse of an
	// already claimed or consumed task returns ErrTaskReused. Unknown
	// identifiers return ErrTaskNotFound.
	Claim(ctx context.Context, id string) (*Task, error)
	// MarkConsumed records that a claimed task finished successfully.
	MarkConsumed(ctx context.Context, id string) error
	// ReleaseClaim moves a claimed task back to pending after a failed run so
	// transient errors do not permanently burn a task. It is a no-op style
	// failure if the task is no longer claimed.
	ReleaseClaim(ctx context.Context, id string) error
}

// MemoryTaskStore is an in-process TaskStore safe for concurrent use.
type MemoryTaskStore struct {
	mu    sync.Mutex
	tasks map[string]*Task
}

// NewMemoryTaskStore constructs an empty MemoryTaskStore.
func NewMemoryTaskStore() *MemoryTaskStore {
	return &MemoryTaskStore{tasks: make(map[string]*Task)}
}

// Put implements TaskStore.
func (s *MemoryTaskStore) Put(_ context.Context, task *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[task.ID] = task.Clone()
	return nil
}

// Get implements TaskStore.
func (s *MemoryTaskStore) Get(_ context.Context, id string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return nil, ErrTaskNotFound
	}
	return task.Clone(), nil
}

// Claim implements TaskStore.
func (s *MemoryTaskStore) Claim(_ context.Context, id string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return nil, ErrTaskNotFound
	}
	if task.Status != StatusPending {
		return nil, ErrTaskReused
	}
	task.Status = StatusClaimed
	return task.Clone(), nil
}

// MarkConsumed implements TaskStore.
func (s *MemoryTaskStore) MarkConsumed(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	task.Status = StatusConsumed
	return nil
}

// ReleaseClaim implements TaskStore.
func (s *MemoryTaskStore) ReleaseClaim(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	if task.Status == StatusClaimed {
		task.Status = StatusPending
	}
	return nil
}
