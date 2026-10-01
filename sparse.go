// Package sparse 实现"大部分为缺省值"的稀疏数组。
//
// 值语义（位置身份 = 下标，永不改变）：
//
//		value(p) = 例外绝对值            若 p 是例外（被显式赋值固定）
//		         = def + segDelta(p)     否则（缺省质量 + 惰性层增量）
//
//	 - Assign 点更新：写入绝对值例外，即使等于当前缺省也固定身份（显式 0 不是缺失）。
//	 - RangeAdd 范围加法：只切分惰性层 seg（O(相交段数)），并把范围内稀疏例外逐项 +d；
//	   不遍历巨大缺省区。赋值过的位置同样被后续范围加法推动（惰性层叠乘语义）。
//	 - ChangeDefault 缺省变更：基准平移，未固定位置整体跟随（def 变、seg 增量保留）；
//	   例外保持绝对值不跟随；值等于"回收后读取值"（新缺省 + 该位置惰性层增量）的例外
//	   被回收——回收不改变任何读取结果，回收后该位置重新跟随缺省。
//	 - Snapshot 快照：run/seg 不可变共享 + 写时复制，旧快照可继续查询。
//	 - Locate 按累计值定位：仅对非负快照开放，均匀缺省段算术跳跃，不逐元素扫描。
package sparse

// seg 是惰性层的一段：[lo,hi) 上叠于缺省值之上的增量 d。
// segs 保持排序、不相交、相邻同 d 合并、d==0 不存储的规范形。
type seg struct {
	lo, hi uint64
	d      int64
}

// view 是只读视图，Array 与 Snapshot 共用查询逻辑。
type view struct {
	def    int64
	segs   []seg
	run    *runT    // 不可变压缩例外索引（单 run，合并时整体重建）
	bufPos []uint64 // 写缓冲：排序平行切片，覆盖 run 中的旧值
	bufVal []int64
}

// Array 是可变头。所有修改只作用于头，已取快照不受影响。
type Array struct {
	n uint64
	v view

	nnValid bool // 非负性缓存
	nnVal   bool
}

// Snapshot 是不可变快照，与 Array 共享底层不可变数据。
type Snapshot struct {
	n      uint64
	v      view
	nonNeg bool
}

// New 创建逻辑长度 n、缺省值 def 的数组。
func New(n uint64, def int64) *Array {
	a := &Array{n: n}
	a.v.def = def
	return a
}

func (a *Array) Len() uint64       { return a.n }
func (a *Array) Default() int64    { return a.v.def }
func (s *Snapshot) Len() uint64    { return s.n }
func (s *Snapshot) Default() int64 { return s.v.def }

// ---------- 查询 ----------

func (v *view) segDelta(p uint64) int64 {
	i := searchSeg(v.segs, p)
	if i < len(v.segs) && v.segs[i].lo <= p && p < v.segs[i].hi {
		return v.segs[i].d
	}
	return 0
}

