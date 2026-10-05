package main

import "errors"

// ErrConnReset 模拟接收方在持久化部分批次后断链。
var ErrConnReset = errors.New("connection reset by peer")

// BatchOutcome 是对一次 ReceiveBatch 调用的故障注入脚本：
// PersistN 表示只持久化批次中的前 N 条（短写），DropConn 表示随后断链。
type BatchOutcome struct {
	PersistN int
	DropConn bool
}

// Receiver 模拟接收方：持久化日志项并返回累计确认（最高连续持久化序号）。
type Receiver struct {
	persisted map[int]string
	contig    int // 最高连续持久化序号（累计确认值）
	plan      []BatchOutcome
	planIdx   int
}

func NewReceiver(plan []BatchOutcome) *Receiver {
	return &Receiver{persisted: make(map[int]string), plan: plan}
}

// Handshake 在重连时返回接收方当前的累计持久化确认。
func (r *Receiver) Handshake() int {
	return r.contig
}

// ReceiveBatch 模拟接收一个批次：按故障脚本持久化前缀，可能断链。
// 返回值为累计确认（最高连续持久化序号），断链时返回错误且确认丢失。
func (r *Receiver) ReceiveBatch(batch []Entry) (int, error) {
	oc := BatchOutcome{PersistN: len(batch)}
	if r.planIdx < len(r.plan) {
		oc = r.plan[r.planIdx]
		r.planIdx++
	}
	n := oc.PersistN
	if n > len(batch) {
		n = len(batch)
	}
	for i := 0; i < n; i++ {
		r.persisted[batch[i].Seq] = batch[i].Data
	}
	for r.persisted[r.contig+1] != "" {
		r.contig++
	}
	if oc.DropConn {
		return 0, ErrConnReset
	}
	return r.contig, nil
}

// PersistedCount 返回已持久化的条目总数（用于校验）。
func (r *Receiver) PersistedCount() int {
	return len(r.persisted)
}
