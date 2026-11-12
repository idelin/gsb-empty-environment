package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	// ntpHeaderSize RFC 5905 的 NTP 固定头长度。
	ntpHeaderSize = 48
	// ntpEpochOffset 1900-01-01 与 Unix 纪元 1970-01-01 之间的秒数。
	ntpEpochOffset = 2208988800
	// maxUDPPacket NTP over UDP 的合理报文上限。
	maxUDPPacket = 1500
)

// ServerConfig 控制 NTS 校验服务的密钥与防重放参数。
type ServerConfig struct {
	// AEAD Cookie 保护密钥使用的 AEAD 算法标识，零值默认 AES-128-GCM。
	AEAD uint16
	// CookieKey 保护不透明 Cookie 的对称密钥。
	CookieKey []byte
	// ReplayTTL UID/Cookie 一次性缓存的保留时长，零值默认 10 分钟。
	ReplayTTL time.Duration
	// CacheCapacity 每个缓存的条目上限，零值默认 65536。
	CacheCapacity int
	// Now 注入时钟，主要供测试使用；nil 时使用 time.Now。
	Now func() time.Time
}

// Server 是无状态协议外的全部 NTS 校验状态：Cookie 密钥环、
// Unique Identifier 重放缓存与 Cookie 一次性缓存。并发安全。
type Server struct {
	keyRing *CookieKeyRing

	mu      sync.Mutex
	replay  *ttlCache
	cookies *ttlCache
	now     func() time.Time
}

// NewServer 用默认防重放参数创建 NTS 校验服务。
func NewServer(algo uint16, cookieKey []byte) (*Server, error) {
	return NewServerWithConfig(ServerConfig{AEAD: algo, CookieKey: cookieKey})
}

