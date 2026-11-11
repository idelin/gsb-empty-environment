package netsec

// 握手协议模型的时钟索引。
const (
	ClockRetransmit = 0 // r：重传计时器
	ClockTimeout    = 1 // t：握手超时计时器
	ClockPolicy     = 2 // z：授权窗口（过期计时器）
)

// 位置索引。
const (
	LocIdle = iota
	LocWaiting
	LocAuthorized
	LocOverrun
)

// 模型常数：均不超过十。
const (
	RetransmitPeriod = 5 // 重传周期 r == 5
	TimeoutLimit     = 8 // 握手超时 t > 8
	PolicyWindow     = 7 // 授权窗口边界 z ~ 7
)

// 模型事件名。
const (
	EventRequest    = "send_request"
	EventDupAck     = "dup_ack"
	EventTimeout    = "timeout_abort"
	EventUseGood    = "use_resource"
	EventUseExpired = "use_expired_resource"
	EventIdle       = "idle_retry"
)

// HandshakeMachine 构造握手协议定时状态机。
//
// 时钟语义：
//   - r 为重传计时器，每次重传归零；
//   - t 为握手超时计时器，请求发出后不再归零；
//   - z 为授权窗口计时器，进入授权位置后开始计时。
//
// strictPolicy 为真时授权窗口是严格的 z < 7，否则为非严格 z <= 7。
// 好/坏两条资源访问迁移在整数边界 z=7 上行为相反：
//   - 非严格策略：z<=7 可正常使用，z>7 触发超期；
//   - 严格策略：z<7 可正常使用，z>=7 即超期。
//
// 授权窗口不放进位置不变量，否则窗口外的超期迁移会被不变量静态否决；
// 窗口语义完全由互斥的守卫对表达，供边界探针区分严格与非严格。
func HandshakeMachine(strictPolicy bool) *Machine {
	policyOp := LE
	expiredOp := GT
	if strictPolicy {
		policyOp = LT
		expiredOp = GE
	}

	m := &Machine{
		Name:      "handshake-retransmit-authorization",
		Clocks:    MaxClocks,
		Locations: []string{"idle", "waiting", "authorized", "overrun"},
		Invariants: [][]Guard{
			LocIdle: {},
			LocWaiting: {
				{Clock: ClockRetransmit, Op: LE, Const: RetransmitPeriod},
			},
			LocAuthorized: {},
			LocOverrun:    {},
		},
		Initial: LocIdle,
		Errors:  []int{LocOverrun},
		Edges: []Edge{
			{
				From:  LocIdle,
				Event: EventRequest,
				Resets: []Action{
					{Clock: ClockRetransmit},
					{Clock: ClockTimeout},
				},
				To: LocWaiting,
			},
			{
				From:  LocWaiting,
				Event: EventDupAck,
				Guards: []Guard{
					{Clock: ClockRetransmit, Op: EQ, Const: RetransmitPeriod},
				},
				Resets: []Action{{Clock: ClockRetransmit}},
				To:     LocWaiting,
			},
			{
				From:  LocWaiting,
				Event: EventTimeout,
				Guards: []Guard{
					{Clock: ClockRetransmit, Op: EQ, Const: RetransmitPeriod},
					{Clock: ClockTimeout, Op: GT, Const: TimeoutLimit},
				},
				To: LocIdle,
			},
			{
				From:  LocWaiting,
				Event: EventUseGood,
				Guards: []Guard{
					{Clock: ClockRetransmit, Op: EQ, Const: RetransmitPeriod},
				},
				Resets: []Action{{Clock: ClockPolicy}},
				To:     LocAuthorized,
			},
			{
				From:  LocAuthorized,
				Event: EventUseGood,
				Guards: []Guard{
					{Clock: ClockPolicy, Op: policyOp, Const: PolicyWindow},
				},
				Resets: []Action{{Clock: ClockRetransmit}},
				To:     LocAuthorized,
			},
			{
				From:  LocAuthorized,
				Event: EventUseExpired,
				Guards: []Guard{
					{Clock: ClockPolicy, Op: expiredOp, Const: PolicyWindow},
				},
				To: LocOverrun,
			},
			{
				From:  LocAuthorized,
				Event: EventIdle,
				To:    LocIdle,
			},
		},
	}
	return m
}

// OverrunTarget 返回模型中的错误位置，便于宿主固定断言目标。
func OverrunTarget() int { return LocOverrun }
