package session

// Session 是客户端会话：持有跨键因果令牌，可在节点间迁移。
type Session struct {
	ID    string
	Token Token
	sim   *Simulator
}

// NewSession 创建会话。
func NewSession(sim *Simulator, id string) *Session {
	return &Session{ID: id, Token: NewToken(id), sim: sim}
}

// Migrate 返回同一会话在新客户端侧的句柄：令牌随会话迁移，
// 在另一个节点上的读写继续使用同一令牌（会话迁移）。
func (s *Session) Migrate() *Session {
	return &Session{ID: s.ID, Token: s.Token.Copy(), sim: s.sim}
}

// Write 在指定节点写入并等待确认；确认版本并入会话令牌（读己之写的基础）。
func (s *Session) Write(nodeID, key, value string) (VersionVector, error) {
	n := s.sim.Node(nodeID)
	var vv VersionVector
	var err error
	done := false
	n.ClientWrite(s, key, value, func(v VersionVector, e error) {
		vv, err, done = v, e, true
	})
	s.sim.RunUntil(func() bool { return done }, s.sim.Now+1)
	if !done {
		return nil, ErrNoResponse
	}
	if err == nil {
		s.Token.MergeVersion(key, vv)
	}
	return vv, err
}

// Read 在指定节点读取：副本达到令牌版本则返回，否则等待至超时；
// 超时返回 ErrConsistencyTimeout（显式标记一致性未满足），绝不返回旧值。
// 读成功后将所见版本并入令牌（单调读的基础）。
func (s *Session) Read(nodeID, key string, timeout int64) (string, error) {
	n := s.sim.Node(nodeID)
	var reply ReadReply
	done := false
	n.ClientRead(s, key, timeout, func(r ReadReply) { reply, done = r, true })
	limit := s.sim.Now + timeout + 1
	s.sim.RunUntil(func() bool { return done }, limit)
	if !done {
		return "", ErrNoResponse
	}
	if reply.Err == nil {
		s.Token.MergeVersion(key, reply.Ver)
	}
	return reply.Value, reply.Err
}
