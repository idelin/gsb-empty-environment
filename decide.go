package netsec

import (
	"errors"
	"slices"
	"sync"
)

// MaxDetectors 是单个网关判定可并行运行的检测器上限。
const MaxDetectors = 20

// Outcome 是单个检测器已经证明的结论；未知不是已证明结果。
type Outcome int

const (
	// Unknown 表示检测器尚未给出已证明结论（仍在运行或无法判定）。
	Unknown Outcome = iota
	// ProvenAllow 表示检测器已证明应当允许。
	ProvenAllow
	// ProvenDeny 表示检测器已证明应当拒绝。
	ProvenDeny
)

func (o Outcome) String() string {
	switch o {
	case ProvenAllow:
		return "proven-allow"
	case ProvenDeny:
		return "proven-deny"
	default:
		return "unknown"
	}
}

// Verdict 是组合器对当前请求的最终结论。
type Verdict int

const (
	// Pending 表示仍存在可能改变结论的分支，当前不能定案。
	Pending Verdict = iota
	// Allow 表示允许组达到阈值，且拒绝组没有任何命中。
	Allow
	// Deny 表示拒绝，具体原因见 Status.Reason。
	Deny
)

func (v Verdict) String() string {
	switch v {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	default:
		return "pending"
	}
}

// Reason 说明拒绝结论是如何被锁定的，允许与未定案结论没有原因。
type Reason int

const (
	// NoReason 对应未定案或允许结论。
	NoReason Reason = iota
	// DenyHit 表示拒绝组已有任一检测器命中已证明拒绝。
	DenyHit
	// AllowUnreachable 表示允许组剩余可能票数不足以达到阈值，
	// 默认安全拒绝（fail-closed）。
	AllowUnreachable
)

func (r Reason) String() string {
	switch r {
	case DenyHit:
		return "deny-hit"
	case AllowUnreachable:
		return "allow-unreachable"
	default:
		return "none"
	}
}

var (
	// ErrTooManyDetectors 在检测器数量超过 MaxDetectors 时返回。
	ErrTooManyDetectors = errors.New("netsec: too many detectors")
	// ErrEmptyGroup 在允许组或拒绝组为空时返回。
	ErrEmptyGroup = errors.New("netsec: allow and deny groups must both be non-empty")
	// ErrBadThreshold 在允许阈值超出允许组规模或小于 1 时返回。
	ErrBadThreshold = errors.New("netsec: allow threshold must be in [1, len(allow group)]")
	// ErrUnknownDetector 在向未注册的检测器提交结果时返回。
	ErrUnknownDetector = errors.New("netsec: unknown detector id")
	// ErrCancelledDetector 在向已结束或已取消的检测器提交结果时返回。
	ErrCancelledDetector = errors.New("netsec: detector already finished or cancelled")
	// ErrBadOutcome 在提交的结果不是 Unknown/ProvenAllow/ProvenDeny 时返回。
	ErrBadOutcome = errors.New("netsec: invalid outcome")
)

// Config 描述一次请求的检测器分组与允许阈值。
//
// 允许组与拒绝组必须把全部检测器恰好划分一次：每个 id 出现且只出现一次。
// 拒绝组任一命中即拒绝；允许组需要 AllowThreshold 个已证明允许才放行。
type Config struct {
	AllowGroup     []int
	DenyGroup      []int
	AllowThreshold int
}

// Snapshot 是组合器当前状态的不可变拷贝，便于在锁外做穷举核对。
type Snapshot struct {
	// Outcome[id] 是检测器 id 已证明的结论，未知用 Unknown 表示。
	Outcome []Outcome
	// Finished[id] 为 true 表示该检测器已经结束：给出已证明结果或被取消。
	Finished []bool
	// Cancelled 记录已被提前取消的检测器 id。
	Cancelled []int
}

