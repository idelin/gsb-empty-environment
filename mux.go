package logasm

import (
	"errors"
	"fmt"
	"sort"
)

// ErrBackpressure 在总缓冲额度耗尽时返回；输入未被消费，
// 调用方应在 Flush/记录发出释放额度后重试。
var ErrBackpressure = errors.New("logasm: buffer budget exhausted, apply backpressure")

// Line 是行式输入。Seq 为来源内单调序号；LineNo 为原始行号（>0 时
// 覆盖内部计数）；CorrectsLineNo >= 0 表示该行是对某原始行的迟到更正。
type Line struct {
	Source         string
	Epoch          uint64
	Seq            uint64
	LineNo         int
	Text           string
	CorrectsLineNo int
}

// Mux 按 (Source, Epoch) 独立重组多源日志，并维护总缓冲额度。
type Mux struct {
	rules    Rules // 新来源的默认规则
	budget   int   // 总缓冲字节额度，0 表示不限
	sink     Sink
	onRecord func(Record)
	sources  map[string]*sourceState
	// ReorderWindow 限定重排窗口：序号落后超过窗口的缺失序号按
	// 永久缺口跳过，防止重排缓冲无限增长。0 表示取默认值 256。
	ReorderWindow uint64
}

type sourceState struct {
	source  string
	epoch   uint64
	asm     *assembler
	nextSeq uint64
	maxSeen uint64
	started bool
	reorder map[uint64]Line
	owner   map[int]string    // 原始行号 -> 已闭合记录 ID
	closed  map[string]Record // 已闭合记录，供迟到更正产生新修订
}

// NewMux 创建多源重组器。sink 可为 nil（丢弃遥测）。
func NewMux(defaultRules Rules, budget int, sink Sink, onRecord func(Record)) *Mux {
	if sink == nil {
		sink = &Recorder{}
	}
	return &Mux{
		rules:    defaultRules,
		budget:   budget,
		sink:     sink,
		onRecord: onRecord,
		sources:  make(map[string]*sourceState),
	}
}

func muxKey(source string, epoch uint64) string {
	return fmt.Sprintf("%s\x00%d", source, epoch)
}

func (m *Mux) state(source string, epoch uint64) *sourceState {
	k := muxKey(source, epoch)
	if st, ok := m.sources[k]; ok {
		return st
	}
	st := &sourceState{
		source:  source,
		epoch:   epoch,
		nextSeq: 1,
		reorder: make(map[uint64]Line),
		owner:   make(map[int]string),
		closed:  make(map[string]Record),
	}
	st.asm = newAssembler(source, epoch, m.rules, func(r Record) {
		st.closed[r.ID] = r
		m.emitRecord(r)
	}, m.sink.Emit)
	m.sources[k] = st
	return st
}

func (m *Mux) emitRecord(r Record) {
	if m.onRecord != nil {
		m.onRecord(r)
	}
	m.sink.Emit(Event{Kind: EventRecordEmitted, Source: r.Source, Epoch: r.Epoch, RecordID: r.ID})
}

// buffered 返回当前占用的总缓冲字节。
func (m *Mux) buffered() int {
	n := 0
	for _, st := range m.sources {
		n += st.asm.bufferedBytes()
		for _, l := range st.reorder {
			n += len(l.Text)
		}
	}
	return n
}

func (m *Mux) admit(bytes int) error {
	if m.budget > 0 && m.buffered()+bytes > m.budget {
		m.sink.Emit(Event{Kind: EventBackpressure, Detail: "buffer budget exhausted"})
		return ErrBackpressure
	}
	return nil
}

// SetRules 热换某 (source, epoch) 的拼接规则。已发出的记录 ID、
// 原始行号与归属关系不受影响，新规则只作用于后续输入。
func (m *Mux) SetRules(source string, epoch uint64, rules Rules) {
	st := m.state(source, epoch)
	st.asm.rules = rules
	m.sink.Emit(Event{Kind: EventRulesSwapped, Source: source, Epoch: epoch,
		Detail: "rules hot-swapped, line identity preserved"})
}

// FeedChunk 流入原始文本块（流切分路径），行号与序号由内部顺序分配。
func (m *Mux) FeedChunk(source string, epoch uint64, chunk string) error {
	if err := m.admit(len(chunk)); err != nil {
		return err
	}
	m.state(source, epoch).asm.Write(chunk)
	return nil
}

// Feed 流入一条带序号的行。序号缺口、乱序、重复与迟到更正在此处理。
func (m *Mux) Feed(l Line) error {
	st := m.state(l.Source, l.Epoch)

	if l.CorrectsLineNo >= 0 {
		return m.applyCorrection(st, l)
	}

	if !st.started {
		// 会话首行锚定序号游标：行式路径要求首行先达，
		// 其后乱序行由重排缓冲按序号放行。
		st.started = true
		st.nextSeq = l.Seq
		st.maxSeen = l.Seq
	}
	if l.Seq > st.maxSeen {
		st.maxSeen = l.Seq
	}

	switch {
	case l.Seq < st.nextSeq:
		// 迟到行：绝不拼进当前待定记录，按原始行号路由。
		m.routeLate(st, l)
		return nil
	case l.Seq > st.nextSeq:
		m.sink.Emit(Event{Kind: EventSeqGap, Source: l.Source, Epoch: l.Epoch,
			Detail: fmt.Sprintf("expected seq %d, got %d", st.nextSeq, l.Seq)})
	}

	if err := m.admit(len(l.Text)); err != nil {
		return err
	}
	st.reorder[l.Seq] = l
	m.drain(st)
	return nil
}

