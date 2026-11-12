package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

// Decoder limits. They are deliberately fixed rather than configurable so a
// caller cannot accidentally weaken the security boundary.
const (
	MaxDepth = 32
	MaxStack = 10000
	MaxMemo  = 10000

	// Names used in documentation/tests that prefer explicit wording.
	MaxNestingDepth = MaxDepth
	MaxStackItems   = MaxStack
	MaxMemoItems    = MaxMemo
)

const (
	opProto           = 0x80
	opFrame           = 0x95
	opMark            = 0x28
	opStop            = 0x2e
	opNone            = 0x4e
	opBinInt1         = 0x4b
	opBinInt2         = 0x4d
	opBinInt          = 0x4a
	opBinUnicode      = 0x58
	opShortBinUnicode = 0x8c
	opEmptyList       = 0x5d
	opEmptyDict       = 0x7d
	opEmptyTuple      = 0x29
	opAppend          = 0x61
	opAppends         = 0x65
	opSetItem         = 0x73
	opSetItems        = 0x75
	opTuple           = 0x74
	opBinPut          = 0x71
	opBinGet          = 0x68
	opMemoize         = 0x94
)

// Decode parses one complete pickle stream. It supports only protocols 2
// through 5 and only the following opcodes:
//
//	PROTO, FRAME, MARK, STOP, NONE, BININT1, BININT2, BININT,
//	BINUNICODE, SHORT_BINUNICODE, EMPTY_LIST, EMPTY_DICT,
//	EMPTY_TUPLE, APPEND, APPENDS, SETITEM, SETITEMS, TUPLE,
//	BINPUT, BINGET, MEMOIZE
//
// Any other opcode (including GLOBAL, STACK_GLOBAL, REDUCE, and BUILD) fails
// as soon as it is encountered. Decoding also fails for undefined memo
// references, nesting deeper than MaxDepth, stack or memo growth beyond their
// limits, malformed frames, truncated operands, and bytes following STOP.
//
// Decoded values are pure Go data: nil, int64, string, []any, map[any]any, and
// Tuple. Dictionary keys are limited to hashable scalar values (nil, int64,
// and string).
func Decode(data []byte) (any, error) {
	p := parser{
		data:     data,
		frameEnd: -1,
		stack:    make([]stackItem, 0, 16),
		memo:     make([]memoSlot, 0, 16),
	}
	return p.decode()
}

// SafeDecode is an explicit alias for Decode.
func SafeDecode(data []byte) (any, error) {
	return Decode(data)
}

type valueKind uint8

const (
	kindNone valueKind = iota
	kindInt
	kindString
	kindList
	kindDict
	kindTuple
)

type value struct {
	kind valueKind
	num  int64
	str  string
	list *listValue
	dict *dictValue
	tup  *tupleValue
}

type listValue struct {
	items []*value
	depth int
}

type dictEntry struct {
	key   *value
	value *value
}

type dictValue struct {
	entries []dictEntry
	depth   int
}

type tupleValue struct {
	items []*value
	depth int
}

func (v *value) nestingDepth() int {
	switch v.kind {
	case kindList:
		return v.list.depth
	case kindDict:
		return v.dict.depth
	case kindTuple:
		return v.tup.depth
	default:
		return 0
	}
}

func newNone() *value {
	return &value{kind: kindNone}
}

func newInt(n int64) *value {
	return &value{kind: kindInt, num: n}
}

func newString(s string) *value {
	return &value{kind: kindString, str: s}
}

func newList() *value {
	return &value{kind: kindList, list: &listValue{depth: 1}}
}

func newDict() *value {
	return &value{kind: kindDict, dict: &dictValue{depth: 1}}
}

func newTuple(items []*value) (*value, error) {
	depth := 1
	for _, item := range items {
		itemDepth := item.nestingDepth()
		if itemDepth+1 > MaxDepth {
			return nil, ErrDepthLimit
		}
		if itemDepth+1 > depth {
			depth = itemDepth + 1
		}
	}
	return &value{
		kind: kindTuple,
		tup: &tupleValue{
			items: items,
			depth: depth,
		},
	}, nil
}

type stackItem struct {
	mark bool
	val  *value
}

type memoSlot struct {
	defined bool
	val     *value
}

