package main

import "strings"

// otherSymbol 是字母表中代表"任意未在规则里出现的片段名"的内部符号。
const otherSymbol = "\x00other\x00"

// nfa 是路径模式编译出的非确定有限自动机，唯一接受态为 accept。
type nfa struct {
	n       int
	delta   []map[string][]int
	epsilon [][]int
	accept  int
}

func buildNFA(segs []segment, alphabet []string) *nfa {
	n := len(segs) + 1
	m := &nfa{
		n:       n,
		delta:   make([]map[string][]int, n),
		epsilon: make([][]int, n),
		accept:  n - 1,
	}
	for i := range m.delta {
		m.delta[i] = map[string][]int{}
	}
	for i, s := range segs {
		switch s.kind {
		case segLiteral:
			m.delta[i][s.lit] = append(m.delta[i][s.lit], i+1)
		case segSingle:
			for _, sym := range alphabet {
				m.delta[i][sym] = append(m.delta[i][sym], i+1)
			}
		case segMulti:
			for _, sym := range alphabet {
				m.delta[i][sym] = append(m.delta[i][sym], i)
			}
			m.epsilon[i] = append(m.epsilon[i], i+1)
		}
	}
	return m
}

func singletonMask(size, idx int) []bool {
	mask := make([]bool, size)
	mask[idx] = true
	return mask
}

func closure(m *nfa, states []bool) []bool {
	res := make([]bool, len(states))
	var stack []int
	for i, v := range states {
		if v {
			res[i] = true
			stack = append(stack, i)
		}
	}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, t := range m.epsilon[s] {
			if !res[t] {
				res[t] = true
				stack = append(stack, t)
			}
		}
	}
	return res
}

func move(m *nfa, states []bool, sym string) []bool {
	next := make([]bool, m.n)
	for s, v := range states {
		if !v {
			continue
		}
		for _, t := range m.delta[s][sym] {
			next[t] = true
		}
	}
	return closure(m, next)
}

func anySet(mask []bool) bool {
	for _, v := range mask {
		if v {
			return true
		}
	}
	return false
}

func maskKey(mask []bool) string {
	b := make([]byte, len(mask))
	for i, v := range mask {
		if v {
			b[i] = 1
		}
	}
	return string(b)
}

// productState 是目标自动机的一个状态集与若干遮蔽自动机各自状态集的乘积。
type productState struct {
	target []bool
	covers [][]bool
}

func productKey(st productState) string {
	var sb strings.Builder
	sb.WriteString(maskKey(st.target))
	for _, c := range st.covers {
		sb.WriteByte(2)
		sb.WriteString(maskKey(c))
	}
	return sb.String()
}

// bfsWitness 在乘积图上做 BFS，返回满足 accept 条件的最短路径（片段序列）。
// accept 为 nil 时不可达返回 nil。
func bfsWitness(target *nfa, covers []*nfa, alphabet []string, accept func(productState) bool) []string {
	start := productState{target: closure(target, singletonMask(target.n, 0))}
	for _, c := range covers {
		start.covers = append(start.covers, closure(c, singletonMask(c.n, 0)))
	}
	if accept(start) {
		return []string{}
	}
	type item struct {
		state productState
		word  []string
	}
	visited := map[string]bool{productKey(start): true}
	queue := []item{{state: start}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, sym := range alphabet {
			nt := move(target, cur.state.target, sym)
			if !anySet(nt) {
				continue
			}
			next := productState{target: nt}
			for i, c := range covers {
				next.covers = append(next.covers, move(c, cur.state.covers[i], sym))
			}
			k := productKey(next)
			if visited[k] {
				continue
			}
			visited[k] = true
			word := make([]string, len(cur.word)+1)
			copy(word, cur.word)
			word[len(cur.word)] = sym
			if accept(next) {
				return word
			}
			queue = append(queue, item{state: next, word: word})
		}
	}
	return nil
}

// differenceWitness 返回一条被 target 接受、但不被任何 cover 接受的路径；
// 不存在（即 target 语言被 cover 并集覆盖）时返回 nil。
func differenceWitness(target *nfa, covers []*nfa, alphabet []string) []string {
	return bfsWitness(target, covers, alphabet, func(st productState) bool {
		if !st.target[target.accept] {
			return false
		}
		for i, c := range covers {
			if st.covers[i][c.accept] {
				return false
			}
		}
		return true
	})
}

// intersectsAny 报告 target 的语言是否与任一 other 的语言相交。
func intersectsAny(target *nfa, others []*nfa, alphabet []string) bool {
	return bfsWitness(target, others, alphabet, func(st productState) bool {
		if !st.target[target.accept] {
			return false
		}
		for i, c := range others {
			if st.covers[i][c.accept] {
				return true
			}
		}
		return false
	}) != nil
}
