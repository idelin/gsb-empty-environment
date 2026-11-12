package netsec

// Kind 区分认证事件的方向。
type Kind int

const (
	// KindUnknown 是零值，Analyze 会拒绝该取值。
	KindUnknown Kind = iota
	// KindBegin 是开始（注入）事件。
	KindBegin
	// KindEnd 是结束（消费）事件。
	KindEnd
)

// Event 是一个 begin/end 认证事件。
//
// begin 与 end 都携带三个关联字段：
//   - Subject：认证主体；
//   - SessionNonce：会话随机数，用于区分同一主体的并发会话；
//   - Operation：操作摘要。
//
// 事件的时间先后由它们在传入切片中的位置决定：下标更小的事件
// 发生得更早。包不依赖任何墙钟时间，因此宿主可以直接按抓包/日志
// 顺序喂入事件。
type Event struct {
	Kind         Kind
	Subject      string
	SessionNonce string
	Operation    string
}

// Pair 表示一次合法的 begin→end 配对，取值为事件在输入切片中的下标。
type Pair struct {
	// Begin 是被消费的 begin 事件下标。
	Begin int
	// End 是消费该 begin 的 end 事件下标。
	End int
}

// HallWitness 是 Hall 型缺口证据。
//
// 当不存在覆盖全部 end 的合法匹配时，存在一个 end 子集 Ends，其所有
// 可匹配 begin 邻居的数量严格小于子集大小：
//
//	len(NeighborBegins) < len(Ends)
//
// 这个缺口是注入端 begin 不足（或被时序/字段约束排除）的证书化证据，
// 宿主可直接将其写入验证报告。Ends 与 NeighborBegins 中的元素均为
// 输入事件切片的下标。
type HallWitness struct {
	Ends           []int
	NeighborBegins []int
}

// Report 是一次匹配分析的结果。
type Report struct {
	// Feasible 为 true 当且仅当每个 end 都能匹配到一个更早且未被
	// 其他 end 使用的 begin。
	Feasible bool
	// Matching 是一个完整合法匹配（覆盖全部 end）；Feasible 为
	// false 时为 nil。
	Matching []Pair
	// UnmatchedBegins 是未被任何 end 消费的 begin 下标，对应协议
	// 允许的“部分未结束”会话。
	UnmatchedBegins []int
	// Witness 是 Feasible 为 false 时的 Hall 型缺口证据，否则为 nil。
	Witness *HallWitness
}

// compatible 判断 begin 事件 b 与 end 事件 e 是否满足字段关联条件。
// 时序条件（begin 更早）在调用处按下标另行判断。
func compatible(b, e Event) bool {
	return b.Subject == e.Subject &&
		b.SessionNonce == e.SessionNonce &&
		b.Operation == e.Operation
}

// validKind 报告事件类型是否合法。
func validKind(k Kind) bool {
	return k == KindBegin || k == KindEnd
}
