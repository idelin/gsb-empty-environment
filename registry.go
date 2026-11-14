package netsec

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// Registry is the whitelist of print queues a service may address. Queue
// names arriving on the wire are only honored when present here.
type Registry struct {
	mu     sync.RWMutex
	queues map[string]*Queue
}

// NewRegistry creates an empty queue registry.
func NewRegistry() *Registry {
	return &Registry{queues: make(map[string]*Queue)}
}

// AddQueue registers a queue. It rejects duplicate queue names and any queue
// whose spool directory overlaps an already registered spool directory, so
// one queue can never address another queue's files.
func (r *Registry) AddQueue(q *Queue) error {
	if q == nil {
		return fmt.Errorf("%w: nil queue", ErrInvalidName)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.queues[q.name]; exists {
		return fmt.Errorf("%w: duplicate queue %q", ErrQueueNotAllowed, q.name)
	}
	for _, other := range r.queues {
		if pathsOverlap(q.spoolDir, other.spoolDir) {
			return fmt.Errorf("%w: spool %q overlaps queue %q spool %q",
				ErrOutsideSpool, q.spoolDir, other.name, other.spoolDir)
		}
	}
	r.queues[q.name] = q
	return nil
}

// Queue returns the registered queue for name, or ErrQueueNotAllowed.
func (r *Registry) Queue(name string) (*Queue, error) {
	r.mu.RLock()
	q, ok := r.queues[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrQueueNotAllowed, name)
	}
	return q, nil
}

// OpenControl resolves a whitelisted queue and opens an admitted control file.
func (r *Registry) OpenControl(ctx context.Context, queueName, controlName string) (*Queue, *File, error) {
	q, err := r.Queue(queueName)
	if err != nil {
		return nil, nil, err
	}
	f, err := q.OpenControl(ctx, controlName)
	if err != nil {
		return q, nil, err
	}
	return q, f, nil
}

// OpenJob resolves a whitelisted queue and opens a complete validated job.
func (r *Registry) OpenJob(ctx context.Context, queueName, controlName string, dataNames []string) (*Job, error) {
	q, err := r.Queue(queueName)
	if err != nil {
		return nil, err
	}
	return q.OpenJob(ctx, controlName, dataNames)
}

// pathsOverlap reports whether two absolute, cleaned directories are equal
// or nested inside one another.
func pathsOverlap(a, b string) bool {
	return a == b ||
		strings.HasPrefix(a, b+sep) ||
		strings.HasPrefix(b, a+sep)
}

var sep = string(filepath.Separator)
