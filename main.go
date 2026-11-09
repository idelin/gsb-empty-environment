package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

type page struct {
	idRaw json.RawMessage
	idKey string
	in    string
	out   string
	term  bool
	minV  int64
	maxV  int64
}

func normToken(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(bytes.TrimSpace(raw))
}

func isEmptyCursor(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	if bytes.Equal(t, []byte("null")) {
		return true
	}
	var s string
	if err := json.Unmarshal(t, &s); err == nil {
		return s == ""
	}
	return false
}

func fatal(args ...any) {
	fmt.Fprintln(os.Stderr, args...)
	os.Exit(1)
}

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fatal(err)
	}
	var doc struct {
		Start json.RawMessage   `json:"start"`
		Pages []json.RawMessage `json:"pages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		fatal(err)
	}
	start := normToken(doc.Start)

	pages := make([]*page, 0, len(doc.Pages))
	for _, raw := range doc.Pages {
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil || len(arr) != 5 {
			fatal("bad page entry: ", string(raw))
		}
		p := &page{
			idRaw: json.RawMessage(bytes.TrimSpace(arr[0])),
			idKey: normToken(arr[0]),
			in:    normToken(arr[1]),
			term:  isEmptyCursor(arr[2]),
		}
		if !p.term {
			p.out = normToken(arr[2])
		}
		if err := json.Unmarshal(arr[3], &p.minV); err != nil {
			fatal("bad min version: ", string(raw))
		}
		if err := json.Unmarshal(arr[4], &p.maxV); err != nil {
			fatal("bad max version: ", string(raw))
		}
		pages = append(pages, p)
	}

	adj := map[string][]int{}
	hiSet := map[int64]bool{}
	for i, p := range pages {
		adj[p.in] = append(adj[p.in], i)
		hiSet[p.maxV] = true
	}
	his := make([]int64, 0, len(hiSet))
	for v := range hiSet {
		his = append(his, v)
	}
	sort.Slice(his, func(a, b int) bool { return his[a] > his[b] })

	// A chain's highest usable version is min(maxV) over its pages, so the
	// answer is always one of the distinct maxV values. Scan them descending;
	// for candidate m the usable edges are exactly those with minV <= m <= maxV.
	usable := make([]bool, len(pages))
	markUsable := func(m int64) {
		for i, p := range pages {
			usable[i] = p.minV <= m && m <= p.maxV
		}
	}
	reachable := func() bool {
		seen := map[string]bool{start: true}
		queue := []string{start}
		for len(queue) > 0 {
			u := queue[0]
			queue = queue[1:]
			for _, ei := range adj[u] {
				if !usable[ei] {
					continue
				}
				p := pages[ei]
				if p.term {
					return true
				}
				if !seen[p.out] {
					seen[p.out] = true
					queue = append(queue, p.out)
				}
			}
		}
		return false
	}

	best := int64(0)
	found := false
	for _, m := range his {
		markUsable(m)
		if reachable() {
			best = m
			found = true
			break
		}
	}
	if !found {
		fmt.Println("null")
		return
	}

	// Min hop count to the chain end over the usable subgraph (reverse BFS).
	dist := map[string]int64{}
	rev := map[string][]string{}
	queue := []string{}
	for i, p := range pages {
		if !usable[i] {
			continue
		}
		if p.term {
			if _, ok := dist[p.in]; !ok {
				dist[p.in] = 1
				queue = append(queue, p.in)
			}
		} else {
			rev[p.out] = append(rev[p.out], p.in)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, w := range rev[u] {
			if _, ok := dist[w]; !ok {
				dist[w] = dist[u] + 1
				queue = append(queue, w)
			}
		}
	}
	if _, ok := dist[start]; !ok {
		fmt.Println("null")
		return
	}

	// Greedy: at each step take the smallest page ID that stays on a shortest
	// path. Page IDs are unique, so this yields the lexicographically
	// smallest full ID sequence among minimum-length chains.
	var ids []json.RawMessage
	cur := start
	for {
		d := dist[cur]
		var chosen *page
		next := ""
		for _, ei := range adj[cur] {
			if !usable[ei] {
				continue
			}
			p := pages[ei]
			var nd int64
			if p.term {
				nd = 0
			} else {
				dv, ok := dist[p.out]
				if !ok {
					continue
				}
				nd = dv
			}
			if nd != d-1 {
				continue
			}
			if chosen == nil || p.idKey < chosen.idKey {
				chosen = p
				next = p.out
			}
		}
		if chosen == nil {
			fmt.Println("null")
			return
		}
		ids = append(ids, chosen.idRaw)
		if chosen.term {
			break
		}
		cur = next
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[%d,[", best)
	for i, id := range ids {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(id)
	}
	buf.WriteString("]]")
	fmt.Println(buf.String())
}
