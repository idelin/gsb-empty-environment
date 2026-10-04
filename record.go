package logasm

import (
	"fmt"
	"strings"
)

// Record 是一条重组完成的日志记录。
// ID 在 (Source, Epoch) 内稳定；每次迟到更正/补行产生新 Revision，
// ID 不变。LineNos 保留组成该记录的原始行号。
type Record struct {
	ID           string
	Source       string
	Epoch        uint64
	Revision     int
	StartLine    int
	EndLine      int
	LineNos      []int
	Lines        []string
	Truncated    bool // 超长被截断：标记而非静默丢行
	DroppedBytes int  // 因截断丢弃的字节数
	Closed       bool // false 表示 flush 时仍未闭合（不完整尾部）
}

// Text 以换行拼接记录正文。
func (r Record) Text() string { return strings.Join(r.Lines, "\n") }

// String 便于日志打印。
func (r Record) String() string {
	return fmt.Sprintf("%s rev=%d lines=%d-%d truncated=%v closed=%v",
		r.ID, r.Revision, r.StartLine, r.EndLine, r.Truncated, r.Closed)
}

func recordID(source string, epoch uint64, seq int) string {
	return fmt.Sprintf("%s@%d#%06d", source, epoch, seq)
}
