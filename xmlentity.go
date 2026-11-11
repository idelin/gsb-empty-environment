// Package xmlentity 提供一个位于 encoding/xml 解码器前面的实体展开读取器。
//
// 它只处理内部 DTD 子集里声明的内部通用实体（<!ENTITY name "value">），
// 在字节流经过时即时展开实体引用，不缓冲整份文档：
//   - 外部实体（SYSTEM/PUBLIC）、参数实体（%name;）、未解析实体（NDATA）一律拒绝；
//   - 直接或间接递归的实体引用一律拒绝；
//   - 按展开后总输出字节数、单个实体被引用次数、实体嵌套深度设置预算，
//     超限立即返回错误，且不会先把全文缓冲到内存。
package xmlentity

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// 实体引用展开相关的哨兵错误。具体超出了哪个预算可通过 errors.As 检查
// *LimitError 的 Limit 字段；语法错误可通过 errors.Is 判断 *MalformedError。
var (
	// ErrLimitExceeded 表示触发了 Limits 中配置的某一项预算。
	ErrLimitExceeded = errors.New("xmlentity: expansion budget exceeded")
	// ErrRecursiveEntity 表示实体定义或引用形成了递归。
	ErrRecursiveEntity = errors.New("xmlentity: recursive entity reference")
	// ErrExternalEntity 表示出现了外部实体、外部 DTD、参数实体或未解析实体。
	ErrExternalEntity = errors.New("xmlentity: external/parameter/unparsed entities are not allowed")
	// ErrMalformed 表示 DTD 或实体引用的字节流不符合本读取器支持的语法。
	ErrMalformed = errors.New("xmlentity: malformed xml/entity syntax")
)

// Limit 标识被触发的预算种类。
type Limit string

const (
	LimitExpandedBytes   Limit = "expanded_bytes"
	LimitRefsPerEntity   Limit = "refs_per_entity"
	LimitTotalEntityRefs Limit = "total_entity_refs"
	LimitNestDepth       Limit = "nest_depth"
	LimitDTDBytes        Limit = "dtd_bytes"
)

// LimitError 在某项预算被触发时返回，包装 ErrLimitExceeded。
type LimitError struct {
	Limit Limit
	// Value 是触发预算时的实际计数。
	Value int64
	// Budget 是配置的预算值。
	Budget int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("xmlentity: %s budget exceeded: %d > %d", e.Limit, e.Value, e.Budget)
}

func (e *LimitError) Is(target error) bool { return target == ErrLimitExceeded }

// MalformedError 包装 ErrMalformed，附带字节流位置上下文。
type MalformedError struct {
	Context string
}

func (e *MalformedError) Error() string {
	if e.Context == "" {
		return ErrMalformed.Error()
	}
	return ErrMalformed.Error() + ": " + e.Context
}

func (e *MalformedError) Is(target error) bool { return target == ErrMalformed }

func malformed(format string, args ...any) error {
	return &MalformedError{Context: fmt.Sprintf(format, args...)}
}

// Limits 配置展开读取器的所有预算。零值字段会使用 DefaultLimits 中的对应值。
type Limits struct {
	// MaxExpandedBytes 是展开后允许输出的总字节数（含原样透传的字节）。
	MaxExpandedBytes int64
	// MaxRefsPerEntity 是同一个实体在全文中允许被引用（展开）的次数。
	MaxRefsPerEntity int64
	// MaxTotalEntityRefs 是全文允许的自定义实体引用总次数。
	MaxTotalEntityRefs int64
	// MaxNestDepth 是实体引用允许的最大嵌套深度（根文档为 0 层）。
	MaxNestDepth int
	// MaxDTDBytes 是内部 DTD 子集（含 DOCTYPE 头部）允许的最大字节数。
	MaxDTDBytes int64
}

// DefaultLimits 是 NewReader 在 Limits 字段为零值时采用的默认预算。
// 默认值偏向保守，足以容纳正常文档，同时把内存与 CPU 放大锁死在常数级。
var DefaultLimits = Limits{
	MaxExpandedBytes:   16 << 20, // 16 MiB
	MaxRefsPerEntity:   10_000,
	MaxTotalEntityRefs: 100_000,
	MaxNestDepth:       32,
	MaxDTDBytes:        1 << 20, // 1 MiB
}

