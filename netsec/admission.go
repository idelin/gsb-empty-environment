// Package netsec 为 Go MySQL 客户端提供可导入的握手准入（admission）控制，
// 用于抵御针对 MySQL 明文握手阶段的两类中间人攻击：
//
//   - TLS 能力位剥离：中间人抹掉服务端 InitialHandshakePacket 中的
//     CLIENT_SSL 能力位，诱使客户端退回明文认证；
//   - SSLRequest 后夹带明文：中间人在客户端发出 SSLRequest 之后、TLS
//     握手完成之前塞入明文字节（例如伪造的错误包或继续握手的指示），
//     试图让客户端在未加密通道上直接发送认证响应。
//
// 准入器保证：配置要求 TLS 时，服务端能力位缺失 CLIENT_SSL 立即中止；
// 32 字节 SSLRequest 发出后、TLS 握手完成前，连接读缓冲中出现任何服务端
// 字节都立即中止；认证响应（HandshakeResponse）只能在 TLS 建立后发送；
// auth_plugin_data 在明文阶段不参与任何出站报文。
package netsec

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"time"
)

// 默认窥探超时：SSLRequest 发出后等待一小段时间，确认对端没有抢先
// 夹带明文字节。ProbeTimeout 设为负值可做非阻塞的即时检测。
const defaultProbeTimeout = 25 * time.Millisecond

var (
	// ErrTLSRequired 配置要求 TLS 但服务端握手包未置 CLIENT_SSL 能力位。
	ErrTLSRequired = errors.New("netsec: TLS required but server omitted CLIENT_SSL capability")
	// ErrPrematureBytes SSLRequest 发出后、TLS 建立前读到了服务端字节。
	// 正常 MySQL 服务端在此阶段必须保持静默，任何字节都属于协议违规。
	ErrPrematureBytes = errors.New("netsec: server sent bytes before TLS handshake completed")
	// ErrOutOfOrderPacket 包序号与协议期望不符（例如初始包序号不是 0）。
	ErrOutOfOrderPacket = errors.New("netsec: out-of-order packet sequence")
	// ErrTLSNotEstablished 试图在 TLS 握手成功前取得认证材料。
	ErrTLSNotEstablished = errors.New("netsec: TLS not established, auth material sealed")
)

// Config 描述握手准入策略。
type Config struct {
	// TLSConfig 为 TLS 客户端配置；ServerName 通常应设为目标主机名。
	TLSConfig *tls.Config
	// RequireTLS 为 true 时，服务端缺失 CLIENT_SSL 能力位将直接拒绝连接。
	RequireTLS bool
	// ProbeTimeout 是 SSLRequest 后检测提前字节的等待窗口；零值取默认 25ms，
	// 负值表示非阻塞探测（只检查已到达内核/缓冲的数据，立即超时返回）。
	ProbeTimeout time.Duration
	// Charset 写入 SSLRequest 的字符集，零值使用 utf8mb4(45)。
	Charset byte
	// MaxPacketSize 写入 SSLRequest 的最大包长，零值使用 16MiB-1。
	MaxPacketSize uint32
}

// PendingHandshake 是已通过准入校验、但尚未升级到 TLS 的明文连接状态。
// 该状态下认证材料处于“封存”状态，无法用于构造任何出站报文。
type PendingHandshake struct {
	conn         *bufferedConn
	scramble     []byte
	serverCaps   uint32
	charset      byte
	maxPacket    uint32
	probeTimeout time.Duration
	tlsConfig    *tls.Config
}

// SecureConn 是 TLS 建立成功后的安全连接。此时才允许读取认证材料并
// 发送认证响应。
type SecureConn struct {
	*tls.Conn
	serverCaps uint32
	scramble   []byte
}