// Status 是一次 [Combiner.Evaluate] 的判定结果。
type Status struct {
	// Verdict 是当前结论；Pending 表示尚不能定案。
	Verdict Verdict
	// Reason 仅在 Verdict 为 Deny 时有意义。
	Reason Reason
	// Evidence 是支撑当前结论的最小决定证据，按检测器 id 升序。
	// Pending 时为 nil。
	Evidence []int
	// Relevant 是仍可能改变结论、必须等待结束的未结束检测器。
	Relevant []int
	// Irrelevant 是结论已锁定后可立即取消的未结束检测器。
	Irrelevant []int
}

// Combiner 在并行检测器返回结果的过程中增量组合结论。
// 多个 goroutine 可并发提交结果与读取判定。
type Combiner struct {
	mu sync.Mutex

	n         int
	threshold int

	inAllow   []bool
	inDeny    []bool
	outcome   []Outcome
	finished  []bool
	cancelled []bool
}

// New 按配置构造组合器并校验分组与阈值。
func New(cfg Config) (*Combiner, error) {
	n := len(cfg.AllowGroup) + len(cfg.DenyGroup)
	switch {
	case n > MaxDetectors:
		return nil, ErrTooManyDetectors
	case len(cfg.AllowGroup) == 0 || len(cfg.DenyGroup) == 0:
		return nil, ErrEmptyGroup
	case cfg.AllowThreshold < 1 || cfg.AllowThreshold > len(cfg.AllowGroup):
		return nil, ErrBadThreshold
	}

	inAllow := make([]bool, n)
	inDeny := make([]bool, n)
	for _, id := range cfg.AllowGroup {
		if id < 0 || id >= n || inAllow[id] || inDeny[id] {
			return nil, ErrUnknownDetector
		}
		inAllow[id] = true
	}
	for _, id := range cfg.DenyGroup {
		if id < 0 || id >= n || inAllow[id] || inDeny[id] {
			return nil, ErrUnknownDetector
		}
		inDeny[id] = true
	}

	return &Combiner{
		n:         n,
		threshold: cfg.AllowThreshold,
		inAllow:   inAllow,
		inDeny:    inDeny,
		outcome:   make([]Outcome, n),
		finished:  make([]bool, n),
		cancelled: make([]bool, n),
	}, nil
}

// Submit 记录检测器 id 的已证明结果。提交 Unknown 等价于标记该检测器在无法
// 判定的情况下结束。对已结束（含已取消）的检测器提交会返回错误。
func (c *Combiner) Submit(id int, result Outcome) error {
	if result < Unknown || result > ProvenDeny {
		return ErrBadOutcome
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if id < 0 || id >= c.n {
		return ErrUnknownDetector
	}
	if c.finished[id] {
		return ErrCancelledDetector
	}
	c.outcome[id] = result
	c.finished[id] = true
	return nil
}

// Snapshot 复制当前组合状态，供锁外穷举核对使用。
func (c *Combiner) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	cancelled := make([]int, 0)
	for id := range c.cancelled {
		if c.cancelled[id] {
			cancelled = append(cancelled, id)
		}
	}
	return Snapshot{
		Outcome:   slices.Clone(c.outcome),
		Finished:  slices.Clone(c.finished),
		Cancelled: cancelled,
	}
}

// Evaluate 计算当前结论。
//
// 结论已被当前证据锁定时，返回非 Pending 的结论与最小决定证据，并把不再可能
// 改变结论的未结束检测器列入 Status.Irrelevant；这些检测器会被标记为已取消，
// 之后对它们的 Submit 将返回 ErrCancelledDetector。仍可能改变结论的检测器列入
// Status.Relevant，调用方必须等待它们全部结束后才能定案。
func (c *Combiner) Evaluate() Status {
	c.mu.Lock()

	snap := Snapshot{
		Outcome:  slices.Clone(c.outcome),
		Finished: slices.Clone(c.finished),
	}
	cancelled := make([]int, 0)
	for id := range c.cancelled {
		if c.cancelled[id] {
			cancelled = append(cancelled, id)
		}
	}
	snap.Cancelled = cancelled

	status := evaluate(snap, c.inAllow, c.inDeny, c.threshold)

	for _, id := range status.Irrelevant {
		c.cancelled[id] = true
		c.finished[id] = true
	}
	c.mu.Unlock()
	return status
}