func (l *Limits) normalize() {
	d := DefaultLimits
	if l.MaxExpandedBytes <= 0 {
		l.MaxExpandedBytes = d.MaxExpandedBytes
	}
	if l.MaxRefsPerEntity <= 0 {
		l.MaxRefsPerEntity = d.MaxRefsPerEntity
	}
	if l.MaxTotalEntityRefs <= 0 {
		l.MaxTotalEntityRefs = d.MaxTotalEntityRefs
	}
	if l.MaxNestDepth <= 0 {
		l.MaxNestDepth = d.MaxNestDepth
	}
	if l.MaxDTDBytes <= 0 {
		l.MaxDTDBytes = d.MaxDTDBytes
	}
}

// entity 是一条内部通用实体定义。value 在解析完成后不再变化，
// 因此多个展开帧可以安全地共享同一份底层数组。
type entity struct {
	name  string
	value []byte
}

// frame 是实体展开栈上的一帧，对应某条实体替换文本的读取位置。
type frame struct {
	ent  *entity
	data []byte
	pos  int
}

const (
	// 单条实体引用名的最大长度，超出部分按普通文本处理，避免无界缓冲。
	maxNameBytes = 4096
	// nextByte 的回退缓冲容量，仅需容纳一次标记预读（<!...）。
	pushbackCap = 9
)

// Reader 包装一个 XML 字节流，在读取过程中即时展开内部通用实体引用。
// 它实现 io.Reader，可直接传给 xml.NewDecoder。
type Reader struct {
	src      *bufio.Reader
	limits   Limits
	entities map[string]*entity

	frames   []frame
	pushback []byte // 标记预读后退回的少量字节

	// 输出状态机。
	state        state
	rawTerm      []byte // raw 状态下寻找的终止符
	rawMatch     int    // 终止符的当前匹配长度
	nameBuf      []byte // &name; 中 name 的累积缓冲
	doctypeMatch int    // 序言里 "<!DOCTYPE" 的渐进匹配长度
	inProlog     bool   // 还没遇到文档根元素，DOCTYPE 只允许出现在这里

	// 预算计数。
	totalRefs  int64
	refsPerEnt map[string]int64
	// produced 是状态机已产生（含 DTD 透传）的总字节，独立于交付进度，
	// 用于在产出瞬间触发展开字节预算。
	produced int64
	// active 是当前正在展开的实体定义栈，与 frames 一一对应（frames[0] 哨兵除外）。
	// 压入实体帧时入栈，nextByte 回退到父来源弹出帧时同步出栈。
	active []*entity
	// ampDepth 是读到当前 '&' 那一刻活动实体栈 active 的长度快照，
	// 0 表示根文档引用，>0 表示实体内部的嵌套引用。
	ampDepth int
	// peekBuf 是标记预读的可复用缓冲，避免每次遇到 '<' 都分配。
	peekBuf []byte

	// 解析 DOCTYPE 时缓存的原始片段，待输出缓冲有空位时原样吐回。
	dtdPending []byte
	// 状态机已产生、但调用方缓冲暂未取走的输出，保证跨 Read 不丢字节。
	outPending []byte

	// 终止状态：缓存的错误会在已产出数据排空后持续返回。
	err error
}

type state int

const (
	stateData state = iota // 普通字符数据 / 标记
	stateRaw               // 注释、CDATA、PI 内部，原样透传直到 rawTerm
	stateAmp               // 读到 '&'，正在判断引用类型
	stateName              // 正在累积通用实体引用名
)

var _ io.Reader = (*Reader)(nil)

// NewReader 用给定预算包装 r。limits 的零值字段会回退到 DefaultLimits。
func NewReader(r io.Reader, limits Limits) *Reader {
	limits.normalize()
	return &Reader{
		src:        bufio.NewReaderSize(r, 4096),
		limits:     limits,
		entities:   make(map[string]*entity),
		refsPerEnt: make(map[string]int64),
		// 栈底放一个根文档哨兵帧（ent==nil），实体帧永远在它之上。
		frames:     make([]frame, 1, 9),
		pushback:   make([]byte, 0, pushbackCap),
		outPending: make([]byte, 0, 256),
		peekBuf:    make([]byte, 0, 8),
		state:      stateData,
		inProlog:   true,
	}
}