// bufferedConn 在调用方连接前维护一个仅用于读取的自有预读缓冲。
// 与 bufio.Reader 不同，它在底层读取超时但已拿到字节时会保留这些字节，
// 不会因截止时间丢弃已到达的服务端字节，因此可安全用于“窥探后交给 TLS”。
// 写入不做缓冲，直接透传给外层连接（可能带审计/记录包装）。
type bufferedConn struct {
	outer  net.Conn
	prebuf []byte
}

// Read 优先排空预读缓冲，再读取外层连接。
func (c *bufferedConn) Read(p []byte) (int, error) {
	if len(c.prebuf) > 0 {
		n := copy(p, c.prebuf)
		c.prebuf = c.prebuf[n:]
		return n, nil
	}
	return c.outer.Read(p)
}

// Write 直接透传，保证调用方的记录/审计包装能观察到所有出站字节。
func (c *bufferedConn) Write(p []byte) (int, error)        { return c.outer.Write(p) }
func (c *bufferedConn) Close() error                       { return c.outer.Close() }
func (c *bufferedConn) LocalAddr() net.Addr                { return c.outer.LocalAddr() }
func (c *bufferedConn) RemoteAddr() net.Addr               { return c.outer.RemoteAddr() }
func (c *bufferedConn) SetDeadline(t time.Time) error      { return c.outer.SetDeadline(t) }
func (c *bufferedConn) SetReadDeadline(t time.Time) error  { return c.outer.SetReadDeadline(t) }
func (c *bufferedConn) SetWriteDeadline(t time.Time) error { return c.outer.SetWriteDeadline(t) }

// Peek 在给定等待窗口内探测是否已有服务端字节，但不消费任何数据：
//   - wait > 0：等待该窗口，窗口结束仍无数据则放行；
//   - wait <= 0：非阻塞探测，只检查此刻已到达的字节，立即返回。
//
// 数据读入自有缓冲并保留，后续 TLS 握手通过 Read 仍能拿到完整记录。
func (c *bufferedConn) Peek(wait time.Duration) error {
	if len(c.prebuf) > 0 {
		return ErrPrematureBytes
	}
	// 注意：SetReadDeadline 的零值表示“无截止时间”，因此非阻塞探测
	// （wait <= 0）也要显式使用 time.Now() 制造一个即刻到期的截止时间。
	deadline := time.Now()
	if wait > 0 {
		deadline = time.Now().Add(wait)
	}
	if err := c.outer.SetReadDeadline(deadline); err != nil {
		return err
	}
	tmp := make([]byte, 512)
	n, err := c.outer.Read(tmp)
	// 恢复为阻塞读取，由后续 TLS 握手自行管理截止时间。
	_ = c.outer.SetReadDeadline(time.Time{})
	if n > 0 {
		// 即便伴随超时/EOF，只要拿到字节就是明文阶段的协议违规，
		// 字节仍保留下来供调用方诊断，绝不静默丢弃。
		c.prebuf = append(c.prebuf, tmp[:n]...)
		return ErrPrematureBytes
	}
	if err == nil {
		return ErrPrematureBytes
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return nil
	}
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
		return ErrPrematureBytes
	}
	return err
}

