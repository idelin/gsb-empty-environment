package session

import "errors"

// 显式错误：任何一致性未满足的情况都必须以错误形式暴露，禁止静默降级。
var (
	// ErrConsistencyTimeout 表示在超时前副本未达到令牌要求的因果版本。
	ErrConsistencyTimeout = errors.New("consistency timeout: token requirements not met")
	// ErrNodeDown 表示目标节点不可用。
	ErrNodeDown = errors.New("node is down")
	// ErrTokenMismatch 表示令牌被非所属会话使用（不同会话令牌不混用）。
	ErrTokenMismatch = errors.New("session token mismatch")
	// ErrNoResponse 表示事件模拟器耗尽事件仍未得到响应。
	ErrNoResponse = errors.New("no response within simulated time")
)

// Token 是会话版本令牌：跨键的因果版本向量集合。
// 读请求只有在副本对令牌中每一个键的版本都被满足时才可返回，
// 因此不会返回"本键版本够、但缺少其因果依赖"的快照。
type Token struct {
	SessionID string
	Versions  map[string]VersionVector // key -> 该键所需的最小因果版本
}

// NewToken 为给定会话创建空令牌。
func NewToken(sessionID string) Token {
	return Token{SessionID: sessionID, Versions: map[string]VersionVector{}}
}

// Copy 返回深拷贝，用于会话迁移时携带令牌。
func (t Token) Copy() Token {
	out := Token{SessionID: t.SessionID, Versions: make(map[string]VersionVector, len(t.Versions))}
	for k, v := range t.Versions {
		out.Versions[k] = v.Copy()
	}
	return out
}

// MergeVersion 把一次写确认或读结果携带的版本并入令牌。
func (t *Token) MergeVersion(key string, vv VersionVector) {
	cur, ok := t.Versions[key]
	if !ok {
		t.Versions[key] = vv.Copy()
		return
	}
	t.Versions[key] = Merge(cur, vv)
}