// Read 实现 io.Reader。返回的字节流已经完成实体展开；
// 触发预算或语法错误时返回非 nil 错误（已产出的字节会先交付）。
func (r *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	off := 0

	for {
		// 先把已产出的字节交付给调用方。
		for (len(r.outPending) > 0 || len(r.dtdPending) > 0) && off < len(p) {
			var chunk []byte
			if len(r.outPending) > 0 {
				chunk = r.outPending
			} else {
				chunk = r.dtdPending
			}
			n, err := r.emit(p[off:], chunk)
			off += n
			if len(r.outPending) > 0 {
				// 把未交付的尾部移回开头，底层数组得以跨 Read 复用。
				remaining := copy(r.outPending, r.outPending[n:])
				r.outPending = r.outPending[:remaining]
			} else {
				r.dtdPending = r.dtdPending[n:]
			}
			if err != nil {
				r.err = err
				if off > 0 {
					return off, nil
				}
				return 0, err
			}
		}
		if off == len(p) {
			return off, nil
		}
		if r.err != nil {
			if off > 0 {
				return off, nil // 先交付已产出的数据，下一次 Read 再返回错误
			}
			return 0, r.err
		}
		b, ok := r.nextByte()
		if !ok {
			if r.state == stateAmp || r.state == stateName {
				// 流结束时引用没有闭合，'&' 只是普通字符，原样吐出。
				if err := r.flushDanglingRef(); err != nil {
					r.err = err
					break
				}
				continue
			}
			if off > 0 {
				return off, nil
			}
			if len(r.outPending) > 0 || len(r.dtdPending) > 0 {
				continue
			}
			return 0, io.EOF
		}
		if err := r.step(b); err != nil {
			r.err = err
			break
		}
	}
	// 出错路径：尽量先交付已产出的数据。
	for len(r.outPending) > 0 && off < len(p) {
		n := copy(p[off:], r.outPending)
		off += n
		r.outPending = r.outPending[n:]
	}
	if off > 0 {
		return off, nil
	}
	return 0, r.err
}

// nextByte 从字节来源栈取下一个字节：实体替换文本优先于底层文档。
func (r *Reader) nextByte() (byte, bool) {
	// 实体帧压在栈顶，优先从栈顶取字节，保证替换文本先于被中断的文档输出。
	for len(r.frames) > 1 {
		top := len(r.frames) - 1
		f := &r.frames[top]
		if f.pos >= len(f.data) {
			// 顶层实体帧耗尽：展开路径结束，弹出后继续外层实体或根文档哨兵。
			r.popExhausted()
			continue
		}
		b := f.data[f.pos]
		f.pos++
		return b, true
	}
	// 实体帧都已耗尽后，才消费标记预读退回的根文档字节。
	if n := len(r.pushback); n > 0 {
		b := r.pushback[n-1]
		r.pushback = r.pushback[:n-1]
		return b, true
	}
	// 只剩根文档哨兵：从底层输入流读取。
	b, err := r.src.ReadByte()
	if err != nil {
		return 0, false
	}
	return b, true
}

// popExhausted 弹出栈顶已读完的实体帧，并同步 active 栈。
func (r *Reader) popExhausted() {
	for len(r.frames) > 1 && r.frames[len(r.frames)-1].pos >= len(r.frames[len(r.frames)-1].data) {
		r.frames = r.frames[:len(r.frames)-1]
		if len(r.active) > 0 {
			r.active = r.active[:len(r.active)-1]
		}
	}
}

// unreadByte 退回一个字节。回退仅用于标记预读（一次最多 8 个字节），
// 切片按需增长，不会随文档体量扩张。
func (r *Reader) unreadByte(b byte) {
	r.pushback = append(r.pushback, b)
}

// emit 把 src 拷进 dst，返回拷贝字节数。预算计费统一在 produce/
// recordProduced 完成，这里只负责把待输出队列交付给调用方。
func (r *Reader) emit(dst, src []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	return copy(dst, src), nil
}

// produce 把状态机产出的字节追加到待输出队列，并立即做展开预算检查，
// 保证超限时立刻报错而不是等调用方把缓冲取空。
func (r *Reader) produce(data []byte) error {
	if err := r.recordProduced(int64(len(data))); err != nil {
		allowed := r.limits.MaxExpandedBytes - r.produced
		if allowed < 0 {
			allowed = 0
		}
		r.outPending = append(r.outPending, data[:allowed]...)
		return &LimitError{
			Limit: LimitExpandedBytes, Value: r.produced, Budget: r.limits.MaxExpandedBytes,
		}
	}
	r.outPending = append(r.outPending, data...)
	return nil
}