// Begin 读取并校验服务端初始握手包，但不发送任何认证相关数据。
// 成功后返回 PendingHandshake，调用方随后必须调用 Upgrade 完成 TLS。
func Begin(raw net.Conn, cfg Config) (*PendingHandshake, error) {
	if cfg.RequireTLS && cfg.TLSConfig == nil {
		return nil, errors.New("netsec: RequireTLS set but TLSConfig is nil")
	}

	conn := &bufferedConn{outer: raw}

	payload, seq, err := readPacket(conn)
	if err != nil {
		return nil, err
	}
	// 初始握手包必须是连接上的第 0 号包。
	if seq != 0 {
		return nil, errors.Join(ErrOutOfOrderPacket,
			errors.New("netsec: initial handshake sequence must be 0"))
	}
	// 初始握手包后不得再夹带额外明文包（中间人可能追加伪造指令）。
	if err := conn.Peek(0); err != nil {
		if errors.Is(err, ErrPrematureBytes) {
			return nil, errors.Join(ErrPrematureBytes,
				errors.New("netsec: extra packet appended to initial handshake"))
		}
		return nil, err
	}

	hs, err := parseInitialHandshake(payload)
	if err != nil {
		return nil, err
	}

	if cfg.RequireTLS && hs.capabilities&capClientSSL == 0 {
		return nil, ErrTLSRequired
	}

	charset := cfg.Charset
	if charset == 0 {
		charset = defaultCharset
	}
	maxPacket := cfg.MaxPacketSize
	if maxPacket == 0 {
		maxPacket = defaultMaxPacketSize
	}
	probe := cfg.ProbeTimeout
	if probe == 0 {
		probe = defaultProbeTimeout
	}

	return &PendingHandshake{
		conn:         conn,
		scramble:     hs.scramble,
		serverCaps:   hs.capabilities,
		charset:      charset,
		maxPacket:    maxPacket,
		probeTimeout: probe,
		tlsConfig:    cfg.TLSConfig,
	}, nil
}

// ServerCapabilities 回传服务端宣告的原始能力位，仅供驱动在 TLS 建立后
// 协商 HandshakeResponse 使用。
func (p *PendingHandshake) ServerCapabilities() uint32 { return p.serverCaps }

// AuthPluginData 在明文阶段返回 ErrTLSNotEstablished，强制认证材料
// 只能在 SecureConn 上获取。
func (p *PendingHandshake) AuthPluginData() ([]byte, error) {
	return nil, ErrTLSNotEstablished
}

// Upgrade 发送 32 字节 SSLRequest，确认无提前字节后执行 TLS 握手。
// 返回 SecureConn 之前，连接上唯一的明文出站报文就是该 SSLRequest。
func (p *PendingHandshake) Upgrade(ctx context.Context) (*SecureConn, error) {
	// 客户端能力位必须是服务端支持能力的子集，并包含 CLIENT_SSL。
	caps := p.serverCaps & clientCapabilityMask
	if caps&capClientSSL == 0 {
		return nil, ErrTLSRequired
	}

	// SSLRequest 是初始包之后的第 1 号包，固定 32 字节载荷（36 字节上线）。
	req := buildSSLRequest(caps, p.charset, p.maxPacket)
	if err := writePacket(p.conn, 1, req); err != nil {
		return nil, err
	}

	// 关键防御点：TLS ClientHello 发出前，对端必须保持静默。
	if err := p.conn.Peek(p.probeTimeout); err != nil {
		return nil, err
	}

	tlsCfg := p.tlsConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		cp := tlsCfg.Clone()
		if cp.MinVersion == 0 {
			cp.MinVersion = tls.VersionTLS12
		}
		tlsCfg = cp
	}

	tlsConn := tls.Client(p.conn, tlsCfg)
	if ctx == nil {
		ctx = context.Background()
	}
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = p.conn.Close()
		return nil, err
	}

	return &SecureConn{
		Conn:       tlsConn,
		serverCaps: p.serverCaps,
		// 副本交付，调用方无法篡改准入器内部状态。
		scramble: append([]byte(nil), p.scramble...),
	}, nil
}

// ServerCapabilities 返回服务端能力位，供加密通道上的能力协商使用。
func (s *SecureConn) ServerCapabilities() uint32 { return s.serverCaps }

// AuthPluginData 返回初始握手中的 auth_plugin_data（scramble）。
// 该数据仅在 TLS 建立后可用，且只应写入随后的加密 HandshakeResponse。
func (s *SecureConn) AuthPluginData() []byte {
	return append([]byte(nil), s.scramble...)
}

// Admit 是 Begin + Upgrade 的便捷封装，返回可直接发送认证响应的安全连接。
func Admit(ctx context.Context, conn net.Conn, cfg Config) (*SecureConn, error) {
	pending, err := Begin(conn, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return pending.Upgrade(ctx)
}
