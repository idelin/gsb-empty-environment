package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type page struct {
	id  string
	in  int // cursor node index
	out int // cursor node index; endNode if terminal
	lo  int64
	hi  int64
}

// asString decodes a JSON value expected to be a string; falls back to raw text.
func asString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

func asInt(raw json.RawMessage) (int64, error) {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, err
	}
	if v, err := n.Int64(); err == nil {
		return v, nil
	}
	f, err := n.Float64()
	return int64(f), err
}

func main() {
	var input struct {
		Start json.RawMessage    `json:"start"`
		Pages [][]json.RawMessage `json:"pages"`
	}
	dec := json.NewDecoder(bufio.NewReader(os.Stdin))
	if err := dec.Decode(&input); err != nil {
		fmt.Fprintln(os.Stderr, "bad input:", err)
		os.Exit(1)
	}
	start := asString(input.Start)

	// Cursor node indexing; END is a dedicated sink node.
	nodeOf := map[string]int{}
	var nodes []string
	getNode := func(c string) int {
		if i, ok := nodeOf[c]; ok {
			return i
		}
		i := len(nodes)
		nodeOf[c] = i
		nodes = append(nodes, c)
		return i
	}

	var pages []page
	rawOut := make([]string, 0, len(input.Pages))
	for _, raw := range input.Pages {
		if len(raw) != 5 {
			fmt.Fprintln(os.Stderr, "bad page entry")
			os.Exit(1)
		}
		lo, err1 := asInt(raw[3])
		hi, err2 := asInt(raw[4])
		if err1 != nil || err2 != nil {
			fmt.Fprintln(os.Stderr, "bad version")
			os.Exit(1)
		}
		out := asString(raw[2])
		rawOut = append(rawOut, out)
		outNode := 0
		if out != "" {
			outNode = getNode(out)
		}
		pages = append(pages, page{
			id:  asString(raw[0]),
			in:  getNode(asString(raw[1])),
			out: outNode,
			lo:  lo,
			hi:  hi,
		})
	}
	// END is a dedicated sink node, indexed after all named cursors.
	endNode := len(nodes)
	nodes = append(nodes, "") // END sentinel
	for i := range pages {
		if rawOut[i] == "" {
			pages[i].out = endNode
		}
	}

	// Candidate versions: a chain's best common version is always some page's hi.
	verSet := map[int64]struct{}{}
	for _, p := range pages {
		verSet[p.hi] = struct{}{}
	}
	vers := make([]int64, 0, len(verSet))
	for v := range verSet {
		vers = append(vers, v)
	}
	sort.Slice(vers, func(a, b int) bool { return vers[a] > vers[b] })

	n := len(nodes)
	byIn := make([][]int, n)  // node -> page indices with in == node
	byOut := make([][]int, n) // node -> page indices with out == node
	for i, p := range pages {
		byIn[p.in] = append(byIn[p.in], i)
		byOut[p.out] = append(byOut[p.out], i)
	}
	startNode, startOK := nodeOf[start]

	// reachable reports whether END is reachable from start using only
	// pages whose version window contains v.
	reachable := func(v int64) bool {
		if !startOK {
			return false
		}
		visited := make([]bool, n)
		queue := []int{startNode}
		visited[startNode] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, pi := range byIn[cur] {
				p := pages[pi]
				if p.lo > v || p.hi < v {
					continue
				}
				if p.out == endNode {
					return true
				}
				if !visited[p.out] {
					visited[p.out] = true
					queue = append(queue, p.out)
				}
			}
		}
		return false
	}

	best := int64(0)
	found := false
	for _, v := range vers {
		if reachable(v) {
			best = v
			found = true
			break
		}
	}
	if !found {
		fmt.Println("null")
		return
	}

	// Reverse BFS from END: min pages to finish from each cursor node.
	const inf = int(^uint(0) >> 1)
	dist := make([]int, n)
	for i := range dist {
		dist[i] = inf
	}
	dist[endNode] = 0
	queue := []int{endNode}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, pi := range byOut[cur] {
			p := pages[pi]
			if p.lo > best || p.hi < best {
				continue
			}
			if dist[p.in] == inf {
				dist[p.in] = dist[cur] + 1
				queue = append(queue, p.in)
			}
		}
	}
	if dist[startNode] == inf {
		fmt.Println("null")
		return
	}

	// Greedy lexicographically smallest among shortest chains.
	ids := []string{}
	cur := startNode
	for remaining := dist[startNode]; remaining > 0; remaining-- {
		chosen := -1
		for _, pi := range byIn[cur] {
			p := pages[pi]
			if p.lo > best || p.hi < best {
				continue
			}
			if dist[p.out] != remaining-1 {
				continue
			}
			if chosen == -1 || p.id < pages[chosen].id {
				chosen = pi
			}
		}
		if chosen == -1 {
			fmt.Println("null") // defensive; unreachable given feasibility
			return
		}
		ids = append(ids, pages[chosen].id)
		cur = pages[chosen].out
	}

	out, _ := json.Marshal([]interface{}{best, ids})
	fmt.Println(string(out))
}
