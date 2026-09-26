package sessionkv

import "errors"

// ErrTokenSessionMismatch 表示令牌被用于不属于它的会话。
var ErrTokenSessionMismatch = errors.New("sessionkv: token belongs to a different session")

// Token 是会话版本令牌：绑定会话ID，携带该会话已观察到的跨键因果版本向量。
// 读请求必须返回覆盖令牌向量的快照，否则显式失败，禁止静默降级。
type Token struct {
	SessionID string
	Vector    Vector
}

// NewToken 为会话创建空令牌。
func NewToken(sessionID string) Token {
	return Token{SessionID: sessionID, Vector: Vector{}}
}

// Check 校验令牌归属；不同会话的令牌禁止混用。
func (t Token) Check(sessionID string) error {
	if t.SessionID != sessionID {
		return ErrTokenSessionMismatch
	}
	return nil
}
