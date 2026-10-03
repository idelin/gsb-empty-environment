package peritext

// AnchorKind 表示标注边界锚点的种类。
type AnchorKind int

const (
	DocStart   AnchorKind = iota // 文档开头
	DocEnd                       // 文档结尾
	BeforeChar                   // 某字符之前的边界（该处插入不进入区间）
	AfterChar                    // 某字符之后的边界（该处插入进入区间）
)

// Anchor 是格式标注区间的边界，锚定到字符 ID 而非下标，
// 因此并发插入不会使区间错位。
type Anchor struct {
	Kind AnchorKind
	ID   CharID
}

// MarkID 唯一标识一条格式标注。
type MarkID struct {
	Actor string
	Seq   uint64
}

// Mark 是一条格式标注操作，例如 key="bold" value="true"。
// 取消格式用 value="false"（或空串）表示，同样是一条标注，
// 与“设置”标注一起参与 LWW 判定。
type Mark struct {
	ID      MarkID
	Key     string
	Value   string
	Start   Anchor
	End     Anchor
	Lamport uint64
}

// less 定义标注间的 LWW 优先级：先 Lamport 时间，后 Actor（并发时确定性裁决）。
func (m Mark) less(o Mark) bool {
	if m.Lamport != o.Lamport {
		return m.Lamport < o.Lamport
	}
	return m.ID.Actor < o.ID.Actor
}

// MarkRange 对字符下标区间 [from, to) 添加一条格式标注（本地操作）。
//
// 边界锚定遵循 Peritext 的默认展开规则：
//   - 起点锚定到 from 处字符的左边界，在区间起点处插入的文本不进入区间；
//   - 终点锚定到 to-1 处字符的右边界，在区间末尾（因果上晚于本标注）
//     追加的文本进入区间。
func (d *Document) MarkRange(key, value string, from, to int) Mark {
	if from < 0 || to > len(d.nodes) || from >= to {
		panic("peritext: 非法的标注区间")
	}
	start := Anchor{Kind: BeforeChar, ID: d.nodes[from].id}
	var end Anchor
	if to == len(d.nodes) {
		end = Anchor{Kind: DocEnd}
	} else {
		end = Anchor{Kind: AfterChar, ID: d.nodes[to-1].id}
	}
	d.clock++
	d.seq++
	m := Mark{
		ID:      MarkID{Actor: d.actor, Seq: d.seq},
		Key:     key,
		Value:   value,
		Start:   start,
		End:     end,
		Lamport: d.clock,
	}
	d.marks[m.ID] = m
	return m
}

// ApplyMark 应用一条（可能来自远程的）格式标注，幂等。
func (d *Document) ApplyMark(m Mark) {
	if _, ok := d.marks[m.ID]; ok {
		return
	}
	d.marks[m.ID] = m
	if m.Lamport > d.clock {
		d.clock = m.Lamport
	}
}

// startIndex 将起点锚点解析为边界下标（边界左侧的字符数）。
func (d *Document) startIndex(a Anchor) int {
	switch a.Kind {
	case DocStart:
		return 0
	case DocEnd:
		return len(d.nodes)
	case BeforeChar:
		return d.index[a.ID]
	case AfterChar:
		return d.index[a.ID] + 1
	}
	panic("peritext: 未知锚点类型")
}

// gapSet 返回“在 X 之后、且因果上晚于时刻 t 插入”的字符集合：
// 即父节点为 X（或递归地为集合内字符）且 Lamport 时间晚于 t 的字符。
// 这些字符是在标注终点边界处追加的文本，应进入区间（终点展开）。
// 注意 X 原有的右邻后代（在标注创建之前就存在）不在集合内。
func (d *Document) gapSet(x CharID, t uint64) map[CharID]bool {
	gap := map[CharID]bool{x: true}
	for _, n := range d.nodes { // 父节点必然先于子节点出现
		if n.lamport > t && gap[n.parent] {
			gap[n.id] = true
		}
	}
	delete(gap, x)
	return gap
}

// coverage 计算标注 m 覆盖了哪些字符。
func (d *Document) coverage(m Mark) []bool {
	cov := make([]bool, len(d.nodes))
	s := d.startIndex(m.Start)
	switch m.End.Kind {
	case DocEnd:
		for i := s; i < len(cov); i++ {
			cov[i] = true
		}
	case DocStart:
		// 空区间
	case BeforeChar:
		e := d.index[m.End.ID]
		for i := s; i < e; i++ {
			cov[i] = true
		}
	case AfterChar:
		x := m.End.ID
		xi := d.index[x]
		gap := d.gapSet(x, m.Lamport)
		for i := s; i < len(cov); i++ {
			if i <= xi || gap[d.nodes[i].id] {
				cov[i] = true
			}
		}
	}
	return cov
}

// Formats 返回每个字符生效的格式集合（key -> value）。
//
// 对每个字符、每个 key，在所有覆盖该字符的标注中取
// (Lamport, Actor) 最大者生效——这保证：
//   - 因果在后的操作（如看到加粗后再取消）必然胜出；
//   - 并发冲突（A 加粗、B 同时取消）在所有副本上得到相同的确定性结果。
//
// 生效值为 "false" 或空串时表示该格式被取消，不出现在结果中。
func (d *Document) Formats() []map[string]string {
	type span struct {
		cov []bool
		m   Mark
	}
	spans := make([]span, 0, len(d.marks))
	for _, m := range d.marks {
		spans = append(spans, span{d.coverage(m), m})
	}
	out := make([]map[string]string, len(d.nodes))
	for i := range out {
		best := make(map[string]Mark)
		for _, sp := range spans {
			if !sp.cov[i] {
				continue
			}
			cur, ok := best[sp.m.Key]
			if !ok || cur.less(sp.m) {
				best[sp.m.Key] = sp.m
			}
		}
		fm := make(map[string]string, len(best))
		for k, m := range best {
			if m.Value != "" && m.Value != "false" {
				fm[k] = m.Value
			}
		}
		out[i] = fm
	}
	return out
}
