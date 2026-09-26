package sessionkv

// WriteRecord 记录会话内一次写及其确认（供独立检查器使用）。
type WriteRecord struct {
	Key   string
	Value string
	Res   *WriteResult
}

// ReadRecord 记录会话内一次读、请求时的令牌要求及结果（供独立检查器使用）。
type ReadRecord struct {
	Key      string
	Required Vector
	Res      *ReadResult
}

// Client 绑定一个会话，持有该会话的版本令牌。
// 令牌随会话走：会话迁移到另一节点时只需在新节点上出示同一令牌。
type Client struct {
	sim       *Simulator
	SessionID string
	token     Token
	Writes    []*WriteRecord
	Reads     []*ReadRecord
}

// NewClient 创建会话客户端。
func NewClient(sim *Simulator, sessionID string) *Client {
	return &Client{sim: sim, SessionID: sessionID, token: NewToken(sessionID)}
}

// Token 返回当前会话令牌的拷贝（可用于会话迁移）。
func (c *Client) Token() Token {
	return Token{SessionID: c.SessionID, Vector: c.token.Vector.Copy()}
}

// Migrate 生成同会话的新客户端：会话迁移，令牌随之转移，无需全局同步。
func (c *Client) Migrate(sim *Simulator) *Client {
	return &Client{sim: sim, SessionID: c.SessionID, token: c.Token()}
}

// Write 在 node 上写 key；写确认向量到达后并入会话令牌。
func (c *Client) Write(n *Node, key, value string, timeout int64) *WriteResult {
	rec := &WriteRecord{Key: key, Value: value}
	rec.Res = n.WriteAsync(key, value, c.token.Vector, timeout, func(r *WriteResult) {
		if r.Err == nil {
			c.token.Vector.Merge(r.Version)
		}
	})
	c.Writes = append(c.Writes, rec)
	return rec.Res
}

// Read 在 node 上读 key，要求快照覆盖本会话令牌；
// 超时结果的 Err 为 ErrNotSatisfied（显式标记未满足一致性）。
func (c *Client) Read(n *Node, key string, timeout int64) *ReadResult {
	return c.ReadWithToken(n, key, c.token, timeout)
}

// ReadWithToken 用显式令牌读；令牌必须属于本会话，否则立即拒绝——
// 不同会话的令牌不混用。
func (c *Client) ReadWithToken(n *Node, key string, token Token, timeout int64) *ReadResult {
	if err := token.Check(c.SessionID); err != nil {
		return &ReadResult{Done: true, Err: err}
	}
	rec := &ReadRecord{Key: key, Required: token.Vector.Copy()}
	rec.Res = n.ReadAsync(key, token, timeout, func(r *ReadResult) {
		if r.Err == nil {
			c.token.Vector.Merge(r.Version)
		}
	})
	c.Reads = append(c.Reads, rec)
	return rec.Res
}
