package smtpdata

import (
	"bytes"
	"errors"
	"io"
)

// endMarker 是 SMTP DATA 唯一合法的结束序列（RFC 5321 §4.1.1.4）。
// 前导 CRLF 是最后一行正文的行终止符，点号行本身只有 "." + CRLF。
var endMarker = []byte{'\r', '\n', '.', '\r', '\n'}

// LinePolicy 规定如何处理不是标准 CRLF 行终止符的歧义序列。
type LinePolicy int

const (
	// Reject 遇到裸 LF、裸 CR 等歧义序列时返回 *ProtocolError。
	Reject LinePolicy = iota
	// Normalize 将裸 LF / 裸 CR 规范化为 CRLF，消除与对端的理解差异。
	Normalize
)

// Config 控制 DATA 读取器对歧义行终止序列的处理方式。
// 零值表示全部拒绝，这是最安全的默认行为。
type Config struct {
	// BareLF 控制裸 LF（"a\nb"）以及 LF.LF 等以裸 LF 构造的伪结束标记。
	BareLF LinePolicy
	// BareCR 控制裸 CR（"a\rb"）以及 CRCRLF 等以裸 CR 构造的伪结束标记。
	BareCR LinePolicy
}

// 歧义序列类型，用于 ProtocolError.Sequence。
const (
	seqBareLF = "bare LF"
	seqBareCR = "bare CR"
)

// ProtocolError 表示收到的 DATA 违反了只允许 CRLF 行终止符的帧约定。
// Offset 是该歧义字节在去除结束标记后的原始正文中的字节偏移，
// 该偏移与读取分块方式无关，保持稳定。
type ProtocolError struct {
	Sequence string
	Offset   int64
}

func (e *ProtocolError) Error() string {
	return "smtpdata: illegal " + e.Sequence + " at body offset " + itoa(e.Offset)
}

// ErrIncomplete 由 Read 在底层流结束但始终未出现 CRLF.CRLF 时返回。
var ErrIncomplete = errors.New("smtpdata: DATA stream ended without CRLF.CRLF terminator")

// DataReader 读取 SMTP DATA 段。
//
// 它只在原始字节流中识别字面量 CRLF.CRLF 作为结束标记：标记可被任意
// TCP 分块拆开，裸 LF、裸 CR、LF.LF、CRCRLF 等序列永远不会被当作结束。
// 结束标记之后的字节一律不读、不投递。读取出的内容已按 RFC 5321 去除点
// 填充（行首 ".." -> "."），并按 Config 拒绝或规范化歧义行终止序列。
type DataReader struct {
	framer *framingReader
	decode *decodeReader
}

var _ io.Reader = (*DataReader)(nil)

// NewDataReader 包装 r。r 应提供 DATA 命令之后的原始字节，流中包含
// CRLF.CRLF 结束标记；标记之后的额外字节会被保留在底层连接上不被消费。
func NewDataReader(r io.Reader, cfg Config) *DataReader {
	f := &framingReader{src: r, atStart: true}
	return &DataReader{framer: f, decode: &decodeReader{src: f, cfg: cfg, lineStart: true}}
}

// Read 实现 io.Reader。返回 io.EOF 表示完整读到 CRLF.CRLF；
// 若底层先结束则返回 ErrIncomplete（可能伴随已投递的正文）。
func (d *DataReader) Read(p []byte) (int, error) {
	return d.decode.Read(p)
}

// Terminated 报告是否已在原始流中遇到完整的 CRLF.CRLF 结束标记。
func (d *DataReader) Terminated() bool { return d.framer.terminated }

// framingReader 只负责帧定界：在原始字节流中查找字面量 endMarker。
// 它输出 endMarker 之前的全部正文（含最后一行的 CRLF），并保证标记
// 跨越任意 Read 调用边界时也能正确识别，自身不做任何转义或规范化。
type framingReader struct {
	src        io.Reader
	buf        []byte // 已确认是正文、等待投递给上层的字节
	hold       []byte // endMarker 的可能前缀，尚不能确定归属
	terminated bool
	atStart    bool // 尚未确认任何正文归属（区分空邮件与普通末行）
	err        error
}

