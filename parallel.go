package roaring

import "sync"

// parallelOp evaluates a binary set operation container-by-container on a
// pool of workers fed through a lock-free MPMC queue. Each result slot is
// written by exactly one worker, so no synchronization is needed on the
// output bitmap beyond the atomic slot installation.
func parallelOp(a, b *Bitmap, workers int, kind opKind) *Bitmap {
	if workers < 1 {
		workers = 1
	}
	out := &Bitmap{}

	// Collect the container indices worth visiting. For AND both sides
	// must be non-empty; for OR/XOR either side suffices.
	tasks := make([]int, 0, 1024)
	for k := 0; k < numContainers; k++ {
		hasA := loadContainer(a, k) != nil
		hasB := loadContainer(b, k) != nil
		if (kind == opAnd && hasA && hasB) || (kind != opAnd && (hasA || hasB)) {
			tasks = append(tasks, k)
		}
	}
	if len(tasks) == 0 {
		return out
	}
	if workers == 1 || len(tasks) == 1 {
		for _, k := range tasks {
			computeSlot(a, b, out, k, kind)
		}
		return out
	}

	q := NewLFQueue(len(tasks))
	for _, k := range tasks {
		q.Push(k) // never fails: capacity >= len(tasks)
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				v, ok := q.Pop()
				if !ok {
					return // queue drained: all tasks claimed
				}
				computeSlot(a, b, out, v.(int), kind)
			}
		}()
	}
	wg.Wait()
	return out
}

// ParallelAnd computes a AND b using the given number of goroutines.
func (a *Bitmap) ParallelAnd(b *Bitmap, workers int) *Bitmap {
	return parallelOp(a, b, workers, opAnd)
}

// ParallelOr computes a OR b using the given number of goroutines.
func (a *Bitmap) ParallelOr(b *Bitmap, workers int) *Bitmap {
	return parallelOp(a, b, workers, opOr)
}

// ParallelXor computes a XOR b using the given number of goroutines.
func (a *Bitmap) ParallelXor(b *Bitmap, workers int) *Bitmap {
	return parallelOp(a, b, workers, opXor)
}