func (r *Reader) produceByte(b byte) error {
	if err := r.recordProduced(1); err != nil {
		return err
	}
	r.outPending = append(r.outPending, b)
	return nil
}

// produceReference 原样吐出一个 &name; 引用（预定义或未声明实体），
// 直接追加到输出队列以避免字符串拼接产生的临时分配。
func (r *Reader) produceReference(name string) error {
	if err := r.recordProduced(int64(len(name) + 2)); err != nil {
		return err
	}
	r.outPending = append(r.outPending, '&')
	r.outPending = append(r.outPending, name...)
	r.outPending = append(r.outPending, ';')
	return nil
}

// recordProduced 累加产出字节数并对照展开总字节预算。
func (r *Reader) recordProduced(n int64) error {
	r.produced += n
	if r.produced > r.limits.MaxExpandedBytes {
		return &LimitError{
			Limit: LimitExpandedBytes, Value: r.produced, Budget: r.limits.MaxExpandedBytes,
		}
	}
	return nil
}

// step 处理单个输入字节并推进状态机。
func (r *Reader) step(b byte) error {
	switch r.state {
	case stateRaw:
		return r.stepRaw(b)
	case stateAmp:
		return r.stepAmp(b)
	case stateName:
		return r.stepName(b)
	default:
		return r.stepData(b)
	}
}

func (r *Reader) stepData(b byte) error {
	if b == '&' {
		// 快照 '&' 来源层级：此时 active 尚未压入新帧，
		// 0 表示根文档，>0 表示处于某条实体的替换文本中。
		r.ampDepth = len(r.active)
		r.state = stateAmp
		r.nameBuf = r.nameBuf[:0]
		return nil
	}
	if b == '<' {
		return r.stepMarkupStart()
	}
	if r.inProlog {
		// 序言里渐进匹配 "<!DOCTYPE"。匹配中的前缀字节要照常输出，
		// 一旦后续不匹配，已输出的前缀仍是普通文本，不会丢字节。
		if b == doctypeKeyword[r.doctypeMatch] {
			if err := r.produceByte(b); err != nil {
				return err
			}
			r.doctypeMatch++
			if r.doctypeMatch == len(doctypeKeyword) {
				r.doctypeMatch = 0
				return r.parseDoctype()
			}
			return nil
		}
		// 匹配失败：此前输出的前缀保持原样，当前字节按普通文本处理。
		r.doctypeMatch = 0
	}
	return r.produceByte(b)
}

func (r *Reader) stepAmp(b byte) error {
	switch {
	case b == '#':
		// 字符数字引用（&#...;）由下游 encoding/xml 解码，原样透传。
		r.state = stateData
		return r.produce([]byte{'&', '#'})
	case isNameStart(b):
		r.nameBuf = append(r.nameBuf[:0], b)
		r.state = stateName
		return nil
	default:
		// 非法引用开头，'&' 按普通字符处理。
		r.state = stateData
		return r.produce([]byte{'&', b})
	}
}

func (r *Reader) stepName(b byte) error {
	if isNameByte(b) && len(r.nameBuf) < maxNameBytes {
		r.nameBuf = append(r.nameBuf, b)
		return nil
	}
	if b == ';' && len(r.nameBuf) > 0 && len(r.nameBuf) < maxNameBytes {
		name := string(r.nameBuf)
		r.nameBuf = r.nameBuf[:0]
		r.state = stateData
		return r.resolveReference(name)
	}
	// 名字被截断（超长或遇到非法字符且没有分号）：引用不成立，原样吐出。
	dangling := append([]byte{'&'}, r.nameBuf...)
	r.nameBuf = r.nameBuf[:0]
	r.state = stateData
	if err := r.produce(dangling); err != nil {
		return err
	}
	return r.produceByte(b)
}

// flushDanglingRef 在 EOF 处吐出未闭合的 '&' 或 '&name'。
func (r *Reader) flushDanglingRef() error {
	if r.state == stateAmp {
		r.state = stateData
		return r.produceByte('&')
	}
	dangling := append([]byte{'&'}, r.nameBuf...)
	r.nameBuf = r.nameBuf[:0]
	r.state = stateData
	return r.produce(dangling)
}

