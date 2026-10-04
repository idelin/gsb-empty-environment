// Package rollsam 实现可回滚的字节串后缀自动机（suffix automaton）。
//
// 公开约定：
//   - 位置均为 0 起始的字节下标。
//   - 空模式按“空串出现于一切位置”的约定处理：
//     对长度 n 的文本，空模式的出现位置为 0..n（共 n+1 个）。
//   - Distinct 统计不同非空子串总数（不含空串）。
//   - Undo 以“一次 Append 一个字符”为粒度回滚；空栈时 Undo 返回 false。
package rollsam

const absent = -1

type node struct {
	next map[byte]int // 转移
	link int          // 后缀链接
	len  int          // 该状态最长串长度
	pos  []int        // 直接登记在该（主）状态上的前缀结束位置
}

type changeKind uint8

const (
	chgSetTrans changeKind = iota // next[node][sym]: oldVal -> 新值（oldVal 可能为 absent）
	chgSetLink                    // link[node]: oldVal -> 新值
)

// change 描述对“本次操作之前已存在”的节点的一次修改，用于撤销。
type change struct {
	kind   changeKind
	node   int
	sym    byte
	oldVal int
}

// opRecord 是一次 Append 的完整回滚信息。
type opRecord struct {
	nodesBefore int      // 操作前的节点数（新节点总是追加在尾部，截断即可删除）
	lastBefore  int      // 操作前的 last
	delta       int64    // 本次操作带来的不同子串数增量
	changes     []change // 对旧节点的全部修改
}

// SAM 是可回滚后缀自动机。零值不可用，请用 New 构造。
type SAM struct {
	nodes    []node
	last     int
	n        int // 当前文本长度
	distinct int64
	history  []opRecord
}

// New 构造一个空文本的后缀自动机。
func New() *SAM {
	s := &SAM{}
	s.nodes = append(s.nodes, node{next: map[byte]int{}, link: -1, len: 0})
	s.last = 0
	return s
}

// Len 返回当前文本长度。
func (s *SAM) Len() int { return s.n }

// Distinct 返回当前文本的不同非空子串总数。
func (s *SAM) Distinct() int64 { return s.distinct }

// Append 在末尾追加一个字节。
func (s *SAM) Append(c byte) {
	rec := opRecord{nodesBefore: len(s.nodes), lastBefore: s.last}
	base := rec.nodesBefore

	setTrans := func(v int, sym byte, to int) {
		old, ok := s.nodes[v].next[sym]
		if !ok {
			old = absent
		}
		if v < base { // 只记录对旧节点的修改；新节点随截断消失
			rec.changes = append(rec.changes, change{kind: chgSetTrans, node: v, sym: sym, oldVal: old})
		}
		s.nodes[v].next[sym] = to
	}
	setLink := func(v, to int) {
		if v < base {
			rec.changes = append(rec.changes, change{kind: chgSetLink, node: v, oldVal: s.nodes[v].link})
		}
		s.nodes[v].link = to
	}
	newNode := func(length int) int {
		s.nodes = append(s.nodes, node{next: map[byte]int{}, link: 0, len: length})
		return len(s.nodes) - 1
	}

	cur := newNode(s.n + 1)
	s.nodes[cur].pos = append(s.nodes[cur].pos, s.n) // 新前缀的结束位置

	p := s.last
	for p != -1 {
		if _, ok := s.nodes[p].next[c]; ok {
			break
		}
		setTrans(p, c, cur)
		p = s.nodes[p].link
	}
	if p == -1 {
		setLink(cur, 0)
	} else {
		q := s.nodes[p].next[c]
		if s.nodes[p].len+1 == s.nodes[q].len {
			setLink(cur, q)
		} else {
			clone := newNode(s.nodes[p].len + 1)
			for k, v := range s.nodes[q].next {
				s.nodes[clone].next[k] = v
			}
			setLink(clone, s.nodes[q].link)
			for p != -1 {
				if t, ok := s.nodes[p].next[c]; !ok || t != q {
					break
				}
				setTrans(p, c, clone)
				p = s.nodes[p].link
			}
			setLink(q, clone)
			setLink(cur, clone)
		}
	}
	s.last = cur
	s.n++

	// 新增不同子串 = 新文本中此前未出现过的后缀，
	// 恰为 len[cur]-len[link[cur]]（此时 cur 的链接已最终确定）。
	// 若产生了克隆节点，其贡献与 q 的链接改向引起的贡献变化恰好抵消，无需计入。
	rec.delta = int64(s.nodes[cur].len - s.nodes[s.nodes[cur].link].len)
	s.distinct += rec.delta
	s.history = append(s.history, rec)
}

// Undo 撤销最近一次 Append。无可撤销操作时返回 false。
// 撤销后不留任何残留转移，不同子串计数精确回退。
func (s *SAM) Undo() bool {
	if len(s.history) == 0 {
		return false
	}
	rec := s.history[len(s.history)-1]
	s.history[len(s.history)-1] = opRecord{} // 释放引用，便于 GC
	s.history = s.history[:len(s.history)-1]

	for i := len(rec.changes) - 1; i >= 0; i-- {
		ch := rec.changes[i]
		switch ch.kind {
		case chgSetTrans:
			if ch.oldVal == absent {
				delete(s.nodes[ch.node].next, ch.sym)
			} else {
				s.nodes[ch.node].next[ch.sym] = ch.oldVal
			}
		case chgSetLink:
			s.nodes[ch.node].link = ch.oldVal
		}
	}
	// 新节点只可能出现在切片尾部；清零后截断，使其内存可被回收。
	for i := rec.nodesBefore; i < len(s.nodes); i++ {
		s.nodes[i] = node{}
	}
	s.nodes = s.nodes[:rec.nodesBefore]
	s.last = rec.lastBefore
	s.n--
	s.distinct -= rec.delta
	return true
}

// Positions 返回模式串的全部出现位置（起始下标，升序）。
// 空模式按约定返回 0..n；不出现的模式返回 nil。
func (s *SAM) Positions(pattern string) []int {
	if len(pattern) == 0 {
		out := make([]int, s.n+1)
		for i := range out {
			out[i] = i
		}
		return out
	}
	v := 0
	for i := 0; i < len(pattern); i++ {
		to, ok := s.nodes[v].next[pattern[i]]
		if !ok {
			return nil
		}
		v = to
	}
	m := len(pattern)
	// endpos(v) = 所有“链接树祖先链经过 v 的主节点”上登记位置的并集。
	mark := make([]bool, s.n)
	for u := 1; u < len(s.nodes); u++ {
		if len(s.nodes[u].pos) == 0 {
			continue
		}
		for x := u; x != -1; x = s.nodes[x].link {
			if s.nodes[x].len < m { // v.len >= m，更浅的祖先不可能等于 v
				break
			}
			if x == v {
				for _, p := range s.nodes[u].pos {
					mark[p] = true
				}
				break
			}
		}
	}
	var out []int
	for end, ok := range mark {
		if ok {
			out = append(out, end-m+1)
		}
	}
	return out
}
