package main

import "fmt"

// Entry 是一条日志项，Seq 为连续序号。
type Entry struct {
	Seq  int
	Data string
}

// Sender 模拟发送方。关键不变量：
//   - sent  已发送水位：已交给连接的最大序号（发送完成 != 已提交）；
//   - acked 提交水位：接收方累计持久化确认，只增不减；
//   - 旧确认不能降低 acked，确认也不能越过 sent。
type Sender struct {
	total     int
	batchSize int
	sent      int
	acked     int
	next      int
	events    []string
}

func NewSender(total, batchSize int) *Sender {
	return &Sender{total: total, batchSize: batchSize, next: 1}
}

func (s *Sender) logf(format string, args ...interface{}) {
	s.events = append(s.events, fmt.Sprintf(format, args...))
}

// applyAck 应用一个累计确认，保证单调性且不越过已发送水位。
func (s *Sender) applyAck(ack int) {
	switch {
	case ack < s.acked:
		s.logf("ACK %d 忽略：旧确认不能降低提交水位（当前 %d）", ack, s.acked)
	case ack > s.sent:
		s.logf("ACK %d 拒绝：确认不能越过已发送水位 %d", ack, s.sent)
	case ack == s.acked:
		s.logf("ACK %d 重复：提交水位不变", ack)
	default:
		s.acked = ack
		s.logf("ACK %d 接受：可提交前缀推进为 [1..%d]", ack, ack)
	}
}

// sendRange 发送 [from, to] 区间；retransmit 标记是否为重发。
func (s *Sender) sendRange(rx *Receiver, from, to int, retransmit bool) error {
	batch := make([]Entry, 0, to-from+1)
	for seq := from; seq <= to; seq++ {
		batch = append(batch, Entry{Seq: seq, Data: fmt.Sprintf("log-%d", seq)})
	}
	if to > s.sent {
		s.sent = to
	}
	kind := "SEND"
	if retransmit {
		kind = "RETRANSMIT"
	}
	s.logf("%s [%d..%d]（%d 条）", kind, from, to, len(batch))
	ack, err := rx.ReceiveBatch(batch)
	if err != nil {
		s.logf("发送 [%d..%d] 时断链：%v", from, to, err)
		return err
	}
	s.applyAck(ack)
	return nil
}

// recover 在断链后重连：先握手对齐累计确认，再重发未持久化区间。
func (s *Sender) recover(rx *Receiver) {
	s.logf("重连：握手获取接收方累计确认")
	s.applyAck(rx.Handshake())
	for s.acked < s.sent {
		from, to := s.acked+1, s.sent
		s.logf("重发区间：[%d..%d]", from, to)
		if err := s.sendRange(rx, from, to, true); err != nil {
			s.logf("重发期间再次断链，重新恢复")
			s.applyAck(rx.Handshake())
		}
	}
}

// Run 发送全部日志，并在结束后补齐未提交后缀。
func (s *Sender) Run(rx *Receiver) {
	for s.next <= s.total {
		from := s.next
		to := from + s.batchSize - 1
		if to > s.total {
			to = s.total
		}
		s.next = to + 1
		if err := s.sendRange(rx, from, to, false); err != nil {
			s.recover(rx)
		}
	}
	// 发送完成不等于提交：短写可能留下未持久化后缀。
	for s.acked < s.total {
		s.logf("发送完成（sent=%d）但提交水位=%d，需重发未提交后缀", s.sent, s.acked)
		from := s.acked + 1
		s.logf("重发区间：[%d..%d]", from, s.sent)
		if err := s.sendRange(rx, from, s.sent, true); err != nil {
			s.recover(rx)
		}
	}
}
