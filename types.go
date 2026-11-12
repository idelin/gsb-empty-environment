package netsec

// Order 描述一个事件参与跨线程同步的方式。
//
// 零值 OrderPlain 表示普通 Go 读写：同一位置的访问保持连贯（coherence），
// 但不产生任何先行发生（happens-before）边。
type Order uint8

const (
	// OrderPlain 是普通读写，不参与发布/获取同步。
	OrderPlain Order = iota
	// OrderRelaxed 是松弛原子访问：保持每位置写序，但不建立先行发生关系。
	OrderRelaxed
	// OrderAcquire 是获取语义的读（或获取屏障）。
	OrderAcquire
	// OrderRelease 是发布语义的写（或发布屏障）。
	OrderRelease
	// OrderAcqRel 同时具备获取与发布语义，例如读改写操作。
	OrderAcqRel
	// OrderSeqCst 兼具获取/发布语义，并在线程内固定顺序。
	OrderSeqCst
)

func (o Order) String() string {
	switch o {
	case OrderPlain:
		return "plain"
	case OrderRelaxed:
		return "relaxed"
	case OrderAcquire:
		return "acquire"
	case OrderRelease:
		return "release"
	case OrderAcqRel:
		return "acqrel"
	case OrderSeqCst:
		return "seqcst"
	default:
		return "unknown"
	}
}

// isAcquireClass 报告该事件是否可作为同步关系中的获取端。
func (o Order) isAcquireClass() bool {
	return o == OrderAcquire || o == OrderAcqRel || o == OrderSeqCst
}

// isReleaseClass 报告该事件是否可作为同步关系中的发布端。
func (o Order) isReleaseClass() bool {
	return o == OrderRelease || o == OrderAcqRel || o == OrderSeqCst
}

// Event 是一个内存访问或屏障事件。
//
// 普通读写与屏障合计每线程最多 MaxEventsPerThread 个，线程总数最多
// MaxThreads。屏障事件（IsFence == true）不访问位置，Loc 必须为空；
// 其余事件必须给出非空 Loc。
type Event struct {
	// Thread 是线程编号，从 0 开始连续编号；初始写使用 -1。
	Thread int
	// Index 由 ProgramBuilder 自动填写，是事件在线程内的序号（从 0 开始）。
	Index int

	// Loc 是被访问的内存位置（例如变量名），屏障事件留空。
	Loc string
	// IsWrite 为 true 表示写，false 表示读。
	IsWrite bool
	// IsFence 为 true 表示屏障事件。
	IsFence bool

	// Order 给出内存序；零值为普通读写。
	Order Order
	// Value 是写入的值；读事件忽略该字段，读到的值由读自边推出。
	Value int
}

func (e Event) kind() string {
	switch {
	case e.IsFence:
		return "fence(" + e.Order.String() + ")"
	case e.IsWrite:
		return "W" + e.Order.String()
	default:
		return "R" + e.Order.String()
	}
}