// NewServerWithConfig 按完整配置创建 NTS 校验服务。
func NewServerWithConfig(cfg ServerConfig) (*Server, error) {
	algo := cfg.AEAD
	if algo == 0 {
		algo = AEADAESGCM128
	}
	keyRing, err := NewCookieKeyRing(algo, cfg.CookieKey)
	if err != nil {
		return nil, err
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Server{
		keyRing: keyRing,
		replay:  newTTLCache(cfg.ReplayTTL, cfg.CacheCapacity, now),
		cookies: newTTLCache(cfg.ReplayTTL, cfg.CacheCapacity, now),
		now:     now,
	}, nil
}

// RotateCookieKey 轮换 Cookie 保护密钥；旧密钥仍可解封存量 Cookie。
func (s *Server) RotateCookieKey(algo uint16, key []byte) error {
	return s.keyRing.Rotate(algo, key)
}

// MintCookie 为新会话签发不透明 Cookie，供 NTS-KE 阶段或测试使用。
func (s *Server) MintCookie(secrets CookieSecrets) ([]byte, error) {
	return s.keyRing.Mint(secrets)
}

// ProcessPacket 校验一条 NTS 保护的 NTP 请求。
//
// 校验顺序：
//  1. 固定头与扩展字段结构（UID 在前且 ≥32 字节、Cookie 位于 Authenticator 之前、
//     Authenticator 收尾）；
//  2. 解开任一 Cookie 取得流量密钥，验证 Authenticator，AAD 覆盖完整 48 字节头
//     及其前所有扩展；
//  3. 在同一把锁内完成 UID 重放与 Cookie 复用检查并登记消费。
//
// 任何一步失败都返回非 nil 错误且不产生响应字节——调用方据此静默丢弃，
// 绝不回复时间，避免向攻击者泄露可拨时钟的响应。
func (s *Server) ProcessPacket(packet []byte) ([]byte, error) {
	if len(packet) < ntpHeaderSize {
		return nil, ErrPacketTooShort
	}
	if len(packet) > maxUDPPacket {
		return nil, fmt.Errorf("netsec: packet larger than %d bytes", maxUDPPacket)
	}
	if len(packet)%4 != 0 {
		return nil, ErrBadAlignment
	}

	header := packet[:ntpHeaderSize]
	exts, err := ParseExtensions(packet[ntpHeaderSize:])
	if err != nil {
		return nil, err
	}
	uidField, cookieValues, auth, authOffset, err := validateStructure(packet[ntpHeaderSize:], exts)
	if err != nil {
		return nil, err
	}

	secrets, err := s.authenticate(header, packet[ntpHeaderSize:], authOffset, auth, cookieValues)
	if err != nil {
		return nil, err
	}

	uidKey := cacheKey(uidField.value)
	chosenCookieKey := cacheKey(cookieValues[0])
	s.mu.Lock()
	if s.replay.seen(uidKey) {
		s.mu.Unlock()
		return nil, ErrReplayedRequest
	}
	if s.cookies.seen(chosenCookieKey) {
		s.mu.Unlock()
		return nil, ErrReusedCookie
	}
	s.replay.add(uidKey)
	s.cookies.add(chosenCookieKey)
	s.mu.Unlock()

	return s.buildResponse(header, uidField.raw, secrets)
}

// rawField 同时保留扩展字段的原始线上字节与去填充后的取值。
type rawField struct {
	raw   []byte // 完整扩展字段（4 字节头 + 字段体 + 对齐填充）
	value []byte // 去零填充后的字段值
}

// validateStructure 强制 RFC 8915 §5.1 的扩展字段顺序与基数约束。
// 返回 UID 原始字段、全部 Cookie 值、解析后的 Authenticator 以及 Authenticator
// 在扩展区中的字节偏移（用于从原始报文切出 AAD）。
func validateStructure(extArea []byte, exts []Extension) (rawField, [][]byte, Authenticator, int, error) {
	var (
		uid          rawField
		cookieValues [][]byte
		auth         Authenticator
		authOffset   int
		uidSeen      bool
		authSeen     bool
	)
	if len(exts) == 0 {
		return rawField{}, nil, Authenticator{}, 0, ErrMissingUniqueIdentifier
	}
	offset := 0
	for i, ext := range exts {
		fieldSize := extHeaderSize + len(ext.Body)
		if !isKnownNTPExtension(ext.Type) {
			return rawField{}, nil, Authenticator{}, 0, fmt.Errorf("%w: type 0x%04x", ErrUnknownExtension, ext.Type)
		}
		switch {
		case ext.Type == ExtUniqueIdentifier:
			if i != 0 {
				return rawField{}, nil, Authenticator{}, 0, ErrUniqueIdentifierOrder
			}
			if uidSeen {
				return rawField{}, nil, Authenticator{}, 0, fmt.Errorf("%w: unique identifier", ErrDuplicateExtension)
			}
			if len(ext.Value) < MinUniqueIdentifierSize {
				return rawField{}, nil, Authenticator{}, 0, fmt.Errorf("%w: %d octets", ErrUniqueIdentifierTooShort, len(ext.Value))
			}
			uid = rawField{
				raw:   append([]byte(nil), extArea[offset:offset+fieldSize]...),
				value: ext.Value,
			}
			uidSeen = true
		case ext.Type == ExtNTSCookie:
			if authSeen {
				return rawField{}, nil, Authenticator{}, 0, ErrCookieOrder
			}
			if len(ext.Value) == 0 {
				return rawField{}, nil, Authenticator{}, 0, fmt.Errorf("%w: empty cookie", ErrUnknownCookie)
			}
			cookieValues = append(cookieValues, ext.Value)
		case ext.Type == ExtNTSAuthenticator:
			if i != len(exts)-1 {
				return rawField{}, nil, Authenticator{}, 0, ErrAuthenticatorOrder
			}
			if authSeen {
				return rawField{}, nil, Authenticator{}, 0, fmt.Errorf("%w: authenticator", ErrDuplicateExtension)
			}
			parsed, err := parseAuthenticator(ext.Body)
			if err != nil {
				return rawField{}, nil, Authenticator{}, 0, err
			}
			auth = parsed
			authSeen = true
			authOffset = offset
		}
		offset += fieldSize
	}
	if !uidSeen {
		return rawField{}, nil, Authenticator{}, 0, ErrMissingUniqueIdentifier
	}
	if len(cookieValues) == 0 {
		return rawField{}, nil, Authenticator{}, 0, ErrMissingCookie
	}
	if !authSeen {
		return rawField{}, nil, Authenticator{}, 0, ErrMissingAuthenticator
	}
	return uid, cookieValues, auth, authOffset, nil
}

// authenticate 依次尝试报文中的 Cookie：解开 Cookie 拿到流量密钥后，
// 用 C2S 密钥验证 Authenticator。AAD 直接切自原始报文，保证逐字节一致。
func (s *Server) authenticate(header, extArea []byte, authOffset int, auth Authenticator, cookieValues [][]byte) (CookieSecrets, error) {
	aad := make([]byte, ntpHeaderSize+authOffset)
	copy(aad, header)
	copy(aad[ntpHeaderSize:], extArea[:authOffset])

	var unwrapFailed bool
	var authFailed error
	for _, cookieValue := range cookieValues {
		secrets, err := s.keyRing.Unwrap(cookieValue)
		if err != nil {
			unwrapFailed = true
			continue
		}
		if auth.AEADID != secrets.AEAD {
			authFailed = fmt.Errorf("%w: aead id mismatch", ErrAuthenticationFailed)
			continue
		}
		c2s, err := NewAEAD(secrets.AEAD, secrets.C2S)
		if err != nil {
			authFailed = err
			continue
		}
		if _, err := openAuthenticator(c2s, auth, aad); err != nil {
			authFailed = err
			continue
		}
		return secrets, nil
	}
	if authFailed != nil {
		return CookieSecrets{}, authFailed
	}
	if unwrapFailed {
		return CookieSecrets{}, ErrUnknownCookie
	}
	return CookieSecrets{}, ErrAuthenticationFailed
}

// buildResponse 构造 NTS 保护的 NTP 响应：
// 原样回显 UID，补发一枚新 Cookie，并以 S2C 密钥封签 Authenticator。
func (s *Server) buildResponse(requestHeader, uidField []byte, secrets CookieSecrets) ([]byte, error) {
	now := s.now()

	respHeader := make([]byte, ntpHeaderSize)
	respHeader[0] = (4 << 3) | 4                  // VN=4, Mode=4(server)
	respHeader[1] = 1                             // Stratum=1（主参考时钟）
	copy(respHeader[24:32], requestHeader[40:48]) // Origin Timestamp 回显请求 Transmit
	copy(respHeader[32:40], ntpTimestamp(now))    // Receive Timestamp
	copy(respHeader[40:48], ntpTimestamp(now))    // Transmit Timestamp

	newCookie, err := s.keyRing.Mint(secrets)
	if err != nil {
		return nil, err
	}
	cookieField := MarshalExtension(ExtNTSCookie, newCookie)

	s2c, err := NewAEAD(secrets.AEAD, secrets.S2C)
	if err != nil {
		return nil, err
	}
	aad := make([]byte, 0, len(respHeader)+len(uidField)+len(cookieField))
	aad = append(aad, respHeader...)
	aad = append(aad, uidField...)
	aad = append(aad, cookieField...)

	// 响应的加密扩展区可为空；Authenticator 仍对完整 AAD 提供完整性保护。
	authField, err := sealAuthenticator(s2c, secrets.AEAD, aad, nil)
	if err != nil {
		return nil, err
	}

	response := make([]byte, 0, len(aad)+len(authField))
	response = append(response, aad...)
	response = append(response, authField...)
	return response, nil
}

// ntpTimestamp 把 Go 时间编码为 64 位 NTP 时间戳（32 位秒 + 32 位小数）。
func ntpTimestamp(t time.Time) []byte {
	seconds := uint32(t.Unix() + ntpEpochOffset)
	fraction := uint32(uint64(t.Nanosecond()) * (1 << 32) / 1_000_000_000)
	out := make([]byte, 8)
	binary.BigEndian.PutUint32(out[0:4], seconds)
	binary.BigEndian.PutUint32(out[4:8], fraction)
	return out
}

// ServeUDP 在已绑定的 UDP socket 上循环处理 NTS/NTP 请求。
// 校验失败的请求被静默丢弃（不回复时间），函数仅在 Read 出错时返回。
func (s *Server) ServeUDP(conn *net.UDPConn) error {
	buffer := make([]byte, maxUDPPacket)
	for {
		n, addr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		packet := append([]byte(nil), buffer[:n]...)
		response, err := s.ProcessPacket(packet)
		if err != nil {
			continue // 认证/重放/复用失败：不回复时间
		}
		if _, err := conn.WriteToUDP(response, addr); err != nil {
			return err
		}
	}
}