// stepRaw 处理注释 / CDATA / PI 内部的字节，全部原样透传直到终止符。
// 终止符匹配采用重叠回退（resync），保证类似 "-->-->" 的输入不漏匹配。
func (r *Reader) stepRaw(b byte) error {
	if b == r.rawTerm[r.rawMatch] {
		r.rawMatch++
		if r.rawMatch == len(r.rawTerm) {
			r.state = stateData
			r.rawTerm = nil
			r.rawMatch = 0
		}
	} else if r.rawMatch > 0 {
		// 已匹配的前缀仍然要输出；再从重叠位置重新尝试匹配。
		prefix := r.rawTerm[:r.rawMatch]
		r.rawMatch = resync(r.rawTerm, prefix, b)
	}
	return r.produceByte(b)
}

// resync 在新字节 b 打断部分匹配后，计算终止符 term 的新匹配长度。
// matched 是此前已匹配的 term 前缀（含其末尾，b 不属于该前缀）。
func resync(term, matched []byte, b byte) int {
	// 虚拟序列为 matched + b（长度 m+1），不构造实际切片以避免分配。
	m := len(matched)
	virtual := func(i int) byte {
		if i < m {
			return matched[i]
		}
		return b
	}
	for k := m; k > 0; k-- {
		start := m + 1 - k
		ok := true
		for j := 0; j < k; j++ {
			if virtual(start+j) != term[j] {
				ok = false
				break
			}
		}
		if ok {
			return k
		}
	}
	if b == term[0] {
		return 1
	}
	return 0
}

// stepMarkupStart 在遇到 '<' 后预读区分注释、CDATA、PI、DOCTYPE 与普通标记。
func (r *Reader) stepMarkupStart() error {
	peeked := r.peekBuf[:0]
	for len(peeked) < 8 {
		b, ok := r.nextByte()
		if !ok {
			break
		}
		peeked = append(peeked, b)
	}
	r.peekBuf = peeked
	// 按预读结果决定进入哪种 raw 透传模式；未命中则全部退回按普通标记处理。
	var term []byte
	switch {
	case hasPrefix(peeked, commentIntro[1:]):
		term = commentOutro
	case hasPrefix(peeked, cdataIntro[1:]):
		term = cdataOutro
	case hasPrefix(peeked, piIntro[1:]):
		term = piOutro
	case r.inProlog && hasPrefix(peeked, doctypeKeyword[1:]):
		// 退回 "<!DOCTYPE" 之外预读的字节，让 DOCTYPE 解析器从头消费关键字。
		for i := len(peeked) - 1; i >= len(doctypeKeyword)-1; i-- {
			r.unreadByte(peeked[i])
		}
		peeked = peeked[:len(doctypeKeyword)-1]
		if err := r.produceByte('<'); err != nil {
			return err
		}
		if err := r.produce(peeked); err != nil {
			return err
		}
		return r.parseDoctype()
	}
	for i := len(peeked) - 1; i >= 0; i-- {
		r.unreadByte(peeked[i])
	}
	if err := r.produceByte('<'); err != nil {
		return err
	}
	if term != nil {
		r.state = stateRaw
		r.rawTerm = term
		r.rawMatch = 0
	} else if r.inProlog {
		// 根元素（或其它非 DOCTYPE 标记）出现，序言结束。
		r.inProlog = false
		r.doctypeMatch = 0
	}
	return nil
}

func hasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}

