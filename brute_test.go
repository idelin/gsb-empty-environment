package fuzzy

import (
	"container/heap"
	"math/rand"
	"sort"
)

// This file recomputes distances by exhaustively traversing the edit
// graph, independently of the dynamic programming used by the library.
// The graph nodes are the alignment positions (i, j) between query and
// word; edges are the four edit operations. Shortest paths are found with
// Dijkstra's algorithm, which copes with zero-cost edges and with many
// paths reaching the same node.

type graphNode struct {
	i, j int
}

type graphItem struct {
	node graphNode
	dist int
}

type graphPQ []graphItem

func (pq graphPQ) Len() int            { return len(pq) }
func (pq graphPQ) Less(i, j int) bool  { return pq[i].dist < pq[j].dist }
func (pq graphPQ) Swap(i, j int)       { pq[i], pq[j] = pq[j], pq[i] }
func (pq *graphPQ) Push(x interface{}) { *pq = append(*pq, x.(graphItem)) }

func (pq *graphPQ) Pop() interface{} {
	old := *pq
	n := len(old)
	it := old[n-1]
	*pq = old[:n-1]
	return it
}

// bruteDistance exhaustively searches the edit graph for the minimum cost
// of turning a into b.
func bruteDistance(a, b string, c Costs) int {
	ar, br := []rune(a), []rune(b)
	n, m := len(ar), len(br)
	target := graphNode{n, m}
	dist := map[graphNode]int{graphNode{0, 0}: 0}
	pq := graphPQ{graphItem{node: graphNode{0, 0}}}
	for len(pq) > 0 {
		cur := heap.Pop(&pq).(graphItem)
		if cur.dist > dist[cur.node] {
			continue // stale queue entry
		}
		if cur.node == target {
			return cur.dist
		}
		i, j := cur.node.i, cur.node.j
		relax := func(to graphNode, w int) {
			nd := cur.dist + w
			if od, ok := dist[to]; !ok || nd < od {
				dist[to] = nd
				heap.Push(&pq, graphItem{node: to, dist: nd})
			}
		}
		if i < n {
			relax(graphNode{i + 1, j}, c.Del)
		}
		if j < m {
			relax(graphNode{i, j + 1}, c.Ins)
		}
		if i < n && j < m {
			w := c.Rep
			if ar[i] == br[j] {
				w = 0
			}
			relax(graphNode{i + 1, j + 1}, w)
		}
		if i+1 < n && j+1 < m && ar[i] == br[j+1] && ar[i+1] == br[j] {
			relax(graphNode{i + 2, j + 2}, c.Trans)
		}
	}
	return dist[target]
}

// bruteTopK ranks every word by brute-force distance and returns the best
// k, ordered by cost and then lexicographically.
func bruteTopK(words []string, query string, k int, c Costs) []Hit {
	hits := make([]Hit, 0, len(words))
	for _, w := range words {
		hits = append(hits, Hit{Word: w, Cost: bruteDistance(query, w, c)})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Cost != hits[j].Cost {
			return hits[i].Cost < hits[j].Cost
		}
		return hits[i].Word < hits[j].Word
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits
}

func randWord(rng *rand.Rand, alphabet []rune, maxLen int) string {
	n := rng.Intn(maxLen + 1)
	w := make([]rune, n)
	for i := range w {
		w[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(w)
}

func randCosts(rng *rand.Rand, max int) Costs {
	return Costs{
		Ins:   rng.Intn(max + 1),
		Del:   rng.Intn(max + 1),
		Rep:   rng.Intn(max + 1),
		Trans: rng.Intn(max + 1),
	}
}
