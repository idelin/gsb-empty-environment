package bracketdiag

import "sort"

// inverseOp 是一次编辑的逆操作，用于撤销。
type inverseOp struct {
	pos       int
	insert    string // 撤销时需要插回的文本
	deleteLen int    // 撤销时需要删除的长度
}

// Buffer 是支持插入、删除、撤销的文本缓冲，维护按行的增量括号摘要。
type Buffer struct {
	text      []byte
	lineStart []int // 每行起始字节偏移，len >= 1，lineStart[0] == 0
	sums      []lineSummary
	undo      []inverseOp

	// Recomputed 统计最近一次编辑重算摘要的行数（用于验证增量性）。
	Recomputed int
}

// NewBuffer 以 initial 为初始内容创建缓冲。
func NewBuffer(initial string) *Buffer {
	b := &Buffer{text: []byte(initial)}
	b.lineStart = []int{0}
	for i, c := range b.text {
		if c == '\n' && i+1 < len(b.text) {
			b.lineStart = append(b.lineStart, i+1)
		}
	}
	b.sums = make([]lineSummary, len(b.lineStart))
	for i := range b.lineStart {
		b.sums[i] = computeLineSummary(b.lineText(i))
	}
	return b
}

// Text 返回当前全文。
func (b *Buffer) Text() string { return string(b.text) }

// LineCount 返回行数。
func (b *Buffer) LineCount() int { return len(b.lineStart) }

// Insert 在 pos 处插入文本。
func (b *Buffer) Insert(pos int, s string) {
	if s == "" {
		return
	}
	b.applyEdit(pos, 0, s, true)
}

// Delete 删除 [pos, pos+n) 的文本。
func (b *Buffer) Delete(pos, n int) {
	if n <= 0 {
		return
	}
	b.applyEdit(pos, n, "", true)
}

// Undo 撤销最近一次插入/删除，无可撤销操作时返回 false。
func (b *Buffer) Undo() bool {
	if len(b.undo) == 0 {
		return false
	}
	op := b.undo[len(b.undo)-1]
	b.undo = b.undo[:len(b.undo)-1]
	b.applyEdit(op.pos, op.deleteLen, op.insert, false)
	return true
}

// lineAt 返回包含字节偏移 pos 的行号（pos == len(text) 时为最后一行）。
func (b *Buffer) lineAt(pos int) int {
	return sort.Search(len(b.lineStart), func(i int) bool {
		return b.lineStart[i] > pos
	}) - 1
}

// lineText 返回第 i 行的文本（含行尾换行，最后一行可能没有）。
func (b *Buffer) lineText(i int) string {
	end := len(b.text)
	if i+1 < len(b.lineStart) {
		end = b.lineStart[i+1]
	}
	return string(b.text[b.lineStart[i]:end])
}