// resolveReference 处理一个完整的 &name; 引用。
func (r *Reader) resolveReference(name string) error {
	if isPredefined(name) {
		// 预定义实体（lt/gt/amp/quot/apos）交给下游 xml 解码器处理。
		return r.produceReference(name)
	}
	ent, ok := r.entities[name]
	if !ok {
		// 未声明实体原样透传，由 encoding/xml 报未定义实体错误。
		return r.produceReference(name)
	}
	// 连续引用（如根文档里的 &a;&a;）时，前一条替换文本的最后一个字节
	// 与新引用的 '&' 之间没有 nextByte 回退，耗尽帧仍留在栈顶。
	// 只有当 '&' 确实来自根文档（ampDepth==0）时才清理；
	// 实体内部的嵌套引用绝不能动外层活动帧。
	if r.ampDepth == 0 {
		r.popExhausted()
	}
	// 递归是确定性的结构错误，优先于一切计数预算：
	// active 精确记录仍在展开路径上的实体，连续引用 &a;&a; 时
	// 前一条帧已在 nextByte 回退时出栈，不会误判；实体内部自引用则命中。
	for _, active := range r.active {
		if active == ent {
			return fmt.Errorf("%w: entity %q", ErrRecursiveEntity, name)
		}
	}
	// 预算 3（结构类）：嵌套深度，即压栈后 active 栈的长度。
	depth := len(r.active) + 1
	if depth > r.limits.MaxNestDepth {
		return &LimitError{
			Limit: LimitNestDepth, Value: int64(depth), Budget: int64(r.limits.MaxNestDepth),
		}
	}

	// 预算 1：全文自定义实体引用总次数。
	r.totalRefs++
	if r.totalRefs > r.limits.MaxTotalEntityRefs {
		return &LimitError{Limit: LimitTotalEntityRefs, Value: r.totalRefs, Budget: r.limits.MaxTotalEntityRefs}
	}
	// 预算 2：单个实体被引用次数。
	r.refsPerEnt[name]++
	if r.refsPerEnt[name] > r.limits.MaxRefsPerEntity {
		return &LimitError{Limit: LimitRefsPerEntity, Value: r.refsPerEnt[name], Budget: r.limits.MaxRefsPerEntity}
	}
	// 压入替换文本帧；下一次 nextByte 即从替换文本读取，实现即时展开。
	r.frames = append(r.frames, frame{ent: ent, data: ent.value})
	r.active = append(r.active, ent)
	return nil
}

// DOCTYPE 与标记识别用到的固定字符串。
var (
	doctypeKeyword = []byte("<!DOCTYPE")
	commentIntro   = []byte("<!--")
	commentOutro   = []byte("-->")
	cdataIntro     = []byte("<![CDATA[")
	cdataOutro     = []byte("]]>")
	piIntro        = []byte("<?")
	piOutro        = []byte("?>")
)

// dtdReader 是 DOCTYPE 解析期间的只读游标。所有消费的字节同时写入 raw，
// 解析完成后由主状态机原样吐回输出流，保持文档字节不丢失。
type dtdReader struct {
	r   *Reader
	raw []byte
}

// next 读取一个字节并记入 raw；超出 DTD 预算时由调用方通过 wrap/fail 感知。
func (d *dtdReader) next() (byte, bool) {
	b, ok := d.r.nextByte()
	if ok {
		d.raw = append(d.raw, b)
	}
	return b, ok
}

// unread 退回最近读取的一个字节，并从 raw 中移除对应记录。
func (d *dtdReader) unread(b byte) {
	d.r.unreadByte(b)
	if n := len(d.raw); n > 0 && d.raw[n-1] == b {
		d.raw = d.raw[:n-1]
	}
}

// overLimit 报告已消费的 DTD 字节是否超出预算。
func (d *dtdReader) overLimit() bool {
	return int64(len(d.raw)) > d.r.limits.MaxDTDBytes
}

func (d *dtdReader) fail() error {
	return &LimitError{Limit: LimitDTDBytes, Value: int64(len(d.raw)), Budget: d.r.limits.MaxDTDBytes}
}

// parseDoctype 消费整个 DOCTYPE 声明、登记内部通用实体，并把原始字节
// 放进 dtdPending 等待原样输出。外部 DTD、参数实体等在此处被拒绝。
func (r *Reader) parseDoctype() error {
	d := &dtdReader{r: r}

	// 跳过根元素名前的空白。
	if err := d.skipSpace(); err != nil {
		return d.wrap(err)
	}
	rootName, err := d.readName()
	if err != nil {
		return d.wrap(err)
	}
	if len(rootName) == 0 {
		return d.wrap(malformed("DOCTYPE missing root element name"))
	}
	if err := d.skipSpace(); err != nil {
		return d.wrap(err)
	}

	// 外部 DTD：SYSTEM/PUBLIC 一律拒绝。
	b, ok := d.next()
	if !ok {
		return d.wrap(io.ErrUnexpectedEOF)
	}
	switch b {
	case '>':
		// 无内部子集，结束。
	case '[':
		if err := d.parseInternalSubset(); err != nil {
			return d.wrap(err)
		}
	default:
		return fmt.Errorf("%w: external DTD reference", ErrExternalEntity)
	}

	if d.overLimit() {
		return d.fail()
	}
	// 缓存关键字之后的原始 DOCTYPE 片段（关键字本身已在渐进匹配时输出），
	// 交由 Read 主循环按预算吐回；不缓冲文档正文。
	if err := r.recordProduced(int64(len(d.raw))); err != nil {
		allowed := r.limits.MaxExpandedBytes - (r.produced - int64(len(d.raw)))
		if allowed < 0 {
			allowed = 0
		}
		r.dtdPending = append(r.dtdPending, d.raw[:allowed]...)
		return err
	}
	r.dtdPending = append(r.dtdPending, d.raw...)
	r.inProlog = false
	return nil
}

