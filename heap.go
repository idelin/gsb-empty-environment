package hpastar

// priorityQueue is a generic binary min-heap keyed by float64 priority.
type priorityQueue[T any] struct {
	entries  []pqEntry[T]
	lessFunc func(a, b pqEntry[T]) bool
}

type pqEntry[T any] struct {
	item     T
	priority float64
}

func newPriorityQueue[T any](less func(a, b pqEntry[T]) bool) *priorityQueue[T] {
	return &priorityQueue[T]{lessFunc: less}
}

func (pq *priorityQueue[T]) push(item T, priority float64) {
	pq.entries = append(pq.entries, pqEntry[T]{item: item, priority: priority})
	pq.up(len(pq.entries) - 1)
}

func (pq *priorityQueue[T]) pop() (T, float64) {
	n := len(pq.entries) - 1
	pq.swap(0, n)
	pq.down(0, n)
	e := pq.entries[n]
	pq.entries = pq.entries[:n]
	return e.item, e.priority
}

func (pq *priorityQueue[T]) len() int { return len(pq.entries) }

func (pq *priorityQueue[T]) less(i, j int) bool {
	return pq.lessFunc(pq.entries[i], pq.entries[j])
}

func (pq *priorityQueue[T]) swap(i, j int) {
	pq.entries[i], pq.entries[j] = pq.entries[j], pq.entries[i]
}

func (pq *priorityQueue[T]) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !pq.less(i, parent) {
			break
		}
		pq.swap(i, parent)
		i = parent
	}
}

func (pq *priorityQueue[T]) down(i, n int) {
	for {
		left := 2*i + 1
		if left >= n {
			break
		}
		j := left
		if right := left + 1; right < n && pq.less(right, left) {
			j = right
		}
		if !pq.less(j, i) {
			break
		}
		pq.swap(i, j)
		i = j
	}
}