// searchSeg 返回第一个 hi > p 的段下标。
func searchSeg(segs []seg, p uint64) int {
	lo, hi := 0, len(segs)
	for lo < hi {
		mid := (lo + hi) / 2
		if segs[mid].hi <= p {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (v *view) bufHas(p uint64) bool {
	i := searchU64(v.bufPos, p)
	return i < len(v.bufPos) && v.bufPos[i] == p
}

func (v *view) excGet(p uint64) (int64, bool) {
	if i := searchU64(v.bufPos, p); i < len(v.bufPos) && v.bufPos[i] == p {
		return v.bufVal[i], true
	}
	if v.run != nil {
		return v.run.get(p)
	}
	return 0, false
}

func (v *view) get(p uint64) int64 {
	if val, ok := v.excGet(p); ok {
		return val
	}
	return v.def + v.segDelta(p)
}

// Get 返回位置 p 的值。p 必须 < Len()。
func (a *Array) Get(p uint64) int64 {
	a.checkPos(p)
	return a.v.get(p)
}

// Get 返回快照中位置 p 的值。
func (s *Snapshot) Get(p uint64) int64 {
	if p >= s.n {
		panic("sparse: Get out of range")
	}
	return s.v.get(p)
}

// rangeSum 求 [l,r) 的和：缺省贡献 + 惰性层贡献 + 例外修正。
func (v *view) rangeSum(l, r uint64) int64 {
	sum := v.def * int64(r-l) // 缺省贡献（含巨大缺省区，O(1)）
	for i := searchSeg(v.segs, l); i < len(v.segs) && v.segs[i].lo < r; i++ {
		s := v.segs[i]
		sum += s.d * int64(min(s.hi, r)-max(s.lo, l))
	}
	// 例外把"缺省+增量"替换为绝对值：加回差值。
	addExc := func(p uint64, val int64) { sum += val - v.def - v.segDelta(p) }
	for i := searchU64(v.bufPos, l); i < len(v.bufPos) && v.bufPos[i] < r; i++ {
		addExc(v.bufPos[i], v.bufVal[i])
	}
	if v.run != nil {
		ps, vv := v.run.scanRange(l, r, nil, nil)
		for k := range ps {
			if !v.bufHas(ps[k]) { // 写缓冲覆盖 run 旧值
				addExc(ps[k], vv[k])
			}
		}
	}
	return sum
}

// RangeSum 求 [l,r) 的和。要求 0 <= l <= r <= Len()。
func (a *Array) RangeSum(l, r uint64) int64 {
	a.checkRange(l, r)
	return a.v.rangeSum(l, r)
}

// RangeSum 求快照中 [l,r) 的和。
func (s *Snapshot) RangeSum(l, r uint64) int64 {
	if l > r || r > s.n {
		panic("sparse: RangeSum out of range")
	}
	return s.v.rangeSum(l, r)
}

// ---------- 修改 ----------

// Assign 点更新：把 p 固定为绝对值 v（显式例外，即使等于缺省也保留身份）。
func (a *Array) Assign(p uint64, v int64) {
	a.checkPos(p)
	a.bufSet(p, v)
	a.nnValid = false
}

// RangeAdd 给 [l,r) 每个位置加 d。惰性层切分 O(相交段数)，
// 例外调整 O(范围内例外数)，均与区间长度无关。
func (a *Array) RangeAdd(l, r uint64, d int64) {
	a.checkRange(l, r)
	if l == r || d == 0 {
		return
	}
	a.v.segs = addSeg(a.v.segs, l, r, d) // 产出新切片，旧快照共享不受影响
	// 写缓冲内例外就地调整（快照在创建时已复制缓冲，互不影响）。
	for i := searchU64(a.v.bufPos, l); i < len(a.v.bufPos) && a.v.bufPos[i] < r; i++ {
		a.v.bufVal[i] += d
	}
	// run 内未被缓冲覆盖的例外：先按当前缓冲状态收集，再统一写回。
	// （边查边写会因 bufSet 触发 flush 而使 bufHas 过滤器失效。）
	if a.v.run != nil {
		ps, vv := a.v.run.scanRange(l, r, nil, nil)
		var todoP []uint64
		var todoV []int64
		for k := range ps {
			if !a.v.bufHas(ps[k]) {
				todoP = append(todoP, ps[k])
				todoV = append(todoV, vv[k]+d)
			}
		}
		for k := range todoP {
			a.bufSet(todoP[k], todoV[k])
		}
	}
	a.nnValid = false
}

// ChangeDefault 把缺省值改为 d2：
//   - 未固定位置跟随：读取值整体平移（def 换为 d2，惰性层增量保留）；
//   - 例外位置不跟随：保持绝对值；
//   - 值等于 d2+segDelta(p) 的例外被回收（读取不变，此后重新跟随）。
//
// 旧快照保持旧缺省，互不影响。复杂度 O(例外数 + 段数)。
func (a *Array) ChangeDefault(d2 int64) {
	pos, val := a.v.mergedExc()
	kp, kv := pos[:0], val[:0]
	for i := range pos {
		if val[i] != d2+a.v.segDelta(pos[i]) { // 回收条件：值 == 回收后读取值
			kp = append(kp, pos[i])
			kv = append(kv, val[i])
		}
	}
	a.v.run = buildRun(kp, kv)
	a.v.bufPos = a.v.bufPos[:0]
	a.v.bufVal = a.v.bufVal[:0]
	a.v.def = d2
	a.nnValid = false
}

// Compact 强制把写缓冲合并进压缩 run（物理整理，不改变任何逻辑值）。
func (a *Array) Compact() { a.flush() }

// Snapshot 保留当前状态快照；后续对 Array 的修改不影响它。
func (a *Array) Snapshot() *Snapshot {
	s := &Snapshot{n: a.n, nonNeg: a.NonNeg()}
	s.v.def = a.v.def
	s.v.segs = a.v.segs                               // 不可变共享：Array 修改时会整体替换
	s.v.run = a.v.run                                 // 不可变共享
	s.v.bufPos = append([]uint64(nil), a.v.bufPos...) // 缓冲写时复制
	s.v.bufVal = append([]int64(nil), a.v.bufVal...)
	return s
}

// NonNeg 报告当前所有位置值是否非负（精确计算，O(段数+例外数)，带缓存）。
func (a *Array) NonNeg() bool {
	if !a.nnValid {
		a.nnVal = a.v.nonNeg(a.n)
		a.nnValid = true
	}
	return a.nnVal
}

// NonNeg 报告快照所有位置值是否非负（创建快照时精确计算）。
func (s *Snapshot) NonNeg() bool { return s.nonNeg }

// nonNeg 精确判定所有位置值是否非负。
// 注意段内位置可能全部被例外覆盖（def+d 并不真实出现），
// 纯缺省位置也可能不存在，需逐一确认存在性。
func (v *view) nonNeg(n uint64) bool {
	epos, eval := v.mergedExc()
	m := int64(1)<<62 - 1
	has := false
	for _, x := range eval { // 例外位置
		m = min(m, x)
		has = true
	}
	var segCover uint64
	for _, s := range v.segs { // 段内非例外位置
		segCover += s.hi - s.lo
		cnt := searchU64(epos, s.hi) - searchU64(epos, s.lo)
		if uint64(cnt) < s.hi-s.lo {
			m = min(m, v.def+s.d)
			has = true
		}
	}
	var excOutside uint64 // 不在任何段内的例外数
	for _, p := range epos {
		if v.segDelta(p) == 0 {
			excOutside++
		}
	}
	if n-segCover-excOutside > 0 { // 纯缺省位置存在
		m = min(m, v.def)
		has = true
	}
	return !has || m >= 0
}

// Exceptions 返回当前例外（显式固定位置）数量，用于观察回收与占用。
func (a *Array) Exceptions() int {
	pos, _ := a.v.mergedExc()
	return len(pos)
}

// ---------- 内部 ----------

func (a *Array) bufSet(p uint64, val int64) {
	i := searchU64(a.v.bufPos, p)
	if i < len(a.v.bufPos) && a.v.bufPos[i] == p {
		a.v.bufVal[i] = val
		return
	}
	a.v.bufPos = append(a.v.bufPos, 0)
	copy(a.v.bufPos[i+1:], a.v.bufPos[i:])
	a.v.bufPos[i] = p
	a.v.bufVal = append(a.v.bufVal, 0)
	copy(a.v.bufVal[i+1:], a.v.bufVal[i:])
	a.v.bufVal[i] = val
	if len(a.v.bufPos) >= bufFlush {
		a.flush()
	}
}

// flush 把写缓冲合并进压缩 run（单 run 整体重建，摊还 O(例外数/块大小)）。
func (a *Array) flush() {
	if len(a.v.bufPos) == 0 {
		return
	}
	pos, val := a.v.mergedExc()
	a.v.run = buildRun(pos, val)
	a.v.bufPos = a.v.bufPos[:0]
	a.v.bufVal = a.v.bufVal[:0]
}

// mergedExc 返回有效例外的排序合并视图（缓冲覆盖 run）。
func (v *view) mergedExc() ([]uint64, []int64) {
	var pos []uint64
	var val []int64
	if v.run != nil {
		pos, val = v.run.scanAll(pos, val)
	}
	return mergeExc(pos, val, v.bufPos, v.bufVal)
}

// mergeExc 归并两个排序例外序列，b（更新者）覆盖 a 的同位置条目。
func mergeExc(ap []uint64, av []int64, bp []uint64, bv []int64) ([]uint64, []int64) {
	op := make([]uint64, 0, len(ap)+len(bp))
	ov := make([]int64, 0, len(av)+len(bv))
	i, j := 0, 0
	for i < len(ap) && j < len(bp) {
		switch {
		case ap[i] < bp[j]:
			op = append(op, ap[i])
			ov = append(ov, av[i])
			i++
		case bp[j] < ap[i]:
			op = append(op, bp[j])
			ov = append(ov, bv[j])
			j++
		default: // 同位置：b 覆盖 a
			op = append(op, bp[j])
			ov = append(ov, bv[j])
			i++
			j++
		}
	}
	op = append(op, ap[i:]...)
	ov = append(ov, av[i:]...)
	op = append(op, bp[j:]...)
	ov = append(ov, bv[j:]...)
	return op, ov
}

// addSeg 在 [l,r) 上叠加增量 d，返回规范形新切片（不改动入参，
// 旧快照共享的切片因此不受影响）。扫描线一次性按序产出：
// 已有段切分、区间空洞补纯增量 d、相邻同 d 合并、零增量不存储。
func addSeg(segs []seg, l, r uint64, d int64) []seg {
	out := make([]seg, 0, len(segs)+2)
	push := func(s seg) {
		if s.lo >= s.hi || s.d == 0 {
			return
		}
		if last := len(out) - 1; last >= 0 && out[last].hi == s.lo && out[last].d == s.d {
			out[last].hi = s.hi
			return
		}
		out = append(out, s)
	}
	cur := l // [l,r) 中尚未被覆盖的起点
	flushHole := func() {
		if cur < r {
			push(seg{cur, r, d})
			cur = r
		}
	}
	for _, s := range segs {
		switch {
		case s.hi <= l: // 区间之前的段：原样保留
			push(s)
		case s.lo >= r: // 区间之后的段：先补空洞再原样保留
			flushHole()
			push(s)
		default: // 相交段：切分 + 叠加
			if s.lo > cur { // 段前空洞
				push(seg{cur, s.lo, d})
			}
			if s.lo < l {
				push(seg{s.lo, l, s.d})
			}
			push(seg{max(s.lo, l), min(s.hi, r), s.d + d})
			if s.hi > r {
				push(seg{r, s.hi, s.d})
			}
			cur = max(cur, min(s.hi, r))
		}
	}
	flushHole() // 尾部空洞
	return out
}

func (a *Array) checkPos(p uint64) {
	if p >= a.n {
		panic("sparse: position out of range")
	}
}

func (a *Array) checkRange(l, r uint64) {
	if l > r || r > a.n {
		panic("sparse: range out of bounds")
	}
}

// ---------- 物理占用 ----------

// PhysicalBytes 返回持久结构的实际占用字节（编码布局真实统计，非估算）：
// 头 16B + 惰性层 24B/段 + run（目录 8B/块 + 块头 10B + posEnc + valEnc）
// + 写缓冲 16B/条。
func (a *Array) PhysicalBytes() int64 {
	t := int64(16)
	t += int64(len(a.v.segs)) * 24
	if r := a.v.run; r != nil {
		t += 16 + int64(len(r.bases))*8
		for i := range r.blocks {
			b := &r.blocks[i]
			t += 10 + int64(len(b.posEnc)) + int64(len(b.valEnc))
		}
	}
	t += int64(len(a.v.bufPos)) * 16
	return t
}

// LogicalBytes 返回等效稠密 int64 数组的字节数。
func (a *Array) LogicalBytes() int64 { return int64(a.n) * 8 }
