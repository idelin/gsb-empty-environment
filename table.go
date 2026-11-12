package netsec

import (
	"fmt"
	"maps"
)

// 判定表最多建模前两个位置参数。该限制使参数域可以被有限枚举（见 [Domain]），
// 真实宿主可在表外对其余参数做固定处理后再进入判定。
const maxModeledArgs = 2

// Key 标识判定表中的一条系统调用规则：系统调用号加至多两个位置参数。
//
// ArgMask 的第 i 位置 1 表示 Args[i] 参与匹配，为 0 表示该位置参数被忽略。
// 未参与匹配的 Args 位置必须为零值。Key 是可比较类型，可直接作为 map 键。
type Key struct {
	Syscall uint
	ArgMask uint8
	Args    [maxModeledArgs]uint64
}

// NewKey 构造一条规则键。argMask 标记哪些位置参数参与匹配，
// 未参与匹配的位置必须传 0。argMask 超过两参数模型时返回错误。
func NewKey(syscall uint, argMask uint8, args [maxModeledArgs]uint64) (Key, error) {
	if argMask&^(1<<maxModeledArgs-1) != 0 {
		return Key{}, fmt.Errorf("netsec: arg mask %#x exceeds %d modeled arguments", argMask, maxModeledArgs)
	}
	for i := range maxModeledArgs {
		if argMask&(1<<i) == 0 && args[i] != 0 {
			return Key{}, fmt.Errorf("netsec: arg %d is masked out but has nonzero value %d", i, args[i])
		}
	}
	return Key{Syscall: syscall, ArgMask: argMask, Args: args}, nil
}

// matches 报告实际调用键 request 是否命中本规则键：
// 系统调用号相同，且本键要求参与匹配的参数位取值一致。
func (k Key) matches(request Key) bool {
	if k.Syscall != request.Syscall {
		return false
	}
	for i := range maxModeledArgs {
		if k.ArgMask&(1<<i) != 0 && k.Args[i] != request.Args[i] {
			return false
		}
	}
	return true
}

// Decision 是命中规则时返回的处置结果，纯数据、可比较。
type Decision struct {
	// Action 为处置动作，必须是四个受支持动作之一。
	Action Action
	// Errno 在 Deny 时作为返回给调用方的错误码；其余动作下应为 0。
	Errno uint16
	// Signal 在 Kill 时发送的信号；其余动作下应为 0。
	Signal uint16
	// Tag 是宿主自定义的不透明标签（如规则来源），不参与组合优先级。
	Tag string
}

// Table 是调用方提供的纯判定表：规则键到决策的不可变映射。
// 未命中任何规则时使用默认决策（默认为 Allow）。
type Table struct {
	name  string
	rules map[Key]Decision
	def   Decision
}

// NewTable 校验并构造不可变判定表。rules 会被复制，调用方后续修改原 map
// 不会影响已构造的表。非法动作或非法键会返回错误。
func NewTable(name string, rules map[Key]Decision, defaultDecision Decision) (*Table, error) {
	if !defaultDecision.Action.Valid() {
		return nil, fmt.Errorf("netsec: table %q has invalid default action %d", name, defaultDecision.Action)
	}
	copied := make(map[Key]Decision, len(rules))
	for key, decision := range rules {
		if key.ArgMask&^(1<<maxModeledArgs-1) != 0 {
			return nil, fmt.Errorf("netsec: table %q rule has arg mask %#x beyond %d args", name, key.ArgMask, maxModeledArgs)
		}
		if !decision.Action.Valid() {
			return nil, fmt.Errorf("netsec: table %q rule for syscall %d has invalid action %d", name, key.Syscall, decision.Action)
		}
		copied[key] = decision
	}
	return &Table{name: name, rules: copied, def: defaultDecision}, nil
}

// Name 返回判定表名称。
func (t *Table) Name() string { return t.name }

// Lookup 在表内解析一次系统调用请求。命中多条规则时，取 ArgMask 置位数最多
// （即最具体）的规则；置位数相同则取 map 迭代顺序中稳定排序后的第一条，
// 保证同一张表的解析结果确定。第二个返回值报告是否命中显式规则；未命中时
// 返回默认决策且 matched=false，默认决策仅作整链兜底，不与其他表的显式规则竞争。
func (t *Table) Lookup(request Key) (Decision, bool) {
	best, bestBits, found := Decision{}, -1, false
	for key, decision := range t.rules {
		if !key.matches(request) {
			continue
		}
		bits := popcount(key.ArgMask)
		if bits > bestBits {
			best, bestBits, found = decision, bits, true
		}
	}
	if !found {
		return t.def, false
	}
	return best, true
}

// Rules 返回表内规则的快照副本。
func (t *Table) Rules() map[Key]Decision {
	return maps.Clone(t.rules)
}

// Default 返回表的默认决策。
func (t *Table) Default() Decision { return t.def }

func popcount(mask uint8) int {
	count := 0
	for mask != 0 {
		count += int(mask & 1)
		mask >>= 1
	}
	return count
}
