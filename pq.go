package main

import "container/heap"

// pqItem is a priority-queue entry ordered lexicographically by
// (steps, cost): fewest rollback steps first, then lowest budget cost.
type pqItem struct {
	key   string
	steps int
	cost  int
}

type priorityQueue []pqItem

func (q priorityQueue) Len() int { return len(q) }

func (q priorityQueue) Less(i, j int) bool {
	if q[i].steps != q[j].steps {
		return q[i].steps < q[j].steps
	}
	return q[i].cost < q[j].cost
}

func (q priorityQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }

func (q *priorityQueue) Push(x interface{}) {
	*q = append(*q, x.(pqItem))
}

func (q *priorityQueue) Pop() interface{} {
	old := *q
	n := len(old)
	it := old[n-1]
	*q = old[:n-1]
	return it
}

func pushPQ(q *priorityQueue, item pqItem) {
	heap.Push(q, item)
}

func popPQ(q *priorityQueue) pqItem {
	return heap.Pop(q).(pqItem)
}
