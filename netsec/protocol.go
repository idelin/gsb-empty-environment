package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MySQL 客户端/服务端能力位（CLIENT_*），见 MySQL 协议文档
// Protocol::CapabilityFlags。
const (
	capLongPassword               uint32 = 0x00000001
	capFoundRows                  uint32 = 0x00000002
	capLongFlag                   uint32 = 0x00000004
	capConnectWithDB              uint32 = 0x00000008
	capNoSchema                   uint32 = 0x00000010
	capCompress                   uint32 = 0x00000020
	capODBC                       uint32 = 0x00000040
	capLocalFiles                 uint32 = 0x00000080
	capIgnoreSpace                uint32 = 0x00000100
	capProtocol41                 uint32 = 0x00000200
	capInteractive                uint32 = 0x00000400
	capClientSSL                  uint32 = 0x00000800
	capIgnoreSIGPIPE              uint32 = 0x00001000
	capTransactions               uint32 = 0x00002000
	capReserved                   uint32 = 0x00004000
	capSecureConnection           uint32 = 0x00008000
	capMultiStatements            uint32 = 0x00010000
	capMultiResults               uint32 = 0x00020000
	capMultiStatementResults      uint32 = 0x00040000
	capPSMultiResults             uint32 = 0x00080000
	capPluginAuth                 uint32 = 0x00080000
	capConnectAttrs               uint32 = 0x00100000
	capPluginAuthLenEncClientData uint32 = 0x00200000
	capCanHandleExpiredPasswords  uint32 = 0x00400000
	capSessionTrack               uint32 = 0x00800000
	capDeprecateEOF               uint32 = 0x01000000
)

// clientCapabilityMask 是本准入器愿意在 SSLRequest 中宣告的能力位集合。
// 最终宣告值始终与服务端能力位取交集，绝不宣告对端不支持的能力。
const clientCapabilityMask = capLongPassword | capFoundRows | capLongFlag |
	capConnectWithDB | capNoSchema | capCompress | capODBC | capLocalFiles |
	capIgnoreSpace | capProtocol41 | capInteractive | capClientSSL |
	capIgnoreSIGPIPE | capTransactions | capReserved | capSecureConnection |
	capMultiStatements | capMultiResults | capPSMultiResults | capConnectAttrs |
	capPluginAuthLenEncClientData | capCanHandleExpiredPasswords |
	capSessionTrack | capDeprecateEOF

const (
	// maxPacketPayload 单个 MySQL 包的最大载荷长度（16MiB - 1）。
	maxPacketPayload = 1<<24 - 1
	// sslRequestPayloadLen Protocol::SSLRequest 固定 32 字节载荷。
	sslRequestPayloadLen = 32
	// defaultCharset utf8mb4_general_ci 的字符集编号。
	defaultCharset byte = 45
	// defaultMaxPacketSize SSLRequest 中的最大包长字段。
	defaultMaxPacketSize uint32 = 1<<24 - 1
)

var (
	// ErrMalformedHandshake 初始握手包无法按 MySQL v10 协议解析。
	ErrMalformedHandshake = errors.New("netsec: malformed initial handshake packet")
	// ErrOversizedPacket 包载荷超过 MySQL 协议上限。
	ErrOversizedPacket = errors.New("netsec: packet payload exceeds 16MiB-1")
)

