package sim

// Receiver 是累计确认接收方：只按序交付，失序段丢弃并回重复 ACK。
type Receiver struct {
	link *Link // 反向 ACK 链路
	log  *Logger

	RcvNxt      int // 下一个期望字节（累计 ACK 号）
	Delivered   int // 已按序交付字节数
	AcksSent    int
	DupAcksSent int

	onAckDeliver func(Packet) // 由仿真框架填充，把 ACK 交给发送方
}

func NewReceiver(link *Link, log *Logger) *Receiver {
	return &Receiver{link: link, log: log, onAckDeliver: func(Packet) {}}
}

// OnPacket 处理到达的数据包。
func (r *Receiver) OnPacket(p Packet) {
	switch {
	case p.Seq == r.RcvNxt:
		// 按序到达：推进累计确认点。
		r.RcvNxt += p.Len
		r.Delivered += p.Len
		r.sendAck()
	case p.Seq < r.RcvNxt:
		// 旧包（已被确认过的重传）：去重，仅重发当前 ACK，不重复交付。
		r.sendAck()
	default:
		// 失序到达：无缓存模型，丢弃并回重复 ACK。
		r.DupAcksSent++
		r.sendAck()
	}
}

func (r *Receiver) sendAck() {
	ack := Packet{Ack: true, AckNum: r.RcvNxt}
	r.AcksSent++
	r.log.Logf("recv: send ack=%d", r.RcvNxt)
	r.link.Send(ack, r.onAckDeliver)
}
