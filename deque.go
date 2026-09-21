package worksteal

import "sync"

// deque is a double-ended queue guarded by a single mutex.
// The owning worker pushes and pops at the bottom (LIFO);
// thieves steal from the top (FIFO). Because both popBottom
// and stealTop take the same mutex, a task can be handed out
// exactly once: steal and local pop can never race to deliver
// the same task twice.
type deque struct {
	mu    sync.Mutex
	items []Task
}

func (d *deque) pushBottom(t Task) {
	d.mu.Lock()
	d.items = append(d.items, t)
	d.mu.Unlock()
}

func (d *deque) popBottom() (Task, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.items)
	if n == 0 {
		return nil, false
	}
	t := d.items[n-1]
	d.items[n-1] = nil
	d.items = d.items[:n-1]
	return t, true
}

func (d *deque) stealTop() (Task, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.items) == 0 {
		return nil, false
	}
	t := d.items[0]
	d.items[0] = nil
	copy(d.items, d.items[1:])
	d.items[len(d.items)-1] = nil
	d.items = d.items[:len(d.items)-1]
	return t, true
}