// applyEdit 执行 [pos, pos+deleteLen) 替换为 ins 的编辑，
// 只重算内容受影响的行的摘要；后续行内容不变、摘要位置为相对偏移，均保持有效。
func (b *Buffer) applyEdit(pos, deleteLen int, ins string, record bool) {
	if pos < 0 || pos > len(b.text) || deleteLen < 0 || pos+deleteLen > len(b.text) {
		panic("bracketdiag: edit out of range")
	}
	if record {
		b.undo = append(b.undo, inverseOp{
			pos:       pos,
			insert:    string(b.text[pos : pos+deleteLen]),
			deleteLen: len(ins),
		})
	}

	// 旧行首 q 在编辑后存活当且仅当 q <= pos（其换行符在编辑区之前）
	// 或 q > end（其换行符未被删除）。注意删除区间终点恰好落在某行首时，
	// 产生该行首的换行符已被删去，该行首不能保留。
	end := pos + deleteLen
	l1 := b.lineAt(pos)
	tailIdx := b.lineAt(end) + 1
	regionStart := b.lineStart[l1]
	regionEnd := len(b.text)
	if tailIdx < len(b.lineStart) {
		regionEnd = b.lineStart[tailIdx]
	}

	// 修改文本
	newText := make([]byte, 0, len(b.text)-deleteLen+len(ins))
	newText = append(newText, b.text[:pos]...)
	newText = append(newText, ins...)
	newText = append(newText, b.text[pos+deleteLen:]...)
	b.text = newText

	delta := len(ins) - deleteLen
	newRegionEnd := regionEnd + delta

	// 在新区间内重新扫描行首；区间右端恰为下一旧行的新行首（由尾部平移提供）
	newStarts := []int{regionStart}
	// 整个区域被删空且位于文末时，区域起点不再是合法行首（文末不存在空行）
	if regionStart == len(b.text) && regionStart > 0 {
		newStarts = newStarts[:0]
	}
	for p := regionStart; p < newRegionEnd; p++ {
		if b.text[p] == '\n' && p+1 < newRegionEnd {
			newStarts = append(newStarts, p+1)
		}
	}

	tailStarts := b.lineStart[tailIdx:]
	merged := make([]int, 0, l1+len(newStarts)+len(tailStarts))
	merged = append(merged, b.lineStart[:l1]...)
	merged = append(merged, newStarts...)
	for _, s := range tailStarts {
		merged = append(merged, s+delta)
	}
	b.lineStart = merged

	// 只重算新区间覆盖的行
	tailSums := b.sums[tailIdx:]
	newSums := make([]lineSummary, 0, len(b.lineStart))
	newSums = append(newSums, b.sums[:l1]...)
	for i := 0; i < len(newStarts); i++ {
		newSums = append(newSums, computeLineSummary(b.lineText(l1+i)))
	}
	newSums = append(newSums, tailSums...)
	b.sums = newSums
	b.Recomputed = len(newStarts)
}

// Errors 返回字节区间 [start, end) 内的全部括号错误（按位置升序）。
// 错误是全文扫描的属性（未闭合开括号/未终结字符串以构造起点计），
// 本函数按位置过滤出落在区间内的部分。
func (b *Buffer) Errors(start, end int) []DiagError {
	if start < 0 {
		start = 0
	}
	if end > len(b.text) {
		end = len(b.text)
	}

	// 沿行折叠可合并摘要，同时追踪词法状态与未终结构造的起点。
	var acc monoid
	state := SNormal
	constructStart := -1
	for i := range b.lineStart {
		s := &b.sums[i][state]
		acc = mergeMonoid(acc, offsetMonoid(s.m, b.lineStart[i]))
		state = s.exit
		if s.openPos >= 0 {
			constructStart = b.lineStart[i] + s.openPos
		} else if state == SNormal {
			constructStart = -1
		}
	}

	var all []DiagError
	all = append(all, acc.errs...) // 闭括号与栈顶不匹配
	for _, c := range acc.close {  // 多余的闭括号
		all = append(all, DiagError{Pos: c.pos, Found: string(c.char)})
	}
	for _, o := range acc.open { // 未闭合的开括号
		all = append(all, DiagError{Pos: o.pos, Expected: string(closeOf(o.char))})
	}
	switch state { // 未终结的字符串/块注释
	case SDouble, SEscDouble:
		all = append(all, DiagError{Pos: constructStart, Expected: `"`})
	case SSingle, SEscSingle:
		all = append(all, DiagError{Pos: constructStart, Expected: `'`})
	case SBacktick, SEscBacktick:
		all = append(all, DiagError{Pos: constructStart, Expected: "`"})
	case SBlockComment:
		all = append(all, DiagError{Pos: constructStart, Expected: "*/"})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Pos < all[j].Pos })

	out := all[:0]
	for _, e := range all {
		if e.Pos >= start && e.Pos < end {
			out = append(out, e)
		}
	}
	return out
}

// FirstError 返回字节区间 [start, end) 内的首个括号错误。
func (b *Buffer) FirstError(start, end int) (DiagError, bool) {
	errs := b.Errors(start, end)
	if len(errs) == 0 {
		return DiagError{}, false
	}
	return errs[0], true
}
