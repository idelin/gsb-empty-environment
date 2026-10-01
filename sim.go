// Package sim 在整数 tick 的离散事件模型中实现一个明确的
// 慢启动 / 拥塞避免 / 超时回退传输模型。不发送真实网络流量。
package sim

import "fmt"

// Logger 记录带 tick 前缀的事件日志。
type Logger struct {
	sched   *Scheduler
	Verbose bool
	Lines   []string
}

func NewLogger(sched *Scheduler, verbose bool) *Logger {
	return &Logger{sched: sched, Verbose: verbose}
}

func (l *Logger) Logf(format string, args ...interface{}) {
	if !l.Verbose {
		return
	}
	msg := fmt.Sprintf(format, args...)
	l.Lines = append(l.Lines, fmt.Sprintf("tick=%-6d %s", l.sched.Now(), msg))
}

// Config 描述一次仿真。
type Config struct {
	MSS       int                           // 最大段长（字节）
	DataLen   int                           // 待传输总字节数
	InitCwnd  int                           // 初始拥塞窗口（字节）
	Ssthresh  int                           // 初始慢启动门限（字节）
	RTO       int                           // 初始重传超时（tick）
	MaxRTO    int                           // RTO 退避上限（tick）
	LinkDelay int                           // 单向链路时延（tick）
	DropData  func(p Packet, tick int) bool // 数据方向丢包谓词
	DropAck   func(p Packet, tick int) bool // ACK 方向丢包谓词
	Verbose   bool
}

// Sim 把调度器、发送方、接收方和双向链路接线成一个整体。
type Sim struct {
	Sched    *Scheduler
	Sender   *Sender
	Receiver *Receiver
	Log      *Logger
	DataLink *Link
	AckLink  *Link
}

func New(cfg Config) *Sim {
	sched := NewScheduler()
	log := NewLogger(sched, cfg.Verbose)
	dataLink := NewLink(sched, cfg.LinkDelay, cfg.DropData)
	ackLink := NewLink(sched, cfg.LinkDelay, cfg.DropAck)

	sender := NewSender(sched, dataLink, log, cfg.MSS, cfg.DataLen,
		cfg.InitCwnd, cfg.Ssthresh, cfg.RTO, cfg.MaxRTO)
	receiver := NewReceiver(ackLink, log)

	sim := &Sim{
		Sched: sched, Sender: sender, Receiver: receiver, Log: log,
		DataLink: dataLink, AckLink: ackLink,
	}
	// 接线：数据包交给接收方，ACK 交给发送方。
	sender.onDataDeliver = receiver.OnPacket
	receiver.onAckDeliver = sender.OnAck
	return sim
}

// Run 启动发送方并推进仿真，直到事件耗尽或超过 maxTicks。
func (s *Sim) Run(maxTicks int) {
	s.Sender.Start()
	s.Sched.Run(maxTicks)
}
