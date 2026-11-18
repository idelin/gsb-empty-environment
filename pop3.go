package netsec

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"strings"
)

var (
	// ErrTLSRequired 表示服务器未在 CAPA 中提供 STLS，而策略要求必须加密。
	ErrTLSRequired = errors.New("pop3: server does not offer STLS and encryption is mandatory")
	// ErrSTLSRefused 表示服务器对 STLS 回了 -ERR，禁止回退为明文登录。
	ErrSTLSRefused = errors.New("pop3: server refused STLS")
	// ErrInjectedBytes 表示 STLS 的 +OK 之后读缓冲中仍有未交给 TLS 的明文字节。
	ErrInjectedBytes = errors.New("pop3: unexpected buffered plaintext after STLS +OK, possible injection")
)

// Client 是一个 POP3 客户端连接，供连接建立流程调用 Upgrade 完成强制加密升级。
type Client struct {
	conn net.Conn
	br   *bufio.Reader
	tr   *textproto.Reader
	caps map[string][]string
	tls  bool
}

// NewClient 读取服务器问候并返回客户端。
func NewClient(conn net.Conn) (*Client, error) {
	c := &Client{conn: conn}
	c.resetIO(conn)
	line, err := c.tr.ReadLine()
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

func (c *Client) resetIO(conn net.Conn) {
	c.br = bufio.NewReader(conn)
	c.tr = textproto.NewReader(c.br)
}

func (c *Client) cmd(command string) (string, error) {
	if _, err := fmt.Fprintf(c.conn, "%s\r\n", command); err != nil {
		return "", err
	}
	return c.tr.ReadLine()
}

// Capabilities 发送 CAPA 并解析多行应答，应答以单独一行 "." 结束。
func (c *Client) Capabilities() (map[string][]string, error) {
	line, err := c.cmd("CAPA")
	if err != nil {
		return nil, fmt.Errorf("pop3: CAPA: %w", err)
	}
	if !strings.HasPrefix(line, "+OK") {
		return nil, fmt.Errorf("pop3: CAPA refused: %q", line)
	}
	lines, err := c.tr.ReadDotLines()
	if err != nil {
		return nil, fmt.Errorf("pop3: reading CAPA list: %w", err)
	}
	caps := make(map[string][]string, len(lines))
	for _, l := range lines {
		fields := strings.Fields(l)
		if len(fields) == 0 {
			continue
		}
		caps[strings.ToUpper(fields[0])] = fields[1:]
	}
	return caps, nil
}

// Upgrade 执行强制 STLS 升级：
//  1. CAPA 中没有 STLS 即失败，不允许明文继续；
//  2. STLS 得到 -ERR 即失败并断开，不回退为明文登录；
//  3. +OK 之后若读缓冲里还有未交给 TLS 的字节，视为注入并断开；
//  4. 握手完成后丢弃此前全部能力，重新发 CAPA，仅依据新列表选择认证方式。
func (c *Client) Upgrade(config *tls.Config) error {
	if c.tls {
		return errors.New("pop3: connection is already encrypted")
	}
	caps, err := c.Capabilities()
	if err != nil {
		c.conn.Close()
		return err
	}
	if _, ok := caps["STLS"]; !ok {
		c.conn.Close()
		return ErrTLSRequired
	}
	line, err := c.cmd("STLS")
	if err != nil {
		c.conn.Close()
		return fmt.Errorf("pop3: STLS: %w", err)
	}
	if !strings.HasPrefix(line, "+OK") {
		c.conn.Close()
		return fmt.Errorf("%w: %q", ErrSTLSRefused, line)
	}
	if c.br.Buffered() > 0 {
		c.conn.Close()
		return ErrInjectedBytes
	}
	tlsConn := tls.Client(c.conn, config)
	if err := tlsConn.Handshake(); err != nil {
		c.conn.Close()
		return fmt.Errorf("pop3: TLS handshake: %w", err)
	}
	c.conn = tlsConn
	c.resetIO(tlsConn)
	c.tls = true
	c.caps = nil
	caps, err = c.Capabilities()
	if err != nil {
		c.conn.Close()
		return err
	}
	c.caps = caps
	return nil
}

// HasCapability 报告握手后重新获取的能力列表中是否存在指定能力。
func (c *Client) HasCapability(name string) bool {
	_, ok := c.caps[strings.ToUpper(name)]
	return ok
}

// SelectAuth 只依据握手后重新获取的能力列表选择认证方式。
func (c *Client) SelectAuth() (string, error) {
	if !c.tls {
		return "", errors.New("pop3: refusing to select authentication before TLS")
	}
	for _, mech := range []string{"APOP", "USER"} {
		if c.HasCapability(mech) {
			return mech, nil
		}
	}
	return "", errors.New("pop3: no usable authentication mechanism in post-TLS capabilities")
}

// Close 关闭底层连接。
func (c *Client) Close() error {
	return c.conn.Close()
}
