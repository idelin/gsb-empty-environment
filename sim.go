package leasesim

import "fmt"

// Sim 是单机离散 tick 模拟器。
type Sim struct {
	cfg      Config
	nodes    []*Node
	tick     int
	queue    []Message
	scenario map[int][]func(*Sim)
	spike    *Spike

	Events    []Event
	AfterTick func(*Sim) // 可选：每个 tick 结束后的钩子（测试用于检查不变式）
}

// NewSim 创建模拟器，所有节点初始为 Follower、纪元 0。
func NewSim(cfg Config) *Sim {
	s := &Sim{cfg: cfg, scenario: map[int][]func(*Sim){}}
	for i := 0; i < cfg.NumNodes; i++ {
		s.nodes = append(s.nodes, &Node{
			ID: i, Role: Follower,
			votedFor: -1, leader: -1, lastLeaderContact: -1,
			votes: map[int]bool{}, ackTick: map[int]int{},
		})
	}
	return s
}

// Tick 返回当前全局 tick。
func (s *Sim) Tick() int { return s.tick }

// Node 返回指定节点。
func (s *Sim) Node(id int) *Node { return s.nodes[id] }

// Nodes 返回全部节点。
func (s *Sim) Nodes() []*Node { return s.nodes }

// local 返回节点的本地时钟（全局 tick + 有界偏移）。
func (s *Sim) local(n *Node) int {
	off := 0
	if s.cfg.ClockOffset != nil {
		off = s.cfg.ClockOffset[n.ID]
	}
	return s.tick + off
}

func (s *Sim) log(n *Node, from int, kind, format string, args ...interface{}) {
	s.Events = append(s.Events, Event{
		Tick: s.tick, Node: n.ID, From: from, Kind: kind,
		Detail: fmt.Sprintf(format, args...),
	})
}

// At 在指定 tick 注册场景动作（如触发选举、注入黑洞、发起写入）。
func (s *Sim) At(tick int, fn func(*Sim)) {
	s.scenario[tick] = append(s.scenario[tick], fn)
}

// SetSpike 注入网络黑洞/延迟突增。
func (s *Sim) SetSpike(sp Spike) {
	s.spike = &sp
	s.log(s.nodes[sp.Node], -1, "spike",
		"network blackhole around node %d: +%d ticks delay during [%d,%d)", sp.Node, sp.Extra, sp.Start, sp.End)
}

// Run 运行模拟直到指定 tick（含）。
func (s *Sim) Run(until int) {
	for ; s.tick <= until; s.tick++ {
		for _, fn := range s.scenario[s.tick] {
			fn(s)
		}
		s.deliver()
		for _, n := range s.nodes {
			s.onTimer(n)
		}
		if s.AfterTick != nil {
			s.AfterTick(s)
		}
	}
}

// send 在 dispatch tick 发出报文，LinkDelay 后送达。
func (s *Sim) send(from, to int, kind MsgKind, epoch, dispatch int) {
	s.queue = append(s.queue, Message{
		From: from, To: to, Kind: kind, Epoch: epoch,
		DeliverTick: dispatch + s.cfg.LinkDelay,
	})
}

// broadcast 向除自己外的所有节点广播。
func (s *Sim) broadcast(from *Node, kind MsgKind, dispatch int) {
	for _, n := range s.nodes {
		if n.ID != from.ID {
			s.send(from.ID, n.ID, kind, from.Epoch, dispatch)
		}
	}
}

// deliver 送达本 tick 到期的报文；被黑洞波及的报文改期重投。
func (s *Sim) deliver() {
	var due, pending []Message
	for _, m := range s.queue {
		if m.DeliverTick == s.tick {
			due = append(due, m)
		} else {
			pending = append(pending, m)
		}
	}
	s.queue = pending
	for _, m := range due {
		if sp := s.spike; sp != nil && s.tick >= sp.Start && s.tick < sp.End &&
			(m.From == sp.Node || m.To == sp.Node) {
			m.DeliverTick = s.tick + sp.Extra
			s.queue = append(s.queue, m)
			s.log(s.nodes[m.To], m.From, "delayed",
				"%s swallowed by blackhole, reemerges at tick %d", m.Kind, m.DeliverTick)
			continue
		}
		s.onMessage(s.nodes[m.To], m)
	}
}

// onMessage 处理送达的报文；应答在 ProcessingDelay 后发出。
func (s *Sim) onMessage(n *Node, m Message) {
	lt := s.local(n)
	switch m.Kind {
	case MsgLeaseRequest:
		s.onLeaseRequest(n, m, lt)
	case MsgLeaseGrant:
		if n.Role == Candidate && m.Epoch == n.Epoch {
			n.votes[m.From] = true
			if len(n.votes) >= s.cfg.Majority() {
				n.Role = Primary
				n.leaseStart = lt
				n.lastRenewal = lt
				n.ackTick = map[int]int{}
				s.log(n, -1, "primary",
					"epoch=%d lease granted by majority (%d/%d), serves until tick %d",
					n.Epoch, len(n.votes), s.cfg.NumNodes,
					lt+s.cfg.LeaseDuration-s.cfg.DriftBound)
			}
		}
	case MsgHeartbeat:
		if m.Epoch < n.Epoch {
			s.log(n, m.From, "hb_rejected",
				"stale heartbeat epoch=%d < local epoch=%d", m.Epoch, n.Epoch)
			return
		}
		if m.Epoch > n.Epoch {
			n.Epoch = m.Epoch
			n.votedFor = -1
		}
		if n.Role != Follower {
			s.log(n, m.From, "follower",
				"steps down, primary of epoch %d is node %d", m.Epoch, m.From)
		}
		n.Role = Follower
		n.leader = m.From
		n.lastLeaderContact = lt
		s.send(n.ID, m.From, MsgHeartbeatAck, n.Epoch, s.tick+s.cfg.ProcessingDelay)
	case MsgHeartbeatAck:
		if n.Role == Primary && m.Epoch == n.Epoch {
			n.ackTick[m.From] = lt
			count := 1 // 自己算一票
			for _, t := range n.ackTick {
				if t == lt {
					count++
				}
			}
			if count >= s.cfg.Majority() && lt > n.lastRenewal {
				n.lastRenewal = lt
				s.log(n, m.From, "renewed",
					"lease renewed by majority ack, serves until tick %d",
					lt+s.cfg.LeaseDuration-s.cfg.DriftBound)
			}
		}
	}
}