// drain 按序号连续段从重排缓冲驱动拼接器；缺失序号落后超过
// 重排窗口时按永久缺口跳过（到达时已上报 seq_gap）。
func (m *Mux) drain(st *sourceState) {
	window := m.ReorderWindow
	if window == 0 {
		window = 256
	}
	for {
		l, ok := st.reorder[st.nextSeq]
		if !ok {
			if st.maxSeen >= st.nextSeq+window {
				st.nextSeq++
				continue
			}
			return
		}
		delete(st.reorder, st.nextSeq)
		st.asm.writeLineAt(l.LineNo, l.Text)
		st.owner[st.asm.lineNo] = st.asm.pending.ID
		st.nextSeq++
	}
}

// routeLate 处理序号小于已消费序号的迟到行。
func (m *Mux) routeLate(st *sourceState, l Line) {
	if _, dup := st.owner[l.LineNo]; dup || st.asm.pendingOwns(l.LineNo) {
		m.sink.Emit(Event{Kind: EventDuplicateDropped, Source: l.Source, Epoch: l.Epoch,
			Detail: fmt.Sprintf("line %d already consumed", l.LineNo)})
		return
	}
	// 归属判定：原始行号 no-1 的记录即该行迟到时应属的记录。
	if id, ok := st.owner[l.LineNo-1]; ok {
		if st.asm.pending != nil && st.asm.pending.ID == id {
			insertLine(st.asm.pending, l.LineNo, l.Text)
			st.asm.pending.Revision++
			st.owner[l.LineNo] = id
			m.sink.Emit(Event{Kind: EventLateRevision, Source: l.Source, Epoch: l.Epoch,
				RecordID: id, Detail: fmt.Sprintf("late line %d revised pending record", l.LineNo)})
			return
		}
		rec := st.closed[id]
		insertLine(&rec, l.LineNo, l.Text)
		rec.Revision++
		st.closed[id] = rec
		st.owner[l.LineNo] = id
		m.emitRecord(rec)
		m.sink.Emit(Event{Kind: EventLateRevision, Source: l.Source, Epoch: l.Epoch,
			RecordID: id, Detail: fmt.Sprintf("late line %d revised original record", l.LineNo)})
		return
	}
	// 无归属：独立成条立即发出，绝不并入任何现存记录。
	st.asm.recSeq++
	rec := Record{
		ID:        recordID(l.Source, l.Epoch, st.asm.recSeq),
		Source:    l.Source,
		Epoch:     l.Epoch,
		StartLine: l.LineNo,
		EndLine:   l.LineNo,
		LineNos:   []int{l.LineNo},
		Lines:     []string{l.Text},
	}
	st.owner[l.LineNo] = rec.ID
	st.closed[rec.ID] = rec
	m.emitRecord(rec)
	m.sink.Emit(Event{Kind: EventLateUnowned, Source: l.Source, Epoch: l.Epoch,
		RecordID: rec.ID, Detail: fmt.Sprintf("late line %d has no owner, emitted standalone", l.LineNo)})
}

// insertLine 按原始行号有序地把补行插入记录。
func insertLine(rec *Record, no int, text string) {
	i := sort.Search(len(rec.LineNos), func(i int) bool { return rec.LineNos[i] >= no })
	rec.LineNos = append(rec.LineNos, 0)
	copy(rec.LineNos[i+1:], rec.LineNos[i:])
	rec.LineNos[i] = no
	rec.Lines = append(rec.Lines, "")
	copy(rec.Lines[i+1:], rec.Lines[i:])
	rec.Lines[i] = text
	if no < rec.StartLine {
		rec.StartLine = no
	}
	if no > rec.EndLine {
		rec.EndLine = no
	}
}

// applyCorrection 处理迟到更正：命中待定记录就地修订；命中已闭合
// 记录则产生 Revision+1 的新版本，ID 不变。
func (m *Mux) applyCorrection(st *sourceState, l Line) error {
	no := l.CorrectsLineNo
	if st.asm.revisePending(no, l.Text) {
		m.sink.Emit(Event{Kind: EventRecordRevised, Source: l.Source, Epoch: l.Epoch,
			RecordID: st.asm.pending.ID, Detail: fmt.Sprintf("pending line %d corrected", no)})
		return nil
	}
	id, ok := st.owner[no]
	if !ok {
		m.sink.Emit(Event{Kind: EventLateUnowned, Source: l.Source, Epoch: l.Epoch,
			Detail: fmt.Sprintf("correction for unknown line %d dropped", no)})
		return nil
	}
	rec := st.closed[id]
	for i, ln := range rec.LineNos {
		if ln == no {
			rec.Lines[i] = l.Text
		}
	}
	rec.Revision++
	st.closed[id] = rec
	m.emitRecord(rec)
	m.sink.Emit(Event{Kind: EventRecordRevised, Source: l.Source, Epoch: l.Epoch,
		RecordID: id, Detail: fmt.Sprintf("closed record revised to rev %d", rec.Revision)})
	return nil
}

// Flush 结束全部来源：重排缓冲中剩余行按序号放行（缺口已上报），
// 随后各拼接器冲刷半行与待定记录。
func (m *Mux) Flush() {
	for _, k := range m.sortedKeys() {
		st := m.sources[k]
		seqs := make([]int, 0, len(st.reorder))
		for s := range st.reorder {
			seqs = append(seqs, int(s))
		}
		sort.Ints(seqs)
		for _, s := range seqs {
			l := st.reorder[uint64(s)]
			delete(st.reorder, uint64(s))
			st.asm.writeLineAt(l.LineNo, l.Text)
			st.owner[st.asm.lineNo] = st.asm.pending.ID
			st.nextSeq = uint64(s) + 1
		}
		st.asm.Flush()
	}
}

func (m *Mux) sortedKeys() []string {
	keys := make([]string, 0, len(m.sources))
	for k := range m.sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
