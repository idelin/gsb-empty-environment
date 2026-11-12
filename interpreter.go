package netsec

import (
	"errors"
	"unicode/utf8"
)

// Event 是独立解释器对单次参数消费的描述。字段语义与 Consumption 对齐，
// 但该结构由独立的状态机产生，用于交叉验证主分析器。
type Event struct {
	Index      int
	Explicit   bool
	Repeated   bool
	Role       Role
	Verb       rune
	Constraint Kind
	Truncating bool
}

// Interpretation 是独立解释器的产物。
type Interpretation struct {
	Events       []Event
	ExplicitMode bool
	// Repeated 为被重复引用的显式位置（零基）。
	Repeated []int
	// Truncating 为发生截断字符串转换的参数位置（零基）。
	Truncating []int
	// ArgCount 为解释器口径下覆盖的参数个数。
	ArgCount int
}

// interp 是与主分析器数据结构完全独立的解释器状态。
type interp struct {
	format string
	end    int
	pos    int

	events     []Event
	repeated   []int
	truncating []int
	seen       map[int]bool
	truncSeen  map[int]bool

	explicitMode bool
	maxIndex     int
	seq          int
}

// Interpret 用独立于 parse 的状态机解释 format，返回逐次消费事件。
// 格式不合法时返回非空错误，同时保留出错前已解释出的事件。
func Interpret(format string) (*Interpretation, error) {
	p := &interp{
		format:    format,
		end:       len(format),
		seen:      map[int]bool{},
		truncSeen: map[int]bool{},
		maxIndex:  -1,
	}
	if err := p.run(); err != nil {
		return p.result(), err
	}
	return p.result(), nil
}

func (p *interp) result() *Interpretation {
	out := &Interpretation{
		Events:       p.events,
		ExplicitMode: p.explicitMode,
		Repeated:     p.repeated,
		Truncating:   p.truncating,
	}
	if p.explicitMode {
		out.ArgCount = p.maxIndex + 1
	} else {
		out.ArgCount = p.seq
	}
	return out
}

