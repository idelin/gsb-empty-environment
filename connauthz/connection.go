package connauthz

import (
	"context"
	"crypto/tls"
	"strconv"
)

// Connection 记录一条已建立连接在 TLS 握手时出示的证书链（DER，叶子在前）。
type Connection struct {
	rawChain [][]byte
}

// ConnectionFromTLS 从 TLS 连接状态捕获握手证书链并做防御性拷贝。
// 握手阶段只记录原始 DER，复验时才解析，避免持有可变的解析结果。
func ConnectionFromTLS(state tls.ConnectionState) *Connection {
	if len(state.PeerCertificates) == 0 {
		return &Connection{}
	}
	raw := make([][]byte, len(state.PeerCertificates))
	for i, cert := range state.PeerCertificates {
		raw[i] = append([]byte(nil), cert.Raw...)
	}
	return &Connection{rawChain: raw}
}

// RawChain 返回记录的 DER 证书链拷贝。
func (c *Connection) RawChain() [][]byte {
	out := make([][]byte, len(c.rawChain))
	for i, der := range c.rawChain {
		out[i] = append([]byte(nil), der...)
	}
	return out
}

// Authorize 用最新策略对该连接复验，等价于 Authorizer.Authorize(ctx, c.RawChain())。
func (c *Connection) Authorize(ctx context.Context, a *Authorizer) (*Decision, error) {
	return a.Authorize(ctx, c.RawChain())
}

func uitoa(n uint64) string { return strconv.FormatUint(n, 10) }

func itoa(n int) string { return strconv.Itoa(n) }
