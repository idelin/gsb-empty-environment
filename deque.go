package worksteal

import "sync"

// deque is a mutex-protected double-ended queue of tasks.
// The owner pops from the tail (LIFO); thieves steal from the head (FIFO).
// Pop and steal are serialized by the same mutex and both remove the task,
// so a task can never be handed out twice.
type deque struct {
	mu    sync.Mutex
	tasks []Task
}

func (d *deque) push(t Task) {
	d.mu.Lock()
	d.tasks = append(d.tasks, t)
	d.mu.Unlock()
}

// pop removes and returns the newest task (tail), or nil if empty.
func (d *deque) pop() Task {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.tasks)
	if n == 0 {
		return nil
	}
	t := d.tasks[n-1]
	d.tasks[n-1] = nil
	d.tasks = d.tasks[:n-1]
	return t
}

// steal removes and returns the oldest task (head), or nil if empty.
func (d *deque) steal() Task {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.tasks) == 0 {
		return nil
	}
	t := d.tasks[0]
	d.tasks[0] = nil
	d.tasks = d.tasks[1:]
	return t
}
