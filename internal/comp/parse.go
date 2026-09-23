// Package comp builds fragment dictionaries from training samples and
// parses samples into token streams under the wire cost model.
package comp

import (
	"sort"

	"dictcomp/internal/wire"
)

// Cost model shared by every solver in this package. It mirrors the wire
// format exactly, so reported gains are real file-size gains.
const (
	refTokenCost  = 2              // 0xFF control byte + 1-byte dictionary index
	litRunMax     = wire.LitRunMax // 254
	entryLenField = 2              // u16 length prefix per dictionary entry
)

// entryCost is the number of bytes one dictionary entry occupies in a file.
func entryCost(entryLen int) int64 { return int64(entryLenField + entryLen) }

// DictStoreCost is the total dictionary storage cost in bytes.
func DictStoreCost(dict [][]byte) int64 {
	var n int64
	for _, e := range dict {
		n += entryCost(len(e))
	}
	return n
}

// OptimalParse computes the exact minimum-cost tokenization of s under the
// given dictionary, using the wire cost model. The chosen matches form a
// compatible (non-overlapping) set by construction, so overlapping
// occurrences can never be counted twice.
func OptimalParse(s []byte, dict [][]byte) ([]wire.Token, int64) {
	n := len(s)
	if n == 0 {
		return nil, 0
	}
	var at map[int][]int
	if len(dict) > 0 {
		plens := make([]int, len(dict))
		for i, e := range dict {
			plens[i] = len(e)
		}
		at = buildAutomaton(dict).matchesByStart(s, plens)
	}
	const inf = int64(1) << 62
	dp := make([]int64, n+1)
	choice := make([]int32, n+1) // >0: literal run length; <=0: -1-entryIndex
	for i := n - 1; i >= 0; i-- {
		best := inf
		bestChoice := int32(1)
		maxRun := n - i
		if maxRun > litRunMax {
			maxRun = litRunMax
		}
		for l := 1; l <= maxRun; l++ {
			if c := int64(1+l) + dp[i+l]; c < best {
				best = c
				bestChoice = int32(l)
			}
		}
		for _, id := range at[i] {
			if c := int64(refTokenCost) + dp[i+len(dict[id])]; c < best {
				best = c
				bestChoice = -1 - int32(id)
			}
		}
		dp[i] = best
		choice[i] = bestChoice
	}
	var tokens []wire.Token
	for i := 0; i < n; {
		c := choice[i]
		if c > 0 {
			tokens = append(tokens, wire.Token{Ref: -1, Lit: s[i : i+int(c)]})
			i += int(c)
		} else {
			id := int(-1 - c)
			tokens = append(tokens, wire.Token{Ref: id})
			i += len(dict[id])
		}
	}
	return tokens, dp[0]
}

// automaton is an Aho-Corasick automaton over the dictionary entries.
type automaton struct {
	next []map[byte]int
	fail []int
	out  [][]int // pattern indices whose match ends at this state
}

func buildAutomaton(patterns [][]byte) *automaton {
	a := &automaton{
		next: []map[byte]int{{}},
		fail: []int{0},
		out:  [][]int{nil},
	}
	for id, p := range patterns {
		state := 0
		for _, b := range p {
			ns, ok := a.next[state][b]
			if !ok {
				ns = len(a.next)
				a.next = append(a.next, map[byte]int{})
				a.fail = append(a.fail, 0)
				a.out = append(a.out, nil)
				a.next[state][b] = ns
			}
			state = ns
		}
		a.out[state] = append(a.out[state], id)
	}
	// BFS to compute failure links and merge outputs. Transition bytes are
	// visited in sorted order so every downstream structure is deterministic.
	queue := []int{}
	for _, b := range sortedKeys(a.next[0]) {
		queue = append(queue, a.next[0][b])
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, b := range sortedKeys(a.next[v]) {
			u := a.next[v][b]
			queue = append(queue, u)
			f := a.fail[v]
			for {
				if s, ok := a.next[f][b]; ok && s != u {
					a.fail[u] = s
					break
				}
				if f == 0 {
					a.fail[u] = 0
					break
				}
				f = a.fail[f]
			}
			a.out[u] = append(a.out[u], a.out[a.fail[u]]...)
		}
	}
	return a
}

// matchesByStart returns, for each position of s, the indices of patterns
// that match starting there.
func (a *automaton) matchesByStart(s []byte, plens []int) map[int][]int {
	m := map[int][]int{}
	state := 0
	for i := 0; i < len(s); i++ {
		b := s[i]
		for {
			if ns, ok := a.next[state][b]; ok {
				state = ns
				break
			}
			if state == 0 {
				break
			}
			state = a.fail[state]
		}
		for _, id := range a.out[state] {
			start := i - plens[id] + 1
			m[start] = append(m[start], id)
		}
	}
	return m
}

func sortedKeys(m map[byte]int) []byte {
	keys := make([]byte, 0, len(m))
	for b := range m {
		keys = append(keys, b)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
