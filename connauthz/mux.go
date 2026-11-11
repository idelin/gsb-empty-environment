package connauthz

import "context"

// Stream 表示多路复用连接上的一条已打开的流。
type Stream interface {
	// Close 关闭流。
	Close() error
}

// Mux 是服务端底层多路复用器的最小抽象（如 HTTP/2、gRPC、QUIC、yamux 等）。
type Mux interface {
	// OpenStream 在底层连接上创建一条新流。
	OpenStream(ctx context.Context) (Stream, error)
}

// AuthorizedMux 在每次开流前按最新一代策略复验连接证书链。
//
// 授权判定只作用于“开流”时刻：已打开的流不会因为后续 Rotate 而被关闭，
// 旧流按其打开时的既有决定继续运行直至自行结束。
type AuthorizedMux struct {
	mux        Mux
	authorizer *Authorizer
	conn       *Connection
}

// NewAuthorizedMux 包装底层 Mux。conn 为握手时记录的证书链。
func NewAuthorizedMux(mux Mux, authorizer *Authorizer, conn *Connection) *AuthorizedMux {
	return &AuthorizedMux{mux: mux, authorizer: authorizer, conn: conn}
}

// OpenStream 先复验、复验通过才调用底层 Mux 创建流。
// 拒绝时返回 *DenialError（可通过 errors.As 获取代次与原因），不会触碰底层连接。
func (g *AuthorizedMux) OpenStream(ctx context.Context) (Stream, error) {
	decision, err := g.conn.Authorize(ctx, g.authorizer)
	if err != nil {
		return nil, err
	}
	if err := decision.Err(); err != nil {
		return nil, err
	}
	return g.mux.OpenStream(ctx)
}
