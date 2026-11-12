package netsec

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
)

// 升级策略错误。升级失败时连接一律被关闭，绝不回退到明文会话。
var (
	// ErrNoSTLS 表示 CAPA 应答中没有 STLS 能力。
	ErrNoSTLS = errors.New("pop3: server does not advertise STLS")
	// ErrSTLSRefused 表示服务器对 STLS 命令返回了 -ERR。
	ErrSTLSRefused = errors.New("pop3: server refused STLS")
	// ErrInjectedBytes 表示 STLS 的 +OK 之后读缓冲里仍有未交给 TLS 的明文字节。
	ErrInjectedBytes = errors.New("pop3: unexpected plaintext bytes buffered after STLS +OK")
)

// Client 封装连接建立阶段的 POP3 会话：问候、CAPA、STLS 升级与认证方式选择。
type Client struct {
	conn   net.Conn
	reader *bufio.Reader
	caps   map[string][]string
	tlsOn  bool
}

// NewClient 读取服务器问候并返回待升级的客户端。
func NewClient(conn net.Conn) (*Client, error) {
	c := &Client{
		conn:   conn,
		reader: bufio.NewReader(conn),
	}
	line, err := c.readLine()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("pop3: reading greeting: %w", err)
	}
	if !strings.HasPrefix(line, "+OK") {
		conn.Close()
		return nil, fmt.Errorf("pop3: bad greeting: %q", line)
	}
	return c, nil
}

// Caps 返回当前生效的能力列表。升级成功后其中只包含握手之后重新获取的能力。
func (c *Client) Caps() map[string][]string {
	out := make(map[string][]string, len(c.caps))
	for k, v := range c.caps {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// Encrypted 报告会话是否已完成 TLS 升级。
func (c *Client) Encrypted() bool { return c.tlsOn }

// AuthMethods 依据当前能力列表给出可选认证方式：USER 明文口令或 SASL 机制。
// 升级之后该列表只反映握手后重新获取的 CAPA 应答。
func (c *Client) AuthMethods() []string {
	var methods []string
	if _, ok := c.caps["USER"]; ok {
		methods = append(methods, "USER")
	}
	for _, mech := range c.caps["SASL"] {
		methods = append(methods, "SASL "+mech)
	}
	return methods
}

// Close 关闭底层连接。
func (c *Client) Close() error { return c.conn.Close() }

// Upgrade 执行强制加密的 STLS 升级流程：
//  1. 发送 CAPA，解析以单独一行 "." 结束的多行应答；无 STLS 能力即失败；
//  2. 发送 STLS，-ERR 即失败，绝不回退明文登录；
//  3. +OK 之后若读缓冲中还有未交给 TLS 的字节，视为注入并断开；
//  4. 完成 TLS 握手后丢弃此前全部能力，重新发送 CAPA，
//     之后的认证方式选择只依据新的能力列表。
//
// 任何一步失败都会关闭连接。
func (c *Client) Upgrade(config *tls.Config) error {
	if c.tlsOn {
		return errors.New("pop3: connection already upgraded")
	}
	if err := c.fetchCaps(); err != nil {
		c.conn.Close()
		return err
	}
	if _, ok := c.caps["STLS"]; !ok {
		c.conn.Close()
		return ErrNoSTLS
	}
	if err := c.writeLine("STLS"); err != nil {
		c.conn.Close()
		return fmt.Errorf("pop3: sending STLS: %w", err)
	}
	line, err := c.readLine()
	if err != nil {
		c.conn.Close()
		return fmt.Errorf("pop3: reading STLS reply: %w", err)
	}
	if strings.HasPrefix(line, "-ERR") {
		c.conn.Close()
		return ErrSTLSRefused
	}
	if !strings.HasPrefix(line, "+OK") {
		c.conn.Close()
		return fmt.Errorf("pop3: unexpected STLS reply: %q", line)
	}
	// 攻击者可能在 +OK 同一段明文里夹带字节，试图被当成 TLS 流或
	// 后续命令的一部分。凡是还没交给 TLS 握手的缓冲字节都视为注入。
	if c.reader.Buffered() > 0 {
		c.conn.Close()
		return ErrInjectedBytes
	}

	tlsConn := tls.Client(c.conn, config)
	if err := tlsConn.Handshake(); err != nil {
		c.conn.Close()
		return fmt.Errorf("pop3: TLS handshake: %w", err)
	}
	c.conn = tlsConn
	c.reader = bufio.NewReader(tlsConn)
	c.tlsOn = true
	// 握手前的能力列表可能来自攻击者，全部丢弃并以加密通道内
	// 重新获取的列表为准。
	c.caps = nil
	if err := c.fetchCaps(); err != nil {
		c.conn.Close()
		return err
	}
	return nil
}

// fetchCaps 发送 CAPA 并解析多行应答，替换当前能力列表。
func (c *Client) fetchCaps() error {
	if err := c.writeLine("CAPA"); err != nil {
		return fmt.Errorf("pop3: sending CAPA: %w", err)
	}
	line, err := c.readLine()
	if err != nil {
		return fmt.Errorf("pop3: reading CAPA reply: %w", err)
	}
	if !strings.HasPrefix(line, "+OK") {
		return fmt.Errorf("pop3: CAPA refused: %q", line)
	}
	caps := make(map[string][]string)
	for {
		line, err := c.readLine()
		if err != nil {
			return fmt.Errorf("pop3: reading CAPA list: %w", err)
		}
		if line == "." {
			break
		}
		// RFC 1939 字节填充：以 ".." 开头的行去掉一个点。
		line = strings.TrimPrefix(line, "..")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		caps[strings.ToUpper(fields[0])] = fields[1:]
	}
	c.caps = caps
	return nil
}

func (c *Client) readLine() (string, error) {
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (c *Client) writeLine(line string) error {
	_, err := c.conn.Write([]byte(line + "\r\n"))
	return err
}
