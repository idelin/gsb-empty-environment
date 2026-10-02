// Package cdcundo 实现基于 CDC 前置/后置镜像的反向补偿重放与数据回滚包生成器。
//
// 引擎读取 CDC 日志中的每条变更事件（含 Before Image / After Image），
// 逆向生成对应的补偿 DML 序列（Undo 脚本），用于人为误操作后的快速冲正。
package cdcundo

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// Op 表示 CDC 变更操作类型。
type Op string

const (
	OpInsert Op = "INSERT"
	OpUpdate Op = "UPDATE"
	OpDelete Op = "DELETE"
)

// Row 是一行数据的列名到值的映射（镜像）。
type Row map[string]interface{}

// Clone 深拷贝一行（值按不可变标量处理）。
func (r Row) Clone() Row {
	if r == nil {
		return nil
	}
	out := make(Row, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

// Equal 比较两行是否逐列相等。
func (r Row) Equal(other Row) bool {
	if len(r) != len(other) {
		return false
	}
	for k, v := range r {
		ov, ok := other[k]
		if !ok || fmt.Sprint(ov) != fmt.Sprint(v) {
			return false
		}
	}
	return true
}

// ChangeEvent 是一条 CDC 变更事件，携带前置镜像与后置镜像。
type ChangeEvent struct {
	TxnID  string    `json:"txn_id"` // 源事务 ID
	Seq    int64     `json:"seq"`    // 日志序列号（单调递增）
	Table  string    `json:"table"`  // 目标表
	Op     Op        `json:"op"`     // 操作类型
	Before Row       `json:"before"` // 前置镜像（INSERT 时为 nil）
	After  Row       `json:"after"`  // 后置镜像（DELETE 时为 nil）
	At     time.Time `json:"at"`     // 变更发生时间（毫秒级）
}

// Validate 校验事件镜像与操作类型是否自洽。
func (e ChangeEvent) Validate() error {
	if e.Table == "" {
		return fmt.Errorf("cdcundo: seq=%d 缺少表名", e.Seq)
	}
	switch e.Op {
	case OpInsert:
		if e.After == nil {
			return fmt.Errorf("cdcundo: seq=%d INSERT 缺少 After Image", e.Seq)
		}
	case OpUpdate:
		if e.Before == nil || e.After == nil {
			return fmt.Errorf("cdcundo: seq=%d UPDATE 需要完整的 Before/After Image", e.Seq)
		}
	case OpDelete:
		if e.Before == nil {
			return fmt.Errorf("cdcundo: seq=%d DELETE 缺少 Before Image", e.Seq)
		}
	default:
		return fmt.Errorf("cdcundo: seq=%d 未知操作类型 %q", e.Seq, e.Op)
	}
	return nil
}

// LogWriter 以 JSON Lines 形式追加写入 CDC 日志（模拟 CDC 采集端落盘）。
type LogWriter struct {
	w   *bufio.Writer
	enc *json.Encoder
}

// NewLogWriter 创建写入到 w 的 CDC 日志写入器。
func NewLogWriter(w io.Writer) *LogWriter {
	bw := bufio.NewWriter(w)
	return &LogWriter{w: bw, enc: json.NewEncoder(bw)}
}

// Append 追加一条变更事件。
func (lw *LogWriter) Append(ev ChangeEvent) error {
	if err := ev.Validate(); err != nil {
		return err
	}
	return lw.enc.Encode(ev)
}

// Flush 刷写缓冲。
func (lw *LogWriter) Flush() error { return lw.w.Flush() }

// ReadLog 从 r 中按序读取全部 CDC 事件。
func ReadLog(r io.Reader) ([]ChangeEvent, error) {
	var events []ChangeEvent
	dec := json.NewDecoder(bufio.NewReader(r))
	for {
		var ev ChangeEvent
		err := dec.Decode(&ev)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("cdcundo: 解析 CDC 日志失败: %v", err)
		}
		if err := ev.Validate(); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, nil
}

// ReadLogFile 从文件读取 CDC 日志。
func ReadLogFile(path string) ([]ChangeEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadLog(f)
}