func (d *dtdReader) wrap(err error) error {
	if d.overLimit() {
		return d.fail()
	}
	return err
}

func (d *dtdReader) skipSpace() error {
	for {
		b, ok := d.next()
		if !ok {
			return io.ErrUnexpectedEOF
		}
		if !isSpace(b) {
			d.unread(b)
			return nil
		}
	}
}

// readName 读取 XML Name（首字母与后续字符规则略放宽，覆盖 UTF-8 续字节）。
func (d *dtdReader) readName() ([]byte, error) {
	var name []byte
	for {
		b, ok := d.next()
		if !ok {
			if len(name) > 0 {
				return name, io.ErrUnexpectedEOF
			}
			return nil, io.ErrUnexpectedEOF
		}
		if len(name) == 0 && !isNameStart(b) {
			d.unread(b)
			return name, nil
		}
		if len(name) > 0 && !isNameByte(b) {
			d.unread(b)
			return name, nil
		}
		name = append(name, b)
	}
}

// parseInternalSubset 解析 '[' 与 ']' 之间的声明，只接受内部通用实体。
func (d *dtdReader) parseInternalSubset() error {
	for {
		if err := d.skipSpace(); err != nil {
			return err
		}
		b, ok := d.next()
		if !ok {
			return io.ErrUnexpectedEOF
		}
		switch b {
		case ']':
			// 子集结束，跳过空白并要求 '>' 收尾。
			if err := d.skipSpace(); err != nil {
				return err
			}
			close, ok := d.next()
			if !ok {
				return io.ErrUnexpectedEOF
			}
			if close != '>' {
				return malformed("expected '>' to close DOCTYPE")
			}
			return nil
		case '<':
			next, ok := d.next()
			if !ok {
				return io.ErrUnexpectedEOF
			}
			switch next {
			case '?':
				if err := d.skipUntil(piOutro, false); err != nil {
					return err
				}
			case '!':
				third, ok := d.next()
				if !ok {
					return io.ErrUnexpectedEOF
				}
				if third == '-' {
					fourth, ok := d.next()
					if !ok {
						return io.ErrUnexpectedEOF
					}
					if fourth != '-' {
						return malformed("malformed comment in DTD")
					}
					if err := d.skipUntil(commentOutro, false); err != nil {
						return err
					}
				} else {
					// 第三个字节是声明关键字首字母（E/N...），交给 parseDeclaration。
					if err := d.parseDeclaration(third); err != nil {
						return err
					}
				}
			default:
				// 形如 <? 之外的非法构造（'<' 后既不是 '!' 也不是 '?'）。
				return malformed("unexpected byte %q after '<' in internal DTD subset", string(next))
			}
		default:
			// 参数实体引用（%name;）或游离文本都不允许出现在子集声明之间。
			if b == '%' {
				return fmt.Errorf("%w: parameter entity reference in internal subset", ErrExternalEntity)
			}
			return malformed("unexpected byte %q in internal DTD subset", string(b))
		}
	}
}

// parseDeclaration 处理 '<!' 与关键字首字母 first 已被消费的单条标记声明。
func (d *dtdReader) parseDeclaration(first byte) error {
	keyword, err := d.readName()
	if err != nil {
		return err
	}
	keyword = append([]byte{first}, keyword...)
	switch string(keyword) {
	case "ENTITY":
		return d.parseEntity()
	case "ELEMENT", "ATTLIST":
		// 这些声明不参与实体展开，跳过整条声明；其中的参数实体引用同样拒绝。
		return d.skipUntil([]byte(">"), true)
	case "NOTATION":
		return fmt.Errorf("%w: NOTATION declaration", ErrExternalEntity)
	default:
		return malformed("unsupported declaration <!%s", string(keyword))
	}
}