func (f *framingReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(f.buf) > 0 {
		n := copy(p, f.buf)
		f.buf = f.buf[n:]
		return n, nil
	}
	if f.terminated {
		return 0, io.EOF
	}
	if f.err != nil {
		err := f.err
		f.err = nil
		return 0, err
	}

	// 持续从底层读取直到产生可投递的正文或终态。一次 Read 拿到的数据
	// 可能整体都是 endMarker 的候选前缀（尤其上层用 1 字节缓冲时），
	// 因此用循环而不是递归，且每次 src.Read 都推进了真实输入。
	for len(f.buf) == 0 {
		tmp := make([]byte, len(f.hold)+len(p))
		n := copy(tmp, f.hold)
		m, rerr := f.src.Read(tmp[n:])
		chunk := tmp[:n+m]

		if idx := bytes.Index(chunk, endMarker); idx >= 0 {
			f.hold = f.hold[:0]
			// endMarker = CRLF "." CRLF。标记起点即末行 CRLF 的 CR，
			// 故 chunk[:idx] 不含该 CRLF，需要补回作为末行终止符；
			// 点号行（"." + CRLF）才是被剥离的标记本体。
			// 空邮件（流首即标记，idx==0 且此前无正文）不产出空行。
			f.buf = append(f.buf, chunk[:idx]...)
			if !(f.atStart && idx == 0) {
				f.buf = append(f.buf, '\r', '\n')
			}
			f.terminated = true
			if len(f.buf) > 0 {
				break
			}
			return 0, io.EOF
		}

		if rerr != nil {
			f.hold = f.hold[:0]
			// 流结束仍未见完整标记：先交付残余数据（含此前扣住的标记
			// 候选前缀，最后一次 Read 可能只返回 EOF 而没有新字节），
			// 再上报不完整。
			f.buf = append(f.buf, chunk...)
			if len(f.buf) > 0 {
				f.err = ErrIncomplete
				break
			}
			return 0, ErrIncomplete
		}

		// 未见标记：保留可能是标记前缀的最长后缀，其余作为正文输出。
		f.hold = f.hold[:0]
		keep := markerSuffixLen(chunk)
		split := len(chunk) - keep
		f.buf = append(f.buf, chunk[:split]...)
		f.hold = append(f.hold, chunk[split:]...)
		if split > 0 {
			f.atStart = false
		}
	}
	n := copy(p, f.buf)
	f.buf = f.buf[n:]
	return n, nil
}

// markerSuffixLen 返回 chunk 末尾与 endMarker 前缀匹配的最长长度，
// 这部分字节可能与后续数据拼成完整标记，必须暂缓投递。
func markerSuffixLen(chunk []byte) int {
	max := len(endMarker) - 1
	if max > len(chunk) {
		max = len(chunk)
	}
	for k := max; k > 0; k-- {
		if bytes.Equal(chunk[len(chunk)-k:], endMarker[:k]) {
			return k
		}
	}
	return 0
}

// decodeReader 在帧定界之上做点填充去除与行终止符策略处理，
// 全程流式、不缓冲整封邮件。
//
// 状态机只保留极少量状态：是否处于行首、是否有一个尚未配对判定的 CR，
// 以及一个已经从帧层读出但还没处理完的字节（have/cur）。任何字节只从
// 帧层读取一次，杜绝"退回再读"导致的重复投递。
type decodeReader struct {
	src        *framingReader
	cfg        Config
	lineStart  bool
	pendingCR  bool
	crOffset   int64
	have       bool
	cur        byte
	curOff     int64
	out        [2]byte // 规范化把 1 字节扩成 CRLF 时的溢出暂存
	outLen     int
	pendingErr *ProtocolError
	bodyOff    int64
}

