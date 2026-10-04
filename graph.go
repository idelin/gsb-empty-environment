package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Edge struct {
	U, V int
	W    int64
}

type Graph struct {
	Names []string
	Index map[string]int
	Edges []Edge
	Adj   [][]int
	Radj  [][]int
}

func NewGraph() *Graph {
	return &Graph{Index: map[string]int{}}
}

func (g *Graph) Node(name string) int {
	if i, ok := g.Index[name]; ok {
		return i
	}
	i := len(g.Names)
	g.Names = append(g.Names, name)
	g.Index[name] = i
	g.Adj = append(g.Adj, nil)
	g.Radj = append(g.Radj, nil)
	return i
}

func (g *Graph) AddEdge(u, v string, w int64) {
	ui, vi := g.Node(u), g.Node(v)
	ei := len(g.Edges)
	g.Edges = append(g.Edges, Edge{U: ui, V: vi, W: w})
	g.Adj[ui] = append(g.Adj[ui], ei)
	g.Radj[vi] = append(g.Radj[vi], ei)
}

func ParseGraph(r io.Reader) (*Graph, error) {
	g := NewGraph()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		f := strings.Fields(text)
		if len(f) != 3 {
			return nil, fmt.Errorf("line %d: want \"u v w\", got %q", line, text)
		}
		w, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: bad weight %q: %v", line, f[2], err)
		}
		g.AddEdge(f[0], f[1], w)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return g, nil
}

func (g *Graph) N() int { return len(g.Names) }

func (g *Graph) ReachableFrom(src int) []bool {
	seen := make([]bool, g.N())
	stack := []int{src}
	seen[src] = true
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, ei := range g.Adj[u] {
			v := g.Edges[ei].V
			if !seen[v] {
				seen[v] = true
				stack = append(stack, v)
			}
		}
	}
	return seen
}

func (g *Graph) ReachesTo(dst int) []bool {
	seen := make([]bool, g.N())
	stack := []int{dst}
	seen[dst] = true
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, ei := range g.Radj[u] {
			v := g.Edges[ei].U
			if !seen[v] {
				seen[v] = true
				stack = append(stack, v)
			}
		}
	}
	return seen
}

func (g *Graph) BFSPath(src, dst int) []int {
	prev := make([]int, g.N())
	for i := range prev {
		prev[i] = -1
	}
	prev[src] = src
	queue := []int{src}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if u == dst {
			break
		}
		for _, ei := range g.Adj[u] {
			v := g.Edges[ei].V
			if prev[v] == -1 {
				prev[v] = u
				queue = append(queue, v)
			}
		}
	}
	if prev[dst] == -1 {
		return nil
	}
	var path []int
	for cur := dst; ; cur = prev[cur] {
		path = append(path, cur)
		if cur == src {
			break
		}
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

func (g *Graph) MinEdgeWeight(u, v int) (int64, bool) {
	var best int64
	found := false
	for _, ei := range g.Adj[u] {
		e := g.Edges[ei]
		if e.V == v && (!found || e.W < best) {
			best = e.W
			found = true
		}
	}
	return best, found
}
