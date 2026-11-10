package main

import (
	"bytes"
	"container/heap"
)

// productState pairs the states of the escape-off and escape-on lexers after
// consuming the same prefix.
type productState struct {
	off lexState
	on  lexState
}

// stateKey distinguishes product states per remaining-budget level so the
// visited set never conflates two different concatenation depths.
type stateKey struct {
	state productState
	used  int
}

// boundaryNode is a search frontier resting on a fragment boundary.
type boundaryNode struct {
	state productState
	used  int
	word  []byte
	path  [][]byte
}

// candidate is a completed byte string whose first differential semicolon sits
// at divergenceIndex.
type candidate struct {
	word            []byte
	path            [][]byte
	divergenceIndex int
}

type pqItem struct {
	isSolution bool
	boundary   *boundaryNode
	solution   *candidate
}

func (it *pqItem) keyBytes() []byte {
	if it.isSolution {
		return it.solution.word
	}
	return it.boundary.word
}

type priorityQueue []*pqItem

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	a, b := pq[i].keyBytes(), pq[j].keyBytes()
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return bytes.Compare(a, b) < 0
}

func (pq priorityQueue) Swap(i, j int) { pq[i], pq[j] = pq[j], pq[i] }

func (pq *priorityQueue) Push(x any) { *pq = append(*pq, x.(*pqItem)) }

func (pq *priorityQueue) Pop() any {
	old := *pq
	last := old[len(old)-1]
	*pq = old[:len(old)-1]
	return last
}

// searchResult is the output of shortestDivergence.
type searchResult struct {
	best    *candidate
	visited int
}

// shortestDivergence searches concatenations of at most maxConcat fragments for
// the shortest byte string (lexicographically smallest on ties) containing a
// semicolon that one lexer reads inside a string and the other reads outside.
// It never enumerates all combinations: the product automaton has only 16
// states and frontier states are settled once, shortest-first.
func shortestDivergence(dictionary [][]byte, maxConcat int) searchResult {
	queue := &priorityQueue{}
	heap.Init(queue)
	heap.Push(queue, &pqItem{
		boundary: &boundaryNode{state: productState{off: stOutside, on: stOutside}},
	})

	settled := map[stateKey]bool{}
	bestWord := map[stateKey][]byte{}
	visited := 0

	for queue.Len() > 0 {
		item := heap.Pop(queue).(*pqItem)
		if item.isSolution {
			return searchResult{best: item.solution, visited: visited}
		}

		node := item.boundary
		key := stateKey{state: node.state, used: node.used}
		if settled[key] {
			continue
		}
		settled[key] = true
		visited++
		if node.used >= maxConcat {
			continue
		}

		for _, fragment := range dictionary {
			offState, onState := node.state.off, node.state.on
			hit := -1
			for i, b := range fragment {
				if b == ';' && isInside(offState, b) != isInside(onState, b) {
					hit = i
					break
				}
				offState = nextState(escOff, offState, b)
				onState = nextState(escOn, onState, b)
			}

			path := make([][]byte, 0, len(node.path)+1)
			path = append(path, node.path...)
			path = append(path, append([]byte(nil), fragment...))

			if hit >= 0 {
				word := make([]byte, 0, len(node.word)+hit+1)
				word = append(word, node.word...)
				word = append(word, fragment[:hit+1]...)
				heap.Push(queue, &pqItem{
					isSolution: true,
					solution: &candidate{
						word:            word,
						path:            path,
						divergenceIndex: len(node.word) + hit,
					},
				})
				continue
			}

			word := make([]byte, 0, len(node.word)+len(fragment))
			word = append(word, node.word...)
			word = append(word, fragment...)
			nextNode := &boundaryNode{
				state: productState{off: offState, on: onState},
				used:  node.used + 1,
				word:  word,
				path:  path,
			}
			nextKey := stateKey{state: nextNode.state, used: nextNode.used}
			if settled[nextKey] {
				continue
			}
			if previous, ok := bestWord[nextKey]; ok && bytes.Compare(previous, word) <= 0 {
				continue
			}
			bestWord[nextKey] = append([]byte(nil), word...)
			heap.Push(queue, &pqItem{boundary: nextNode})
		}
	}

	return searchResult{visited: visited}
}
