package netsec

import (
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
)

// TACACS+ 报文头常量。头部固定 12 字节：
// version(1) | type(1) | seq_no(1) | flags(1) | session_id(4) | length(4)，
// 其中 length 只计报文体，不含头部。
const (
	HeaderLen  = 12
	MaxBodyLen = 65536

	MajorVersion = 0xc

	FlagUnencrypted   uint8 = 0x01
	FlagSingleConnect uint8 = 0x04
)

// ErrSessionClosed 表示会话已因先前的致命错误断开，后续报文一律拒绝。
var ErrSessionClosed = errors.New("netsec: tacacs+ session is closed")

// Header 是解析后的 TACACS+ 报文头。
type Header struct {
	Version   uint8
	Type      uint8
	SeqNo     uint8
	Flags     uint8
	SessionID uint32
	Length    uint32
}

// Major 返回版本字节中的主版本号（高 4 位）。
func (h Header) Major() uint8 { return h.Version >> 4 }

// Minor 返回版本字节中的小版本号（低 4 位）。
func (h Header) Minor() uint8 { return h.Version & 0x0f }

// Unencrypted 报告 UNENCRYPT 标志是否置位。
func (h Header) Unencrypted() bool { return h.Flags&FlagUnencrypted != 0 }

// Packet 是校验并解密后的完整报文，Body 为明文。
type Packet struct {
	Header Header
	Body   []byte
}

// Config 是服务端会话配置。
type Config struct {
	// Key 是共享密钥，用于报文体解密。
	Key []byte
	// AllowUnencrypted 显式允许对端置 UNENCRYPT 标志发送明文；
	// 为 false 时收到置位报文即拒绝并断开会话。
	AllowUnencrypted bool
}

// Session 跟踪单个 TACACS+ 会话的序号状态。
// 客户端报文序号须为奇数，从 1 开始，每个客户端报文递增 2
// （偶数序号由服务端响应占用）。任何解析错误都会断开会话。
type Session struct {
	key              []byte
	allowUnencrypted bool
	expectedSeq      int
	closed           bool
}

// NewSession 创建会话，首个客户端报文序号须为 1。
func NewSession(cfg Config) *Session {
	return &Session{
		key:              cfg.Key,
		allowUnencrypted: cfg.AllowUnencrypted,
		expectedSeq:      1,
	}
}

// Parse 校验并处理一条原始报文（含 12 字节头部与报文体）。
// 校验通过后返回解密后的报文；任何校验失败都会断开会话并返回错误，
// 此后该会话上的所有 Parse 调用均返回 ErrSessionClosed。
func (s *Session) Parse(raw []byte) (*Packet, error) {
	if s.closed {
		return nil, ErrSessionClosed
	}
	p, err := s.parse(raw)
	if err != nil {
		s.closed = true
		return nil, err
	}
	return p, nil
}

func (s *Session) parse(raw []byte) (*Packet, error) {
	if len(raw) < HeaderLen {
		return nil, fmt.Errorf("netsec: packet too short: %d bytes, need at least %d", len(raw), HeaderLen)
	}

	h := Header{
		Version:   raw[0],
		Type:      raw[1],
		SeqNo:     raw[2],
		Flags:     raw[3],
		SessionID: binary.BigEndian.Uint32(raw[4:8]),
		Length:    binary.BigEndian.Uint32(raw[8:12]),
	}

	if h.Major() != MajorVersion {
		return nil, fmt.Errorf("netsec: unsupported major version 0x%x", h.Major())
	}
	if minor := h.Minor(); minor != 0 && minor != 1 {
		return nil, fmt.Errorf("netsec: unsupported minor version 0x%x", minor)
	}

	if h.Length > MaxBodyLen {
		return nil, fmt.Errorf("netsec: body length %d exceeds maximum %d", h.Length, MaxBodyLen)
	}
	if got := len(raw) - HeaderLen; got != int(h.Length) {
		return nil, fmt.Errorf("netsec: body length mismatch: header says %d, got %d", h.Length, got)
	}
	body := raw[HeaderLen:]

	if int(h.SeqNo) != s.expectedSeq {
		return nil, fmt.Errorf("netsec: unexpected client sequence number %d, want %d", h.SeqNo, s.expectedSeq)
	}
	s.expectedSeq += 2
	if s.expectedSeq > 255 {
		// 序号空间耗尽：本会话不再接受任何客户端报文。
		s.expectedSeq = 256
	}

	if h.Unencrypted() {
		if !s.allowUnencrypted {
			return nil, errors.New("netsec: unencrypted packet rejected (UNENCRYPT flag set)")
		}
	} else {
		body = Crypt(h.SessionID, s.key, h.Version, h.SeqNo, body)
	}

	return &Packet{Header: h, Body: body}, nil
}

// Crypt 用 MD5 伪随机填充对 data 做异或加解密（异或对称，加解密同一函数）：
//
//	pad_1 = MD5(session_id ‖ key ‖ version ‖ seq_no)
//	pad_n = MD5(session_id ‖ key ‖ version ‖ seq_no ‖ pad_{n-1})
//
// session_id 按 4 字节大端写入。返回新切片，不修改入参。
func Crypt(sessionID uint32, key []byte, version, seqNo byte, data []byte) []byte {
	out := make([]byte, len(data))
	pad := md5Pad(sessionID, key, version, seqNo, len(data))
	for i := range data {
		out[i] = data[i] ^ pad[i]
	}
	return out
}

func md5Pad(sessionID uint32, key []byte, version, seqNo byte, n int) []byte {
	var sid [4]byte
	binary.BigEndian.PutUint32(sid[:], sessionID)

	out := make([]byte, 0, n)
	var prev []byte
	for len(out) < n {
		h := md5.New()
		h.Write(sid[:])
		h.Write(key)
		h.Write([]byte{version, seqNo})
		h.Write(prev)
		prev = h.Sum(nil)
		out = append(out, prev...)
	}
	return out[:n]
}
