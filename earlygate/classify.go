package earlygate

// IdentityRequirement 描述操作对握手所建立身份的依赖程度。
type IdentityRequirement int

const (
	// IdentityNone 表示操作不依赖握手建立的身份，可参与早期执行资格判定。
	IdentityNone IdentityRequirement = iota
	// IdentityHandshake 表示操作必须在握手确认的连接上执行，早期阶段只能挂起。
	IdentityHandshake
)

func (i IdentityRequirement) String() string {
	switch i {
	case IdentityNone:
		return "IdentityNone"
	case IdentityHandshake:
		return "IdentityHandshake"
	default:
		return "IdentityUnknown"
	}
}

// Classification 是调用方为单个操作提供的不可变分类。
//
// 零值是安全的默认值：ReadOnly 为 false 意味着操作不满足早期执行条件，
// 因此分类缺失时门控会倾向于挂起而非提前执行。
type Classification struct {
	// ReadOnly 表示操作无副作用（只读）。
	ReadOnly bool
	// Replayable 表示操作可安全重放，重复执行不会产生额外副作用。
	Replayable bool
	// Identity 表示操作对握手身份的需求。
	Identity IdentityRequirement
}

// AllowsEarlyData 报告该分类是否满足“可重放只读且不依赖握手身份”的
// 早期执行条件。
func (c Classification) AllowsEarlyData() bool {
	return c.ReadOnly && c.Replayable && c.Identity == IdentityNone
}

// Classifier 由调用方实现，把操作映射为不可变分类。
//
// 实现应当是确定性的、无副作用的，且可被多个 goroutine 并发调用。
// 返回错误时，门控会直接拒绝对应请求，而不是猜测其分类。
type Classifier[O any] interface {
	Classify(operation O) (Classification, error)
}

// ClassifierFunc 让普通函数满足 [Classifier] 接口。
type ClassifierFunc[O any] func(operation O) (Classification, error)

// Classify 实现 [Classifier]。
func (f ClassifierFunc[O]) Classify(operation O) (Classification, error) {
	return f(operation)
}

// HoldReason 解释操作为何在早期阶段被挂起。
type HoldReason int

const (
	// HoldReasonNone 表示未挂起。
	HoldReasonNone HoldReason = iota
	// HoldReasonMutating 表示操作带副作用或不可安全重放。
	HoldReasonMutating
	// HoldReasonIdentity 表示操作需要握手确认后才可用的身份。
	HoldReasonIdentity
)

func (r HoldReason) String() string {
	switch r {
	case HoldReasonNone:
		return "HoldReasonNone"
	case HoldReasonMutating:
		return "HoldReasonMutating"
	case HoldReasonIdentity:
		return "HoldReasonIdentity"
	default:
		return "HoldReasonUnknown"
	}
}

func holdReasonFor(c Classification) HoldReason {
	if !c.ReadOnly || !c.Replayable {
		return HoldReasonMutating
	}
	return HoldReasonIdentity
}