func appendUnique(xs []int, v int) []int {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

// argNumber 对应 fmt 的 argNumber：仅在当前位置出现 [n] 时消费索引。
func (p *interp) argNumber() (index int, found bool, err error) {
	idx, wid, ok := parseBracket(p.format, p.pos, p.end)
	if ok {
		if idx < 0 {
			return 0, false, errors.New("显式参数索引必须从 1 开始")
		}
		p.pos += wid
		return idx, true, nil
	}
	if p.pos < p.end && p.format[p.pos] == '[' {
		return 0, false, errors.New("显式参数索引不合法或括号未闭合")
	}
	return -1, false, nil
}

func (p *interp) run() error {
	for p.pos < p.end {
		// 普通文本。
		for p.pos < p.end && p.format[p.pos] != '%' {
			p.pos++
		}
		if p.pos >= p.end {
			return nil
		}
		p.pos++ // 跳过 '%'
		if p.pos >= p.end {
			return errors.New("悬空的百分号：缺少转换说明符")
		}
		if p.format[p.pos] == '%' {
			p.pos++
			continue
		}
		if err := p.spec(); err != nil {
			return err
		}
	}
	return nil
}

// spec 解释单个转换说明符，结构与 fmt.doPrintf 的说明符处理对齐。
func (p *interp) spec() error {
	// 标志位。
	for p.pos < p.end {
		switch p.format[p.pos] {
		case '#', '0', '+', '-', ' ':
			p.pos++
		default:
			goto flagsDone
		}
	}
flagsDone:

	afterIndex := false
	verbIndex, hasVerbIndex := -1, false
	if idx, found, err := p.argNumber(); err != nil {
		return err
	} else if found {
		verbIndex, hasVerbIndex, afterIndex = idx, true, true
	}

	// 宽度。
	widthExplicit, widthStar := -1, false
	switch {
	case p.pos < p.end && p.format[p.pos] == '*':
		if afterIndex {
			// 入口解析到的 [n] 与这个 * 配对，属于动态宽度。
			widthExplicit, hasVerbIndex = verbIndex, false
		}
		p.pos++
		widthStar, afterIndex = true, false
	case p.pos < p.end && p.format[p.pos] == '[':
		idx, found, err := p.argNumber()
		if err != nil {
			return err
		}
		if !found || p.pos >= p.end || p.format[p.pos] != '*' {
			return errors.New("显式宽度索引后缺少 *")
		}
		widthExplicit, widthStar = idx, true
		p.pos++
	default:
		numStart := p.pos
		for p.pos < p.end && p.format[p.pos] >= '0' && p.format[p.pos] <= '9' {
			p.pos++
		}
		if afterIndex && p.pos > numStart {
			return errors.New("显式索引后不能再跟数字宽度")
		}
	}

	// 精度。
	precExplicit, precStar, precPresent := -1, false, false
	if p.pos < p.end && p.format[p.pos] == '.' {
		if afterIndex {
			return errors.New("显式索引后不能直接接精度")
		}
		p.pos++
		precPresent = true
		switch {
		case p.pos < p.end && p.format[p.pos] == '[':
			idx, found, err := p.argNumber()
			if err != nil {
				return err
			}
			if !found || p.pos >= p.end || p.format[p.pos] != '*' {
				return errors.New("显式精度索引后缺少 *")
			}
			precExplicit, precStar = idx, true
			p.pos++
			afterIndex = false
		case p.pos < p.end && p.format[p.pos] == '*':
			p.pos++
			precStar, afterIndex = true, false
		default:
			for p.pos < p.end && p.format[p.pos] >= '0' && p.format[p.pos] <= '9' {
				p.pos++
			}
		}
	}

	// 动词前索引（前面没有索引时）。
	if !afterIndex {
		if idx, found, err := p.argNumber(); err != nil {
			return err
		} else if found {
			verbIndex, hasVerbIndex, afterIndex = idx, true, true
		}
	}

	if p.pos >= p.end {
		return errors.New("转换说明符缺少动词")
	}
	verb, size := utf8.DecodeRuneInString(p.format[p.pos:])
	p.pos += size
	if verb == '%' {
		return nil
	}

	kind := verbConstraint(verb)
	trunc := precPresent && kind == KindString
	if widthStar {
		if widthExplicit >= 0 {
			p.emit(widthExplicit, true, RoleWidth, verb, KindInt, false)
		} else {
			p.emit(p.seq, false, RoleWidth, verb, KindInt, false)
			p.seq++
		}
	}
	if precStar {
		if precExplicit >= 0 {
			p.emit(precExplicit, true, RolePrecision, verb, KindInt, false)
		} else {
			p.emit(p.seq, false, RolePrecision, verb, KindInt, false)
			p.seq++
		}
	}
	if hasVerbIndex {
		p.emit(verbIndex, true, RoleValue, verb, kind, trunc)
	} else {
		p.emit(p.seq, false, RoleValue, verb, kind, trunc)
		p.seq++
	}
	return nil
}

func (p *interp) emit(index int, explicit bool, role Role, verb rune, kind Kind, trunc bool) {
	repeated := false
	if explicit {
		p.explicitMode = true
		repeated = p.seen[index]
		if !p.seen[index] {
			p.seen[index] = true
		} else {
			p.repeated = appendUnique(p.repeated, index)
		}
		if index > p.maxIndex {
			p.maxIndex = index
		}
	}
	if trunc && !p.truncSeen[index] {
		p.truncSeen[index] = true
		p.truncating = append(p.truncating, index)
	}
	p.events = append(p.events, Event{
		Index:      index,
		Explicit:   explicit,
		Repeated:   repeated,
		Role:       role,
		Verb:       verb,
		Constraint: kind,
		Truncating: trunc,
	})
}
