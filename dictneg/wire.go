package dictneg

import (
	"encoding/binary"
	"fmt"
)

// 报文魔数与版本，便于宿主在流上定界与校验。
const (
	eventMagic = "DNGE"
	frameMagic = "DNGF"
	wireVer1   = 1
	headerLen  = 4 + 1 + 1 + 2 + 8 + DigestSize + 4
)

// maxWirePayload 是单条报文载荷的防御性上限（16 MiB），防止畸形长度声明
// 导致的巨量内存分配；真实限制由宿主与 Store 配置决定。
const maxWirePayload = 16 << 20

// EventKind 标识协商事件类型。
type EventKind uint8

const (
	// EventInvalid 是零值，非法。
	EventInvalid EventKind = iota
	// EventInstall 安装字典：携带 Epoch、摘要与字典内容。
	EventInstall
	// EventAck 接收端确认字典已持久安装。
	EventAck
	// EventNack 接收端拒绝安装（摘要不符、存储失败等）。
	EventNack
	// EventEnable 发送端声明活动字典切换到指定摘要。
	EventEnable
)

// String 返回事件类型的可读名称。
func (k EventKind) String() string {
	switch k {
	case EventInstall:
		return "install"
	case EventAck:
		return "ack"
	case EventNack:
		return "nack"
	case EventEnable:
		return "enable"
	default:
		return fmt.Sprintf("invalid(%d)", uint8(k))
	}
}

// Event 是安装、确认、启用三阶段的统一报文。
//
// 字段在线路上全部定长编码（Payload 除外），Epoch 在同一对端的会话内
// 单调递增，用于丢弃过期重放。
type Event struct {
	Kind    EventKind
	Epoch   uint64
	Digest  Digest
	Payload []byte // 仅 EventInstall 非空
}

// MarshalBinary 实现 encoding.BinaryMarshaler。
func (e Event) MarshalBinary() ([]byte, error) {
	if e.Kind == EventInvalid {
		return nil, fmt.Errorf("%w: event kind", ErrInvalidWire)
	}
	if len(e.Payload) > maxWirePayload {
		return nil, fmt.Errorf("%w: payload too large", ErrInvalidWire)
	}
	if e.Kind != EventInstall && len(e.Payload) != 0 {
		return nil, fmt.Errorf("%w: payload only allowed on install", ErrInvalidWire)
	}
	buf := make([]byte, headerLen+len(e.Payload))
	copy(buf[0:4], eventMagic)
	buf[4] = wireVer1
	buf[5] = byte(e.Kind)
	binary.BigEndian.PutUint64(buf[8:16], e.Epoch)
	copy(buf[16:16+DigestSize], e.Digest[:])
	binary.BigEndian.PutUint32(buf[48:52], uint32(len(e.Payload)))
	copy(buf[headerLen:], e.Payload)
	return buf, nil
}

// UnmarshalBinary 实现 encoding.BinaryUnmarshaler。
func (e *Event) UnmarshalBinary(data []byte) error {
	if len(data) < headerLen || string(data[0:4]) != eventMagic {
		return fmt.Errorf("%w: event header", ErrInvalidWire)
	}
	if data[4] != wireVer1 {
		return fmt.Errorf("%w: unsupported event version", ErrInvalidWire)
	}
	kind := EventKind(data[5])
	if kind < EventInstall || kind > EventEnable {
		return fmt.Errorf("%w: event kind", ErrInvalidWire)
	}
	plen := binary.BigEndian.Uint32(data[48:52])
	if uint64(plen) > maxWirePayload || headerLen+int(plen) != len(data) {
		return fmt.Errorf("%w: event payload length", ErrInvalidWire)
	}
	if kind != EventInstall && plen != 0 {
		return fmt.Errorf("%w: payload only allowed on install", ErrInvalidWire)
	}
	e.Kind = kind
	e.Epoch = binary.BigEndian.Uint64(data[8:16])
	copy(e.Digest[:], data[16:16+DigestSize])
	e.Payload = append(e.Payload[:0], data[headerLen:headerLen+plen]...)
	return nil
}

// Frame 是绑定字典摘要的数据帧。
//
// 接收端只允许用 Digest 对应的字典解压 Payload；摘要未知时只能有界暂存，
// 严禁回退到其他字典猜测解码。Seq 在同一摘要的流内单调递增，用于乱序
// 暂存后的重排交付。
type Frame struct {
	Digest  Digest
	Seq     uint64
	Payload []byte
}

// MarshalBinary 实现 encoding.BinaryMarshaler。
func (f Frame) MarshalBinary() ([]byte, error) {
	if len(f.Payload) > maxWirePayload {
		return nil, fmt.Errorf("%w: frame payload too large", ErrInvalidWire)
	}
	buf := make([]byte, headerLen+len(f.Payload))
	copy(buf[0:4], frameMagic)
	buf[4] = wireVer1
	binary.BigEndian.PutUint64(buf[8:16], f.Seq)
	copy(buf[16:16+DigestSize], f.Digest[:])
	binary.BigEndian.PutUint32(buf[48:52], uint32(len(f.Payload)))
	copy(buf[headerLen:], f.Payload)
	return buf, nil
}

// UnmarshalBinary 实现 encoding.BinaryUnmarshaler。
func (f *Frame) UnmarshalBinary(data []byte) error {
	if len(data) < headerLen || string(data[0:4]) != frameMagic {
		return fmt.Errorf("%w: frame header", ErrInvalidWire)
	}
	if data[4] != wireVer1 {
		return fmt.Errorf("%w: unsupported frame version", ErrInvalidWire)
	}
	plen := binary.BigEndian.Uint32(data[48:52])
	if uint64(plen) > maxWirePayload || headerLen+int(plen) != len(data) {
		return fmt.Errorf("%w: frame payload length", ErrInvalidWire)
	}
	f.Seq = binary.BigEndian.Uint64(data[8:16])
	copy(f.Digest[:], data[16:16+DigestSize])
	f.Payload = append(f.Payload[:0], data[headerLen:headerLen+plen]...)
	return nil
}