// Read 每次调用先排空暂存输出，再消费原始字节直到 p 填满或流结束。
// 关键不变量：
//   - 每个原始字节只从帧层读取一次，have/pendingCR 记录跨字节状态；
//   - 输出先进两字节暂存区再拷进 p，p 装不下的部分留待下次，绝不在
//     字节尚未落稳时推进状态；
//   - 每轮要么消费一个原始字节（have 置 false），要么返回。
func (d *decodeReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	total := 0

	// 1. 排空上次残留的暂存输出。
	if d.outLen > 0 {
		n := d.drain(p)
		total += n
		p = p[n:]
		if d.outLen > 0 {
			return total, nil
		}
	}

	var raw [1]byte
	for len(p) > 0 {
		// 2. 取一个待处理原始字节（挂起字节优先，不重复读帧层）。
		if !d.have {
			n, err := d.src.Read(raw[:1])
			if n != 1 {
				// 帧层结束：先收尾一个挂起的裸 CR；暂存区若有输出则
				// 本次先交付，终态错误留待下一次 Read 上报。
				if d.pendingCR {
					d.pendingCR = false
					d.queueLineEnd(seqBareCR, d.crOffset)
				}
				if perr := d.takeErr(); perr != nil {
					return total, perr
				}
				if d.outLen > 0 {
					total += d.drain(p)
					return total, nil
				}
				return total, err
			}
			d.cur = raw[0]
			d.bodyOff++
			d.curOff = d.bodyOff - 1
			d.have = true
		}

		// 3. 状态转移。
		b := d.cur
		switch {
		case d.pendingCR:
			d.pendingCR = false
			if b == '\n' {
				d.push('\r')
				d.push('\n')
				d.lineStart = true
			} else {
				// 前一个是裸 CR；当前字节保留，下一轮再处理。
				d.queueLineEnd(seqBareCR, d.crOffset)
				if perr := d.takeErr(); perr != nil {
					return total, perr
				}
				// 规范化出的 CRLF 结束当前行，下一个字节位于新行行首，
				// 需正确参与点填充剥离（如 "\r." -> 新行行首的点）。
				d.lineStart = true
				n := d.drain(p)
				total += n
				p = p[n:]
				continue // have 仍为 true
			}
		case b == '\r':
			d.pendingCR = true
			d.crOffset = d.curOff
		case b == '\n':
			d.queueLineEnd(seqBareLF, d.curOff)
			d.lineStart = true
		case b == '.' && d.lineStart:
			// 点填充去除：行首 ".." -> "."；单点行是结束标记，帧层已剥离。
			d.lineStart = false
		default:
			d.push(b)
			d.lineStart = false
		}
		d.have = false

		// 4. 落盘本轮输出或上报拒绝错误。
		if perr := d.takeErr(); perr != nil {
			return total, perr
		}
		n := d.drain(p)
		total += n
		p = p[n:]
	}
	return total, nil
}

func (d *decodeReader) push(b byte) {
	d.out[d.outLen] = b
	d.outLen++
}

// queueLineEnd 按配置把裸 CR/LF 规范化为 CRLF，或登记拒绝错误。
func (d *decodeReader) queueLineEnd(kind string, off int64) {
	policy := d.cfg.BareLF
	if kind == seqBareCR {
		policy = d.cfg.BareCR
	}
	if policy == Normalize {
		d.push('\r')
		d.push('\n')
		return
	}
	d.pendingErr = &ProtocolError{Sequence: kind, Offset: off}
}

func (d *decodeReader) takeErr() error {
	if d.pendingErr != nil {
		e := d.pendingErr
		d.pendingErr = nil
		return e
	}
	return nil
}

// drain 把暂存区拷进 p 的剩余空间，返回本次拷出的字节数；
// 超出 p 的部分继续留在暂存区，由下次 Read 先排空。
func (d *decodeReader) drain(p []byte) int {
	n := copy(p, d.out[:d.outLen])
	copy(d.out[:], d.out[n:d.outLen])
	d.outLen -= n
	return n
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