type parser struct {
	data     []byte
	pos      int
	protocol int
	frameEnd int
	stack    []stackItem
	memo     []memoSlot
}

func (p *parser) decode() (any, error) {
	if len(p.data) < 2 {
		return nil, p.fail(0, ErrTruncated)
	}
	if p.data[0] != opProto {
		return nil, p.fail(0, ErrProtocolOrder)
	}

	p.pos = 1
	version, err := p.readByte()
	if err != nil {
		return nil, err
	}
	if version < 2 || version > 5 {
		return nil, p.fail(p.pos-1, fmt.Errorf("%w: version %d", ErrProtocol, version))
	}
	p.protocol = int(version)

	for {
		if p.frameEnd >= 0 && p.pos == p.frameEnd {
			p.frameEnd = -1
		}
		if p.frameEnd >= 0 && p.pos > p.frameEnd {
			return nil, p.fail(p.pos, ErrFrame)
		}

		offset := p.pos
		op, err := p.readByte()
		if err != nil {
			return nil, err
		}

		switch op {
		case opProto:
			return nil, p.fail(offset, ErrProtocolOrder)

		case opFrame:
			if p.frameEnd >= 0 {
				return nil, p.fail(offset, fmt.Errorf("%w: nested or misplaced frame", ErrFrame))
			}
			raw, err := p.readBytes(8)
			if err != nil {
				return nil, err
			}
			size := binary.LittleEndian.Uint64(raw)
			end := uint64(p.pos) + size
			if end > uint64(len(p.data)) {
				return nil, p.fail(offset, fmt.Errorf("%w: frame extends past input", ErrFrame))
			}
			p.frameEnd = int(end)

		case opMark:
			if err := p.pushMark(); err != nil {
				return nil, p.fail(offset, err)
			}

		case opStop:
			if len(p.stack) == 0 {
				return nil, p.fail(offset, fmt.Errorf("%w: STOP has no value", ErrStack))
			}
			top := p.stack[len(p.stack)-1]
			if top.mark {
				return nil, p.fail(offset, fmt.Errorf("%w: STOP on MARK", ErrStack))
			}
			if p.pos != len(p.data) {
				return nil, p.fail(p.pos, ErrTrailingData)
			}
			return exportValue(top.val)

		case opNone:
			if err := p.push(newNone()); err != nil {
				return nil, p.fail(offset, err)
			}

		case opBinInt1:
			raw, err := p.readByte()
			if err != nil {
				return nil, err
			}
			if err := p.push(newInt(int64(raw))); err != nil {
				return nil, p.fail(offset, err)
			}

		case opBinInt2:
			raw, err := p.readBytes(2)
			if err != nil {
				return nil, err
			}
			if err := p.push(newInt(int64(binary.LittleEndian.Uint16(raw)))); err != nil {
				return nil, p.fail(offset, err)
			}

		case opBinInt:
			raw, err := p.readBytes(4)
			if err != nil {
				return nil, err
			}
			n := int64(int32(binary.LittleEndian.Uint32(raw)))
			if err := p.push(newInt(n)); err != nil {
				return nil, p.fail(offset, err)
			}

		case opBinUnicode:
			raw, err := p.readBytes(4)
			if err != nil {
				return nil, err
			}
			if err := p.pushUnicode(raw, offset); err != nil {
				return nil, err
			}

		case opShortBinUnicode:
			raw, err := p.readByte()
			if err != nil {
				return nil, err
			}
			if err := p.pushUnicode([]byte{raw}, offset); err != nil {
				return nil, err
			}

		case opEmptyList:
			if err := p.push(newList()); err != nil {
				return nil, p.fail(offset, err)
			}

		case opEmptyDict:
			if err := p.push(newDict()); err != nil {
				return nil, p.fail(offset, err)
			}

		case opEmptyTuple:
			tuple, err := newTuple(nil)
			if err != nil {
				return nil, p.fail(offset, err)
			}
			if err := p.push(tuple); err != nil {
				return nil, p.fail(offset, err)
			}

		case opAppend:
			if err := p.appendOne(); err != nil {
				return nil, p.fail(offset, err)
			}

		case opAppends:
			if err := p.appendMany(); err != nil {
				return nil, p.fail(offset, err)
			}

		case opSetItem:
			if err := p.setItem(); err != nil {
				return nil, p.fail(offset, err)
			}

		case opSetItems:
			if err := p.setItems(); err != nil {
				return nil, p.fail(offset, err)
			}

		case opTuple:
			if err := p.makeTuple(); err != nil {
				return nil, p.fail(offset, err)
			}

		case opBinPut:
			idx, err := p.readByte()
			if err != nil {
				return nil, err
			}
			if err := p.memoPut(int(idx)); err != nil {
				return nil, p.fail(offset, err)
			}

		case opBinGet:
			idx, err := p.readByte()
			if err != nil {
				return nil, err
			}
			if err := p.memoGet(int(idx)); err != nil {
				return nil, p.fail(offset, err)
			}

		case opMemoize:
			if err := p.memoize(); err != nil {
				return nil, p.fail(offset, err)
			}

		default:
			return nil, p.fail(offset, fmt.Errorf("%w 0x%02x", ErrOpcode, op))
		}
	}
}