// parseEntity 解析一条 <!ENTITY ...> 声明，只接受内部通用实体。
func (d *dtdReader) parseEntity() error {
	if err := d.skipSpace(); err != nil {
		return err
	}
	// 参数实体声明 <!ENTITY % name ...>：拒绝。
	b, ok := d.next()
	if !ok {
		return io.ErrUnexpectedEOF
	}
	if b == '%' {
		return fmt.Errorf("%w: parameter entity declaration", ErrExternalEntity)
	}
	d.unread(b)

	name, err := d.readName()
	if err != nil {
		return err
	}
	if len(name) == 0 {
		return malformed("ENTITY declaration missing name")
	}
	if err := d.skipSpace(); err != nil {
		return err
	}

	quote, ok := d.next()
	if !ok {
		return io.ErrUnexpectedEOF
	}
	if quote != '"' && quote != '\'' {
		// 内部通用实体必须带引号的替换文本。
		if quote == '>' {
			return malformed("ENTITY %q missing replacement text", string(name))
		}
		// 其余形式（SYSTEM/PUBLIC 外部实体、NDATA 未解析实体）一律按外部实体拒绝。
		return fmt.Errorf("%w: external/unparsed entity %q", ErrExternalEntity, string(name))
	}

	value, err := d.readQuotedValue(quote)
	if err != nil {
		return err
	}
	if err := d.skipSpace(); err != nil {
		return err
	}
	close, ok := d.next()
	if !ok {
		return io.ErrUnexpectedEOF
	}
	if close != '>' {
		// 引号后还有 SYSTEM/NDATA 等内容，属于未解析实体：拒绝。
		return fmt.Errorf("%w: unparsed entity %q", ErrExternalEntity, string(name))
	}

	key := string(name)
	if _, exists := d.r.entities[key]; exists {
		return malformed("duplicate entity declaration %q", key)
	}
	// 复制一份，避免与 d.raw 的底层数组共享。
	stored := make([]byte, len(value))
	copy(stored, value)
	d.r.entities[key] = &entity{name: key, value: stored}
	return nil
}

// readQuotedValue 读取引号内的实体替换文本。参数实体引用（%...;）拒绝。
func (d *dtdReader) readQuotedValue(quote byte) ([]byte, error) {
	var value []byte
	for {
		b, ok := d.next()
		if !ok {
			return nil, io.ErrUnexpectedEOF
		}
		if b == quote {
			return value, nil
		}
		if b == '%' {
			return nil, fmt.Errorf("%w: parameter entity reference inside entity value", ErrExternalEntity)
		}
		value = append(value, b)
	}
}

// skipUntil 消费字节直到完整出现 term。rejectParam 为 true 时，
// 引号外的 '%' 触发参数实体拒绝（ELEMENT/ATTLIST 场景）。
func (d *dtdReader) skipUntil(term []byte, rejectParam bool) error {
	match := 0
	for {
		b, ok := d.next()
		if !ok {
			return io.ErrUnexpectedEOF
		}
		// 参数实体引用无论是否出现在引号内都拒绝（本读取器全面禁止参数实体）。
		if rejectParam && b == '%' {
			return fmt.Errorf("%w: parameter entity reference in declaration", ErrExternalEntity)
		}
		if b == term[match] {
			match++
			if match == len(term) {
				return nil
			}
			continue
		}
		if match > 0 {
			// 与 stepRaw 同样的重叠回退。
			prefix := term[:match]
			match = resync(term, prefix, b)
		}
	}
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n':
		return true
	}
	return false
}

func isNameStart(b byte) bool {
	return b == '_' || b == ':' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		b >= 0x80 // UTF-8 续字节/首字节
}

func isNameByte(b byte) bool {
	return isNameStart(b) ||
		(b >= '0' && b <= '9') ||
		b == '-' || b == '.'
}

func isPredefined(name string) bool {
	switch name {
	case "lt", "gt", "amp", "quot", "apos":
		return true
	}
	return false
}
