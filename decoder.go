package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// 题内事件线格式（大端序）：
//
//	[0]              寄存器号 uint8
//	[1]              事件类型 uint8
//	[2:6]            载荷长度 uint32，必须与类型声明长度一致
//	[6:6+n]          载荷
//	[6+n:6+n+32]     摘要 = SHA-256(寄存器号 || 类型 || 长度 || 载荷)
const (
	HeaderLen = 6
	DigestLen = sha256.Size
)

var (
	ErrUnknownType    = errors.New("netsec: unknown event type")
	ErrLengthMismatch = errors.New("netsec: length field does not match type declaration")
	ErrDigestMismatch = errors.New("netsec: event digest mismatch")
	ErrTruncated      = errors.New("netsec: truncated event at end of stream")
	ErrClosed         = errors.New("netsec: decoder already closed")
)

// TypeTable 记录每种事件类型声明的固定载荷长度。
type TypeTable struct {
	lens map[uint8]uint32
}

func NewTypeTable() *TypeTable {
	return &TypeTable{lens: make(map[uint8]uint32)}
}

// Declare 声明事件类型 typ 的载荷长度恒为 payloadLen。
func (t *TypeTable) Declare(typ uint8, payloadLen uint32) {
	t.lens[typ] = payloadLen
}

func (t *TypeTable) lookup(typ uint8) (uint32, bool) {
	n, ok := t.lens[typ]
	return n, ok
}

// Event 是一条完整且摘要校验通过的事件。
type Event struct {
	Register uint8
	Type     uint8
	Payload  []byte
	Digest   [DigestLen]byte
}

// MarshalEvent 按线格式编码一条事件并计算摘要，供对端（固件侧）与测试使用。
func MarshalEvent(register, typ uint8, payload []byte) []byte {
	buf := make([]byte, 0, HeaderLen+len(payload)+DigestLen)
	buf = append(buf, register, typ, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(buf[2:6], uint32(len(payload)))
	buf = append(buf, payload...)
	sum := sha256.Sum256(buf)
	return append(buf, sum[:]...)
}

// Decoder 是增量解码器：任意切块喂入，只在事件完整、长度与类型声明一致
// 且摘要匹配时才交付。解码出错后进入失败态，拒绝后续输入，绝不向前
// 搜索魔数尝试重新同步。
type Decoder struct {
	types  *TypeTable
	buf    []byte
	err    error
	closed bool
}

func NewDecoder(types *TypeTable) *Decoder {
	return &Decoder{types: types}
}

// Feed 喂入下一块字节，返回本次新完成的事件。
func (d *Decoder) Feed(p []byte) ([]Event, error) {
	if d.err != nil {
		return nil, d.err
	}
	if d.closed {
		return nil, d.fail(ErrClosed)
	}
	d.buf = append(d.buf, p...)
	var out []Event
	for {
		ev, consumed, err := d.tryParse()
		if err != nil {
			return nil, d.fail(err)
		}
		if consumed == 0 {
			break
		}
		d.buf = d.buf[consumed:]
		out = append(out, ev)
	}
	return out, nil
}

// tryParse 尝试从缓冲头部解析一条事件。返回 consumed==0 表示数据不足，
// 继续等待更多字节；返回错误表示流已损坏，必须终止。
func (d *Decoder) tryParse() (Event, int, error) {
	if len(d.buf) < HeaderLen {
		return Event{}, 0, nil
	}
	typ := d.buf[1]
	declared, ok := d.types.lookup(typ)
	if !ok {
		return Event{}, 0, fmt.Errorf("%w: 0x%02x", ErrUnknownType, typ)
	}
	n := binary.BigEndian.Uint32(d.buf[2:6])
	if n != declared {
		return Event{}, 0, fmt.Errorf("%w: type 0x%02x declares %d, field says %d",
			ErrLengthMismatch, typ, declared, n)
	}
	total := HeaderLen + int(n) + DigestLen
	if len(d.buf) < total {
		return Event{}, 0, nil
	}
	raw := d.buf[:total]
	var digest [DigestLen]byte
	copy(digest[:], raw[total-DigestLen:])
	if want := sha256.Sum256(raw[:total-DigestLen]); !bytes.Equal(digest[:], want[:]) {
		return Event{}, 0, ErrDigestMismatch
	}
	payload := make([]byte, int(n))
	copy(payload, raw[HeaderLen:HeaderLen+int(n)])
	return Event{
		Register: raw[0],
		Type:     typ,
		Payload:  payload,
		Digest:   digest,
	}, total, nil
}

// Close 宣告输入结束。缓冲中若残留不完整的半个事件则报错。
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	d.closed = true
	if len(d.buf) > 0 {
		return d.fail(fmt.Errorf("%w: %d bytes pending", ErrTruncated, len(d.buf)))
	}
	return nil
}

func (d *Decoder) fail(err error) error {
	d.err = err
	d.buf = nil
	return err
}