func (p *parser) pushUnicode(lengthRaw []byte, offset int) error {
	var n int
	if len(lengthRaw) == 1 {
		n = int(lengthRaw[0])
	} else {
		n = int(binary.LittleEndian.Uint32(lengthRaw))
	}
	raw, err := p.readBytes(n)
	if err != nil {
		return err
	}
	if !utf8.Valid(raw) {
		return p.fail(offset, ErrInvalidUnicode)
	}
	if err := p.push(newString(string(raw))); err != nil {
		return p.fail(offset, err)
	}
	return nil
}

func (p *parser) push(v *value) error {
	if len(p.stack) >= MaxStack {
		return ErrStackLimit
	}
	p.stack = append(p.stack, stackItem{val: v})
	return nil
}

func (p *parser) pushMark() error {
	if len(p.stack) >= MaxStack {
		return ErrStackLimit
	}
	p.stack = append(p.stack, stackItem{mark: true})
	return nil
}

func (p *parser) pop() (stackItem, bool) {
	if len(p.stack) == 0 {
		return stackItem{}, false
	}
	idx := len(p.stack) - 1
	item := p.stack[idx]
	p.stack[idx] = stackItem{}
	p.stack = p.stack[:idx]
	return item, true
}

func (p *parser) lastMark() int {
	for i := len(p.stack) - 1; i >= 0; i-- {
		if p.stack[i].mark {
			return i
		}
	}
	return -1
}

func (p *parser) appendOne() error {
	item, ok := p.pop()
	if !ok || item.mark {
		return fmt.Errorf("%w: APPEND needs a value", ErrStack)
	}
	if len(p.stack) == 0 {
		return fmt.Errorf("%w: APPEND needs a list", ErrStack)
	}
	target := p.stack[len(p.stack)-1]
	if target.mark || target.val.kind != kindList {
		return fmt.Errorf("%w: APPEND target is not a list", ErrStack)
	}
	return appendToList(target.val, item.val)
}

func (p *parser) appendMany() error {
	mark := p.lastMark()
	if mark < 0 {
		return fmt.Errorf("%w: APPENDS requires MARK", ErrStack)
	}
	if mark == 0 {
		return fmt.Errorf("%w: APPENDS has no list below MARK", ErrStack)
	}
	target := p.stack[mark-1]
	if target.mark || target.val.kind != kindList {
		return fmt.Errorf("%w: APPENDS target is not a list", ErrStack)
	}

	items := p.stack[mark+1:]
	for _, item := range items {
		if item.mark {
			return fmt.Errorf("%w: unexpected MARK", ErrStack)
		}
		if err := appendToList(target.val, item.val); err != nil {
			return err
		}
	}
	p.clearFrom(mark)
	return nil
}

func (p *parser) setItem() error {
	val, ok := p.pop()
	if !ok || val.mark {
		return fmt.Errorf("%w: SETITEM needs a value", ErrStack)
	}
	key, ok := p.pop()
	if !ok || key.mark {
		return fmt.Errorf("%w: SETITEM needs a key", ErrStack)
	}
	if len(p.stack) == 0 {
		return fmt.Errorf("%w: SETITEM needs a dictionary", ErrStack)
	}
	target := p.stack[len(p.stack)-1]
	if target.mark || target.val.kind != kindDict {
		return fmt.Errorf("%w: SETITEM target is not a dictionary", ErrStack)
	}
	return setDictItem(target.val, key.val, val.val)
}