// onLeaseRequest 处理投票/租约请求。安全规则按顺序检查：
// 过期纪元 -> 他人租约仍有效（+ε 等待）-> 本纪元已投票。
func (s *Sim) onLeaseRequest(n *Node, m Message, lt int) {
	if m.Epoch < n.Epoch {
		s.log(n, m.From, "vote_rejected",
			"stale epoch=%d < local epoch=%d", m.Epoch, n.Epoch)
		return
	}
	if n.leaseValid(lt, s.cfg) && n.leader != m.From {
		s.log(n, m.From, "vote_rejected",
			"lease of node %d valid until tick %d",
			n.leader, n.lastLeaderContact+s.cfg.LeaseDuration+s.cfg.DriftBound)
		return
	}
	if m.Epoch > n.Epoch {
		n.Epoch = m.Epoch
		n.votedFor = -1
		if n.Role != Follower {
			n.Role = Follower
			s.log(n, m.From, "follower", "steps down for higher epoch %d", m.Epoch)
		}
	}
	if n.votedFor == m.From {
		// 重复请求：幂等重发授权，但不重复记日志。
		s.send(n.ID, m.From, MsgLeaseGrant, n.Epoch, s.tick+s.cfg.ProcessingDelay)
		return
	}
	if n.votedFor != -1 && n.votedFor != m.From {
		s.log(n, m.From, "vote_rejected",
			"already voted for node %d in epoch %d", n.votedFor, n.Epoch)
		return
	}
	n.votedFor = m.From
	n.leader = m.From
	n.lastLeaderContact = lt
	s.log(n, m.From, "vote_granted", "epoch=%d", m.Epoch)
	s.send(n.ID, m.From, MsgLeaseGrant, n.Epoch, s.tick+s.cfg.ProcessingDelay)
}

// onTimer 每 tick 的状态机定时逻辑：降级、心跳、重试、选举超时。
func (s *Sim) onTimer(n *Node) {
	lt := s.local(n)
	cfg := s.cfg
	switch n.Role {
	case Primary:
		if lt > n.lastRenewal+cfg.LeaseDuration-cfg.DriftBound {
			s.log(n, -1, "demoted",
				"lease expired: no majority ack since tick %d, stopped serving at tick %d",
				n.lastRenewal, n.lastRenewal+cfg.LeaseDuration-cfg.DriftBound)
			s.becomeCandidate(n, lt)
		} else if (lt-n.leaseStart)%cfg.HeartbeatInterval == 1 {
			s.broadcast(n, MsgHeartbeat, s.tick)
		}
	case Candidate:
		if lt >= n.lastRetry+cfg.RetryInterval {
			n.lastRetry = lt
			s.broadcast(n, MsgLeaseRequest, s.tick)
		}
	case Follower:
		if n.lastLeaderContact >= 0 &&
			lt >= n.lastLeaderContact+cfg.LeaseDuration+cfg.DriftBound+cfg.electionOffset(n.ID) {
			s.log(n, -1, "candidate",
				"election timeout: lease of node %d expired at tick %d",
				n.leader, n.lastLeaderContact+cfg.LeaseDuration+cfg.DriftBound)
			s.becomeCandidate(n, lt)
		}
	}
}

// becomeCandidate 转入候选者：纪元递增、自投一票并广播 LeaseRequest。
func (s *Sim) becomeCandidate(n *Node, lt int) {
	n.Role = Candidate
	n.Epoch++
	n.votedFor = n.ID
	n.votes = map[int]bool{n.ID: true}
	n.lastRetry = lt
	s.broadcast(n, MsgLeaseRequest, s.tick)
}

// StartElection 是外部触发（如人工指定初始主节点）。
func (s *Sim) StartElection(id int) {
	n := s.nodes[id]
	s.log(n, -1, "lease_request", "initiates lease negotiation for epoch %d", n.Epoch+1)
	s.becomeCandidate(n, s.local(n))
}

// Write 模拟客户端写入：只有持有有效租约的主节点才接受。
func (s *Sim) Write(id int) bool {
	n := s.nodes[id]
	lt := s.local(n)
	deadline := n.lastRenewal + s.cfg.LeaseDuration - s.cfg.DriftBound
	if n.Role == Primary && lt <= deadline {
		s.log(n, -1, "write_ok", "write accepted within lease (valid until tick %d)", deadline)
		return true
	}
	s.log(n, -1, "write_rejected", "node is %s, not a primary with valid lease", n.Role)
	return false
}
