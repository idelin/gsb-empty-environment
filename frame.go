// Package correlator 按 (epoch, seq) 关联请求与响应，
// 并提供可任意分块的流式帧解析、有界存储与 checkpoint 能力。
package correlator

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// 线格式（大端）：
//
//	0  ..1  magic: 0xC0 0xDE
//	2       version: 1
//	3       type: 1=request, 2=response
//	4  ..11 epoch: uint64
//	12 ..19 seq: uint64
//	20 ..23 payloadLen: uint32
//	24 ..   payload
const (
	frameMagic0  = 0xC0
	frameMagic1  = 0xDE
	frameVersion = 1
	// HeaderSize 是帧头固定字节数。
	HeaderSize = 24
)

// FrameType 标识帧类型。
type FrameType byte

const (
	// FrameRequest 请求帧。
	FrameRequest FrameType = 1
	// FrameResponse 响应帧。
	FrameResponse FrameType = 2
)

// Frame 是一条解码后的消息。
type Frame struct {
	Type    FrameType
	Epoch   uint64
	Seq     uint64
	Payload []byte
}

var (
	// ErrNeedMoreData 表示缓冲区数据不足，尚未构成完整帧（非错误输入）。
	ErrNeedMoreData = errors.New("need more data")
	// ErrBadData 表示输入本身非法（魔数/版本/类型/长度额度），应断开或丢弃。
	ErrBadData = errors.New("bad data")
)

// EncodeFrame 把帧编码为字节流。
func EncodeFrame(f Frame) []byte {
	out := make([]byte, HeaderSize+len(f.Payload))
	out[0] = frameMagic0
	out[1] = frameMagic1
	out[2] = frameVersion
	out[3] = byte(f.Type)
	binary.BigEndian.PutUint64(out[4:12], f.Epoch)
	binary.BigEndian.PutUint64(out[12:20], f.Seq)
	binary.BigEndian.PutUint32(out[20:24], uint32(len(f.Payload)))
	copy(out[HeaderSize:], f.Payload)
	return out
}

// Parser 是增量式帧解析器，接受任意分块的字节输入。
// 内存有界：内部缓冲不超过 maxBuffered，单次分配不超过 maxPayload。
type Parser struct {
	buf         []byte
	maxPayload  uint32
	maxBuffered int
}

// NewParser 创建解析器。maxPayload 是单帧负载上限；maxBuffered 是
// 内部未消费缓冲上限（应 >= HeaderSize+maxPayload）。
func NewParser(maxPayload uint32, maxBuffered int) *Parser {
	return &Parser{maxPayload: maxPayload, maxBuffered: maxBuffered}
}

// Feed 追加一块输入。超过缓冲额度时报错（调用方应先用 Next 排空）。
func (p *Parser) Feed(data []byte) error {
	if len(p.buf)+len(data) > p.maxBuffered {
		return fmt.Errorf("%w: buffer quota %d exceeded", ErrBadData, p.maxBuffered)
	}
	p.buf = append(p.buf, data...)
	return nil
}

// Next 尝试弹出一帧。
//   - 数据不足：返回 ErrNeedMoreData，不发生任何分配；
//   - 数据非法或长度超额度：返回 ErrBadData，在分配 payload 之前拒绝；
//   - 成功：返回帧并消费对应字节。
func (p *Parser) Next() (Frame, error) {
	if len(p.buf) < HeaderSize {
		return Frame{}, ErrNeedMoreData
	}
	if p.buf[0] != frameMagic0 || p.buf[1] != frameMagic1 {
		return Frame{}, fmt.Errorf("%w: bad magic %#x %#x", ErrBadData, p.buf[0], p.buf[1])
	}
	if p.buf[2] != frameVersion {
		return Frame{}, fmt.Errorf("%w: bad version %d", ErrBadData, p.buf[2])
	}
	ft := FrameType(p.buf[3])
	if ft != FrameRequest && ft != FrameResponse {
		return Frame{}, fmt.Errorf("%w: bad frame type %d", ErrBadData, p.buf[3])
	}
	payloadLen := binary.BigEndian.Uint32(p.buf[20:24])
	// 分配前检查长度额度：拒绝超额长度，无论数据是否到齐。
	if payloadLen > p.maxPayload {
		return Frame{}, fmt.Errorf("%w: payload length %d exceeds quota %d", ErrBadData, payloadLen, p.maxPayload)
	}
	total := HeaderSize + int(payloadLen)
	if len(p.buf) < total {
		return Frame{}, ErrNeedMoreData
	}
	f := Frame{
		Type:    ft,
		Epoch:   binary.BigEndian.Uint64(p.buf[4:12]),
		Seq:     binary.BigEndian.Uint64(p.buf[12:20]),
		Payload: make([]byte, payloadLen),
	}
	copy(f.Payload, p.buf[HeaderSize:total])
	p.buf = p.buf[total:]
	return f, nil
}