func (p *parser) setItems() error {
	mark := p.lastMark()
	if mark < 0 {
		return fmt.Errorf("%w: SETITEMS requires MARK", ErrStack)
	}
	if mark == 0 {
		return fmt.Errorf("%w: SETITEMS has no dictionary below MARK", ErrStack)
	}
	target := p.stack[mark-1]
	if target.mark || target.val.kind != kindDict {
		return fmt.Errorf("%w: SETITEMS target is not a dictionary", ErrStack)
	}

	items := p.stack[mark+1:]
	if len(items)%2 != 0 {
		return fmt.Errorf("%w: SETITEMS has an odd number of items", ErrStack)
	}
	for i := 0; i < len(items); i += 2 {
		key := items[i]
		val := items[i+1]
		if key.mark || val.mark {
			return fmt.Errorf("%w: unexpected MARK", ErrStack)
		}
		if err := setDictItem(target.val, key.val, val.val); err != nil {
			return err
		}
	}
	p.clearFrom(mark)
	return nil
}

func (p *parser) makeTuple() error {
	mark := p.lastMark()
	if mark < 0 {
		return fmt.Errorf("%w: TUPLE requires MARK", ErrStack)
	}
	items := p.stack[mark+1:]
	values := make([]*value, len(items))
	for i, item := range items {
		if item.mark {
			return fmt.Errorf("%w: unexpected MARK", ErrStack)
		}
		values[i] = item.val
	}
	tuple, err := newTuple(values)
	if err != nil {
		return err
	}
	p.clearFrom(mark)
	return p.push(tuple)
}

func (p *parser) memoPut(idx int) error {
	if idx < 0 || idx >= MaxMemo {
		return fmt.Errorf("%w: index %d", ErrMemoLimit, idx)
	}
	if len(p.stack) == 0 || p.stack[len(p.stack)-1].mark {
		return fmt.Errorf("%w: BINPUT needs a value", ErrStack)
	}
	for len(p.memo) <= idx {
		p.memo = append(p.memo, memoSlot{})
	}
	p.memo[idx] = memoSlot{defined: true, val: p.stack[len(p.stack)-1].val}
	return nil
}

func (p *parser) memoGet(idx int) error {
	if idx < 0 || idx >= len(p.memo) || !p.memo[idx].defined {
		return fmt.Errorf("%w: index %d", ErrMemoUndefined, idx)
	}
	return p.push(p.memo[idx].val)
}

func (p *parser) memoize() error {
	if len(p.stack) == 0 || p.stack[len(p.stack)-1].mark {
		return fmt.Errorf("%w: MEMOIZE needs a value", ErrStack)
	}
	if len(p.memo) >= MaxMemo {
		return fmt.Errorf("%w: no free slot", ErrMemoLimit)
	}
	p.memo = append(p.memo, memoSlot{
		defined: true,
		val:     p.stack[len(p.stack)-1].val,
	})
	return nil
}

func (p *parser) clearFrom(idx int) {
	for i := idx; i < len(p.stack); i++ {
		p.stack[i] = stackItem{}
	}
	p.stack = p.stack[:idx]
}

func (p *parser) readByte() (byte, error) {
	raw, err := p.readBytes(1)
	if err != nil {
		return 0, err
	}
	return raw[0], nil
}

func (p *parser) readBytes(n int) ([]byte, error) {
	if n < 0 {
		return nil, p.fail(p.pos, ErrTruncated)
	}
	end := p.pos + n
	if end < p.pos || end > len(p.data) {
		return nil, p.fail(p.pos, ErrTruncated)
	}
	if p.frameEnd >= 0 && end > p.frameEnd {
		return nil, p.fail(p.pos, fmt.Errorf("%w: operand crosses frame boundary", ErrFrame))
	}
	raw := p.data[p.pos:end]
	p.pos = end
	return raw, nil
}

