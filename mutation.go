package taskpkg

import "errors"

// MutationKind 标识候选变异的类型。
type MutationKind int

const (
	// KindInsert 是在偏移 At 处插入 Payload（常用于重复字段插入）。
	KindInsert MutationKind = iota
	// KindReplace 是把原始字节区间 [At, At+len(Old)) 替换为 Payload
	// （常用于分隔符/换行序列替换）。
	KindReplace
)

// Candidate 是一个候选变异：重复字段插入或分隔符替换。
//
// 一次搜索至多提供 12 个候选。所有偏移都相对于未变异的原始请求字节。
type Candidate struct {
	// Kind 为 KindInsert 或 KindReplace。
	Kind MutationKind
	// At 是插入位置或被替换区间的起始偏移（相对原始字节）。
	At int
	// Old 仅 KindReplace 使用，必须与原始字节 [At, At+len(Old)) 完全一致。
	Old []byte
	// Payload 是插入内容或替换后的新内容（可为空，表示删除式替换）。
	Payload []byte
	// Note 供宿主标注该候选的语义（例如 "duplicate file field"），原样回传。
	Note string
}

// ErrTooManyCandidates 在候选数量超过 12 时返回。
var ErrTooManyCandidates = errors.New("taskpkg: at most 12 mutation candidates are supported")

type event struct {
	kind    MutationKind
	at      int
	payload []byte
	old     []byte
	idx     int
}

// ApplyCandidates 按候选索引集合把变异应用到原始字节 base，返回新的独立字节切片。
//
// 输出切片始终是新分配的，不会修改或别名引用 base。替换区间两两不得重叠；插入位置
// 不得落在任何替换区间内部（落在替换边界上是允许的）。同一位置的多个插入按候选索引
// 升序依次写入。
func ApplyCandidates(base []byte, candidates []Candidate, picks []int) ([]byte, error) {
	if len(candidates) > 12 {
		return nil, ErrTooManyCandidates
	}
	events := make([]event, 0, len(picks))
	for _, idx := range picks {
		if idx < 0 || idx >= len(candidates) {
			return nil, &CandidateIndexError{Index: idx, Count: len(candidates)}
		}
		c := candidates[idx]
		events = append(events, event{
			kind: c.Kind, at: c.At, payload: c.Payload, old: c.Old, idx: idx,
		})
	}

	if err := validateEvents(base, events); err != nil {
		return nil, err
	}
	if err := validateRelations(events); err != nil {
		return nil, err
	}

	// 事件排序：按原始偏移；同偏移时替换先于插入，插入再按候选索引升序。
	for i := 1; i < len(events); i++ {
		for j := i; j > 0; j-- {
			a, b := events[j-1], events[j]
			less := b.at < a.at ||
				(b.at == a.at && b.kind < a.kind) ||
				(b.at == a.at && b.kind == a.kind && b.idx < a.idx)
			if !less {
				break
			}
			events[j-1], events[j] = b, a
		}
	}

	size := len(base)
	for _, ev := range events {
		if ev.kind == KindInsert {
			size += len(ev.payload)
		} else {
			size += len(ev.payload) - len(ev.old)
		}
	}

	out := make([]byte, 0, size)
	cursor := 0
	for _, ev := range events {
		if ev.at < cursor {
			return nil, &CandidateError{Index: ev.idx, Message: "overlapping mutation range"}
		}
		out = append(out, base[cursor:ev.at]...)
		if ev.kind == KindInsert {
			out = append(out, ev.payload...)
			cursor = ev.at
		} else {
			out = append(out, ev.payload...)
			cursor = ev.at + len(ev.old)
		}
	}
	out = append(out, base[cursor:]...)
	return out, nil
}

// ValidateCandidates 校验候选表对 base 是否可应用。
//
// 校验内容：数量不超过 12；偏移位于 base 范围内；替换区间合法且与 base 内容一致；
// 替换区间两两不重叠；插入位置不落在替换区间内部。
func ValidateCandidates(base []byte, candidates []Candidate) error {
	if len(candidates) > 12 {
		return ErrTooManyCandidates
	}
	events := make([]event, 0, len(candidates))
	for idx, c := range candidates {
		if c.At < 0 || c.At > len(base) {
			return &CandidateError{Index: idx, Message: "offset out of range"}
		}
		switch c.Kind {
		case KindInsert:
		case KindReplace:
			if c.At+len(c.Old) > len(base) {
				return &CandidateError{Index: idx, Message: "replacement range out of range"}
			}
			if !bytesEqual(base[c.At:c.At+len(c.Old)], c.Old) {
				return &CandidateError{Index: idx, Message: "Old does not match base bytes"}
			}
		default:
			return &CandidateError{Index: idx, Message: "unknown mutation kind"}
		}
		events = append(events, event{kind: c.Kind, at: c.At, old: c.Old, idx: idx})
	}
	return validateRelations(events)
}

func validateRelations(events []event) error {
	for i := 0; i < len(events); i++ {
		for j := i + 1; j < len(events); j++ {
			a, b := events[i], events[j]
			if a.kind == KindReplace && b.kind == KindReplace {
				as, ae := a.at, a.at+len(a.old)
				bs, be := b.at, b.at+len(b.old)
				if as < be && bs < ae {
					return &CandidateError{Index: b.idx, Message: "overlapping replacement ranges"}
				}
			}
			var rep, ins event
			if a.kind == KindReplace {
				rep, ins = a, b
			} else if b.kind == KindReplace {
				rep, ins = b, a
			} else {
				continue
			}
			if ins.kind != KindInsert {
				continue
			}
			if ins.at > rep.at && ins.at < rep.at+len(rep.old) {
				return &CandidateError{Index: ins.idx, Message: "insertion lies inside a replacement range"}
			}
		}
	}
	return nil
}

func validateEvents(base []byte, events []event) error {
	for _, ev := range events {
		if ev.at < 0 || ev.at > len(base) {
			return &CandidateError{Index: ev.idx, Message: "offset out of range"}
		}
		if ev.kind == KindReplace {
			if ev.at+len(ev.old) > len(base) {
				return &CandidateError{Index: ev.idx, Message: "replacement range out of range"}
			}
			if !bytesEqual(base[ev.at:ev.at+len(ev.old)], ev.old) {
				return &CandidateError{Index: ev.idx, Message: "Old does not match base bytes"}
			}
		}
	}
	return nil
}

// CandidateError 表示某个候选自身非法（偏移越界、内容不匹配、区间重叠等）。
type CandidateError struct {
	Index   int
	Message string
}

func (e *CandidateError) Error() string {
	return "taskpkg: candidate " + itoa(e.Index) + ": " + e.Message
}

// CandidateIndexError 表示 picks 引用了不存在的候选索引。
type CandidateIndexError struct {
	Index int
	Count int
}

func (e *CandidateIndexError) Error() string {
	return "taskpkg: candidate index " + itoa(e.Index) +
		" out of range for " + itoa(e.Count) + " candidates"
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
