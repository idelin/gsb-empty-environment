package netsec

import (
	"errors"
	"sync"
)

// 成本类别。三类成本分别累计，互不挪用。
const (
	CostInput  = "input"  // 实际从压缩片段读取并消耗的输入字节
	CostOutput = "output" // 解压器实际生成的输出字节
	CostDict   = "dict"   // 字典查找 / 回写 / 装载等字典操作次数
)

// Limits 是单个请求授权的预算上限。某一类别 <= 0 表示该类别不限。
type Limits struct {
	MaxInputBytes  uint64
	MaxOutputBytes uint64
	MaxDictOps     uint64
}

// Usage 是截至当前的累计成本。
type Usage struct {
	InputBytes  uint64
	OutputBytes uint64
	DictOps     uint64
	Attempts    uint64
}

// Cost 是一步解压动作声明的实际成本，由解压器在产出结果后自报。
type Cost struct {
	InputBytes  uint64
	OutputBytes uint64
	DictOps     uint64
}

// ErrBudgetExceeded 用于 errors.Is 判断。
var ErrBudgetExceeded = errors.New("decompression budget exceeded")

// BudgetExceededError 指明是哪一类成本、在第几次尝试（重试）中耗尽预算。
type BudgetExceededError struct {
	Category string
	Limit    uint64
	Used     uint64
	Charge   uint64
	Attempt  uint64
}

func (e *BudgetExceededError) Error() string {
	return "decompression budget exceeded: " + e.Category +
		" charge=" + uitoa(e.Charge) + " used=" + uitoa(e.Used) +
		" limit=" + uitoa(e.Limit) + " attempt=" + uitoa(e.Attempt)
}

func (e *BudgetExceededError) Unwrap() error { return ErrBudgetExceeded }

// Budget 是请求级预算。同一授权请求的全部失败重试共享同一个 Budget 实例，
// 成本持续累计；只有收到新的授权请求（新的 RequestID）时才会清零重置。
//
// 预算按解压器实际消耗的工作量记账，而不是按网络层已接收字节，
// 因此重放一个小的压缩片段也无法逃避累计。
type Budget struct {
	mu        sync.Mutex
	requestID string
	attempts  uint64
	used      Usage
	limits    Limits
}

// NewBudget 创建绑定到首个授权请求的预算。
func NewBudget(requestID string, limits Limits) *Budget {
	b := &Budget{}
	b.Reset(requestID, limits)
	return b
}

// Reset 仅在授权标识变化（新的授权请求）时清零并重新绑定。
// 相同 requestID 的调用不会丢弃累计成本，避免攻击者借“重试”标签刷新预算。
func (b *Budget) Reset(requestID string, limits Limits) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.requestID != "" && b.requestID == requestID {
		return
	}
	b.requestID = requestID
	b.limits = limits
	b.used = Usage{}
	b.attempts = 0
}

// BindRequest 为一次解压调用绑定授权请求，并返回当前尝试序号（从 1 开始）。
// 相同 requestID 表示同一次请求的又一次重试：预算保留，尝试序号 +1。
// 不同 requestID 表示新的授权请求：预算清零后重新开始。
func (b *Budget) BindRequest(requestID string) (attempt uint64, reset bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.requestID != requestID {
		b.requestID = requestID
		b.used = Usage{}
		b.attempts = 0
		reset = true
	}
	b.attempts++
	return b.attempts, reset
}

// Snapshot 返回累计成本的拷贝。
func (b *Budget) Snapshot() Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// Limits 返回当前生效的预算上限。
func (b *Budget) Limits() Limits {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limits
}

// Exhausted 在记账前做预检：任一类别已到达上限即返回对应错误，
// 使宿主可以在不调用解压器（不产生任何 CPU）的情况下直接拒绝下一步。
func (b *Budget) Exhausted(attempt uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.overLimitLocked(attempt)
}

// Charge 原子地对一步解压记账。任一类成本超限则整笔拒绝（全有或全无，
// 已累计值保持不变），由宿主丢弃本次尝试的隔离输出。
func (b *Budget) Charge(c Cost, attempt uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	newInput := b.used.InputBytes + c.InputBytes
	newOutput := b.used.OutputBytes + c.OutputBytes
	newDict := b.used.DictOps + c.DictOps

	if b.limits.MaxInputBytes > 0 && newInput > b.limits.MaxInputBytes {
		return &BudgetExceededError{
			Category: CostInput, Limit: b.limits.MaxInputBytes,
			Used: b.used.InputBytes, Charge: c.InputBytes, Attempt: attempt,
		}
	}
	if b.limits.MaxOutputBytes > 0 && newOutput > b.limits.MaxOutputBytes {
		return &BudgetExceededError{
			Category: CostOutput, Limit: b.limits.MaxOutputBytes,
			Used: b.used.OutputBytes, Charge: c.OutputBytes, Attempt: attempt,
		}
	}
	if b.limits.MaxDictOps > 0 && newDict > b.limits.MaxDictOps {
		return &BudgetExceededError{
			Category: CostDict, Limit: b.limits.MaxDictOps,
			Used: b.used.DictOps, Charge: c.DictOps, Attempt: attempt,
		}
	}

	b.used.InputBytes = newInput
	b.used.OutputBytes = newOutput
	b.used.DictOps = newDict
	b.used.Attempts = attempt
	return nil
}

func (b *Budget) overLimitLocked(attempt uint64) error {
	if b.limits.MaxInputBytes > 0 && b.used.InputBytes >= b.limits.MaxInputBytes {
		return &BudgetExceededError{
			Category: CostInput, Limit: b.limits.MaxInputBytes,
			Used: b.used.InputBytes, Attempt: attempt,
		}
	}
	if b.limits.MaxOutputBytes > 0 && b.used.OutputBytes >= b.limits.MaxOutputBytes {
		return &BudgetExceededError{
			Category: CostOutput, Limit: b.limits.MaxOutputBytes,
			Used: b.used.OutputBytes, Attempt: attempt,
		}
	}
	if b.limits.MaxDictOps > 0 && b.used.DictOps >= b.limits.MaxDictOps {
		return &BudgetExceededError{
			Category: CostDict, Limit: b.limits.MaxDictOps,
			Used: b.used.DictOps, Attempt: attempt,
		}
	}
	return nil
}

func uitoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