// readPacket 读取一个完整 MySQL 包，返回载荷与序号。
func readPacket(r io.Reader) (payload []byte, seq byte, err error) {
	var header [4]byte
	if _, err = io.ReadFull(r, header[:]); err != nil {
		return nil, 0, err
	}
	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	seq = header[3]
	if length == 0 {
		return nil, seq, fmt.Errorf("%w: zero-length payload", ErrMalformedHandshake)
	}
	if length > maxPacketPayload {
		return nil, seq, fmt.Errorf("%w: %d bytes", ErrOversizedPacket, length)
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(r, payload); err != nil {
		return nil, seq, err
	}
	return payload, seq, nil
}

// writePacket 将载荷加上 4 字节包头一次性写出，避免明文阶段产生半包。
func writePacket(w io.Writer, seq byte, payload []byte) error {
	if len(payload) > maxPacketPayload {
		return fmt.Errorf("%w: %d bytes", ErrOversizedPacket, len(payload))
	}
	frame := make([]byte, 4+len(payload))
	frame[0] = byte(len(payload))
	frame[1] = byte(len(payload) >> 8)
	frame[2] = byte(len(payload) >> 16)
	frame[3] = seq
	copy(frame[4:], payload)
	_, err := w.Write(frame)
	return err
}

// serverHandshake 是 InitialHandshakePacket（协议版本 10）的解析结果。
type serverHandshake struct {
	protocolVersion byte
	serverVersion   string
	connectionID    uint32
	capabilities    uint32
	charset         byte
	statusFlags     uint16
	authPluginName  string
	scramble        []byte
}

// parseInitialHandshake 按 MySQL 10 号初始握手包格式解析载荷。
func parseInitialHandshake(payload []byte) (*serverHandshake, error) {
	if len(payload) < 2 {
		return nil, fmt.Errorf("%w: payload too short", ErrMalformedHandshake)
	}
	pos := 0
	hs := &serverHandshake{}
	hs.protocolVersion = payload[pos]
	pos++
	if hs.protocolVersion != 10 {
		return nil, fmt.Errorf("%w: protocol version %d, only v10 is supported",
			ErrMalformedHandshake, hs.protocolVersion)
	}

	idx := bytes.IndexByte(payload[pos:], 0)
	if idx < 0 {
		return nil, fmt.Errorf("%w: unterminated server version", ErrMalformedHandshake)
	}
	hs.serverVersion = string(payload[pos : pos+idx])
	pos += idx + 1

	if len(payload) < pos+4 {
		return nil, fmt.Errorf("%w: missing connection id", ErrMalformedHandshake)
	}
	hs.connectionID = binary.LittleEndian.Uint32(payload[pos:])
	pos += 4

	if len(payload) < pos+9 {
		return nil, fmt.Errorf("%w: missing auth-plugin-data-part-1", ErrMalformedHandshake)
	}
	authPart1 := make([]byte, 8)
	copy(authPart1, payload[pos:pos+8])
	pos += 8
	if payload[pos] != 0 {
		return nil, fmt.Errorf("%w: filler byte must be zero", ErrMalformedHandshake)
	}
	pos++

	if len(payload) < pos+2 {
		return nil, fmt.Errorf("%w: missing capability flags", ErrMalformedHandshake)
	}
	capLower := binary.LittleEndian.Uint16(payload[pos:])
	pos += 2

	if len(payload) >= pos+1 {
		hs.charset = payload[pos]
		pos++
	}
	if len(payload) >= pos+2 {
		hs.statusFlags = binary.LittleEndian.Uint16(payload[pos:])
		pos += 2
	}
	if len(payload) < pos+2 {
		return nil, fmt.Errorf("%w: missing upper capability flags", ErrMalformedHandshake)
	}
	capUpper := binary.LittleEndian.Uint16(payload[pos:])
	pos += 2
	hs.capabilities = uint32(capLower) | uint32(capUpper)<<16

	var authPluginDataLen byte
	if hs.capabilities&capPluginAuth != 0 {
		if len(payload) < pos+1 {
			return nil, fmt.Errorf("%w: missing auth plugin data length", ErrMalformedHandshake)
		}
		authPluginDataLen = payload[pos]
		pos++
	}

	// Protocol::41 保留 10 个全零字节。
	if len(payload) < pos+10 {
		return nil, fmt.Errorf("%w: missing reserved bytes", ErrMalformedHandshake)
	}
	pos += 10

	var authPart2 []byte
	if hs.capabilities&capSecureConnection != 0 {
		part2Len := 13
		if authPluginDataLen > 8 {
			part2Len = int(authPluginDataLen) - 8
		}
		if len(payload) < pos+part2Len {
			return nil, fmt.Errorf("%w: truncated auth-plugin-data-part-2", ErrMalformedHandshake)
		}
		authPart2 = payload[pos : pos+part2Len]
		pos += part2Len
	}

	if hs.capabilities&capPluginAuth != 0 && pos < len(payload) {
		end := bytes.IndexByte(payload[pos:], 0)
		if end < 0 {
			hs.authPluginName = string(payload[pos:])
		} else {
			hs.authPluginName = string(payload[pos : pos+end])
		}
	}

	scramble := make([]byte, 0, len(authPart1)+len(authPart2))
	scramble = append(scramble, authPart1...)
	scramble = append(scramble, authPart2...)
	scramble = bytes.TrimRight(scramble, "\x00")
	if len(scramble) == 0 {
		return nil, fmt.Errorf("%w: empty auth plugin data", ErrMalformedHandshake)
	}
	hs.scramble = scramble
	return hs, nil
}

// buildSSLRequest 构造固定 32 字节的 Protocol::SSLRequest 载荷：
// capability flags(4) + max packet size(4) + charset(1) + reserved(23)。
func buildSSLRequest(capabilities uint32, charset byte, maxPacketSize uint32) []byte {
	payload := make([]byte, sslRequestPayloadLen)
	binary.LittleEndian.PutUint32(payload[0:4], capabilities)
	binary.LittleEndian.PutUint32(payload[4:8], maxPacketSize)
	payload[8] = charset
	return payload
}