func (p *parser) fail(offset int, err error) error {
	if err == nil {
		return nil
	}
	var de *DecodeError
	if errors.As(err, &de) {
		return err
	}
	return &DecodeError{Offset: offset, Err: err}
}

func appendToList(list *value, item *value) error {
	if containsNode(item, list) {
		return ErrCycle
	}
	nextDepth := item.nestingDepth() + 1
	if nextDepth > MaxDepth {
		return ErrDepthLimit
	}
	list.list.items = append(list.list.items, item)
	if nextDepth > list.list.depth {
		list.list.depth = nextDepth
	}
	return nil
}

func setDictItem(dict *value, key *value, val *value) error {
	if !isDictKey(key) {
		return ErrDictKey
	}
	if containsNode(key, dict) || containsNode(val, dict) {
		return ErrCycle
	}
	for i := range dict.dict.entries {
		if sameScalar(dict.dict.entries[i].key, key) {
			dict.dict.entries[i].value = val
			refreshDictDepth(dict.dict)
			if dict.dict.depth > MaxDepth {
				return ErrDepthLimit
			}
			return nil
		}
	}
	dict.dict.entries = append(dict.dict.entries, dictEntry{key: key, value: val})
	refreshDictDepth(dict.dict)
	if dict.dict.depth > MaxDepth {
		return ErrDepthLimit
	}
	return nil
}

func isDictKey(v *value) bool {
	switch v.kind {
	case kindNone, kindInt, kindString:
		return true
	default:
		return false
	}
}

func sameScalar(a, b *value) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case kindNone:
		return true
	case kindInt:
		return a.num == b.num
	case kindString:
		return a.str == b.str
	default:
		return false
	}
}

func refreshDictDepth(dict *dictValue) {
	depth := 1
	for _, entry := range dict.entries {
		candidate := entry.value.nestingDepth() + 1
		if entry.key.nestingDepth()+1 > candidate {
			candidate = entry.key.nestingDepth() + 1
		}
		if candidate > depth {
			depth = candidate
		}
	}
	dict.depth = depth
}

func containsNode(root *value, target *value) bool {
	seen := make(map[*value]struct{})
	pending := []*value{root}
	for len(pending) > 0 {
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]
		if current == nil {
			continue
		}
		if _, ok := seen[current]; ok {
			continue
		}
		seen[current] = struct{}{}

		if sameContainer(current, target) {
			return true
		}
		switch current.kind {
		case kindList:
			pending = append(pending, current.list.items...)
		case kindDict:
			for _, entry := range current.dict.entries {
				pending = append(pending, entry.key, entry.value)
			}
		case kindTuple:
			pending = append(pending, current.tup.items...)
		}
	}
	return false
}

func sameContainer(a, b *value) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case kindList:
		return a.list == b.list
	case kindDict:
		return a.dict == b.dict
	case kindTuple:
		return a.tup == b.tup
	default:
		return false
	}
}

func exportValue(v *value) (any, error) {
	return exportValueMemo(v, make(map[*value]any))
}

func exportValueMemo(v *value, memo map[*value]any) (any, error) {
	if cached, ok := memo[v]; ok {
		return cached, nil
	}

	switch v.kind {
	case kindNone:
		return nil, nil
	case kindInt:
		return v.num, nil
	case kindString:
		return v.str, nil
	case kindList:
		out := make([]any, len(v.list.items))
		memo[v] = out
		for i, item := range v.list.items {
			decoded, err := exportValueMemo(item, memo)
			if err != nil {
				return nil, err
			}
			out[i] = decoded
		}
		return out, nil
	case kindDict:
		out := make(map[any]any, len(v.dict.entries))
		memo[v] = out
		for _, entry := range v.dict.entries {
			key, err := exportValueMemo(entry.key, memo)
			if err != nil {
				return nil, err
			}
			val, err := exportValueMemo(entry.value, memo)
			if err != nil {
				return nil, err
			}
			out[key] = val
		}
		return out, nil
	case kindTuple:
		out := make(Tuple, len(v.tup.items))
		memo[v] = out
		for i, item := range v.tup.items {
			decoded, err := exportValueMemo(item, memo)
			if err != nil {
				return nil, err
			}
			out[i] = decoded
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: unknown internal value kind", ErrStack)
	}
}
