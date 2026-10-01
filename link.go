package sim

// Packet 是链路中传输的单元：数据段或纯 ACK。
type Packet struct {
	Seq    int  // 数据起始字节序号（仅数据包有效）
	Len    int  // 数据字节数（ACK 包为 0）
	Ack    bool // 是否为 ACK 包
	AckNum int  // 累计确认号：下一个期望字节
}

// Link 是单向链路：固定整数时延，可按确定性谓词丢包。
// 不发送任何真实网络流量，只在调度器上登记未来事件。
type Link struct {
	sched *Scheduler
	Delay int
	// ShouldDrop 返回 true 时丢弃该包（用于确定性丢包建模）。
	ShouldDrop func(p Packet, tick int) bool

	Sent    int
	Dropped int
}

func NewLink(sched *Scheduler, delay int, drop func(Packet, int) bool) *Link {
	return &Link{sched: sched, Delay: delay, ShouldDrop: drop}
}

// Send 把包放入链路，delay 个 tick 后调用 deliver。
func (l *Link) Send(p Packet, deliver func(Packet)) {
	l.Sent++
	if l.ShouldDrop != nil && l.ShouldDrop(p, l.sched.Now()) {
		l.Dropped++
		return
	}
	l.sched.After(l.Delay, func() { deliver(p) })
}
