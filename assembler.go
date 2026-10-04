package logasm

import "strings"

// assembler 是单 (source, epoch) 的流式记录拼接器。
// 它处理：跨块半行缓冲、起始行/缩进续行判定、超长截断、
// 原始行号分配，以及 flush 时的不完整尾部。
type assembler struct {
	source string
	epoch  uint64
	rules  Rules

	partial string  // 尚未遇到换行符的半行
	pending *Record // 正在拼接、尚未闭合的记录
	lineNo  int     // 最近分配的原始行号
	recSeq  int     // 记录序号（ID 的一部分）

	emit  func(Record)
	event func(Event)
}

func newAssembler(source string, epoch uint64, rules Rules, emit func(Record), event func(Event)) *assembler {
	return &assembler{source: source, epoch: epoch, rules: rules, emit: emit, event: event}
}

// bufferedBytes 统计当前占用的缓冲（半行 + 待定记录正文）。
func (a *assembler) bufferedBytes() int {
	n := len(a.partial)
	if a.pending != nil {
		for _, l := range a.pending.Lines {
			n += len(l)
		}
	}
	return n
}

// Write 流入一个任意切分的文本块。半行被保留到下次 Write 或 Flush。
func (a *assembler) Write(chunk string) {
	data := a.partial + chunk
	a.partial = ""
	for {
		i := strings.IndexByte(data, '\n')
		if i < 0 {
			a.partial = data
			return
		}
		a.writeLineAt(0, strings.TrimSuffix(data[:i], "\r"))
		data = data[i+1:]
	}
}

// writeLineAt 处理一行；no>0 时使用外部给定的原始行号。
func (a *assembler) writeLineAt(no int, text string) {
	if no > 0 {
		a.lineNo = no
	} else {
		a.lineNo++
	}
	switch {
	case a.rules.isStart(text):
		// 新起始行关闭上一条。
		a.closePending(true)
		a.beginRecord(text)
	case a.rules.ContinuationIndent && isIndented(text) && a.pending != nil:
		a.appendLine(text)
	case a.pending != nil && !a.rules.ContinuationIndent:
		// 无缩进规则时，凡非起始行皆为续行。
		a.appendLine(text)
	case a.pending == nil:
		a.beginRecord(text)
	default:
		// 既非起始行也非缩进续行：按隐式起始行处理。
		a.closePending(true)
		a.beginRecord(text)
	}
}

func (a *assembler) beginRecord(text string) {
	a.recSeq++
	a.pending = &Record{
		ID:        recordID(a.source, a.epoch, a.recSeq),
		Source:    a.source,
		Epoch:     a.epoch,
		StartLine: a.lineNo,
		EndLine:   a.lineNo,
	}
	a.appendLine(text)
}

func (a *assembler) appendLine(text string) {
	p := a.pending
	if p.Truncated {
		// 已截断记录继续吸收续行，只计丢弃字节，不再拼接。
		p.DroppedBytes += len(text)
		p.EndLine = a.lineNo
		return
	}
	if a.rules.MaxRecordBytes > 0 {
		size := 0
		for _, l := range p.Lines {
			size += len(l) + 1
		}
		if size+len(text) > a.rules.MaxRecordBytes {
			room := a.rules.MaxRecordBytes - size
			if room < 0 {
				room = 0
			}
			if room > len(text) {
				room = len(text)
			}
			p.Lines = append(p.Lines, text[:room])
			p.LineNos = append(p.LineNos, a.lineNo)
			p.EndLine = a.lineNo
			p.Truncated = true
			p.DroppedBytes += len(text) - room
			a.event(Event{Kind: EventRecordTruncated, Source: a.source, Epoch: a.epoch,
				RecordID: p.ID, Detail: "record exceeded MaxRecordBytes, tail marked truncated"})
			return
		}
	}
	p.Lines = append(p.Lines, text)
	p.LineNos = append(p.LineNos, a.lineNo)
	p.EndLine = a.lineNo
}

func (a *assembler) closePending(closed bool) {
	if a.pending == nil {
		return
	}
	p := a.pending
	a.pending = nil
	p.Closed = closed
	a.emit(*p)
}

// Flush 结束输入：先把残留半行按完整行处理，再把仍未闭合的
// 待定记录以 Closed=false 发出（不完整尾部，带事件标记）。
func (a *assembler) Flush() {
	if a.partial != "" {
		line := strings.TrimSuffix(a.partial, "\r")
		a.partial = ""
		a.writeLineAt(0, line)
	}
	if a.pending != nil {
		a.event(Event{Kind: EventFlushIncomplete, Source: a.source, Epoch: a.epoch,
			RecordID: a.pending.ID, Detail: "stream ended with unterminated record"})
		a.closePending(false)
	}
}

// revisePending 就更正待定记录中原始行号为 no 的行。
func (a *assembler) revisePending(no int, text string) bool {
	if a.pending == nil {
		return false
	}
	for i, ln := range a.pending.LineNos {
		if ln == no {
			a.pending.Lines[i] = text
			a.pending.Revision++
			return true
		}
	}
	return false
}

// pendingOwns 报告待定记录是否包含原始行号 no。
func (a *assembler) pendingOwns(no int) bool {
	if a.pending == nil {
		return false
	}
	for _, ln := range a.pending.LineNos {
		if ln == no {
			return true
		}
	}
	return false
}
