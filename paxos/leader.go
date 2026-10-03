package paxos

import "time"

// Propose 提议一个值并返回其日志槽位。
// 租约有效时直接执行 Phase 2（绕过 Phase 1）；否则先竞选并完成空洞修补。
func (n *Node) Propose(value []byte) (uint64, error) {
	if !n.hasLease() {
		if err := n.prepare(); err != nil {
			return 0, err
		}
	}
	return n.phase2(value)
}

// hasLease 报告本节点是否持有有效的领导租约：
// 自己仍是当前任期的承诺持有者，且租约未过期。
func (n *Node) hasLease() bool {
	b := n.ballot.Load()
	return b != 0 &&
		n.promised.Load() == b &&
		time.Now().UnixNano() < n.leaseExp.Load()
}

// HasLease 导出 hasLease 供测试与演示使用。
func (n *Node) HasLease() bool { return n.hasLease() }

// prepare 执行 Phase 1：向多数派发送 Prepare，
// 随后对承诺响应中暴露的所有未提交槽位做修补（选值 + noop 填洞 + 提交）。
func (n *Node) prepare() error {
	my := NewBallot(Ballot(n.promised.Load()).Num()+1, uint64(n.id))
	from := n.commitFrontier.Load()

	// 先对自己承诺
	self := n.HandlePrepare(PrepareRequest{Ballot: my, FromSlot: from})
	if !self.OK {
		return ErrNoQuorum
	}
	responses := []PrepareResponse{self}
	for _, p := range n.peers {
		if p == n.id {
			continue
		}
		if resp, ok := n.nw.Prepare(n.id, p, PrepareRequest{Ballot: my, FromSlot: from}); ok && resp.OK {
			responses = append(responses, resp)
		}
	}
	if len(responses) < n.majority {
		return ErrNoQuorum
	}
	n.ballot.Store(uint64(my))

	// 汇总多数派汇报的条目：已提交值优先，否则取最高提案号的值。
	chosen := make(map[uint64]SlotEntry)
	maxSlot := int64(-1)
	for _, r := range responses {
		for _, e := range r.Entries {
			if e.Slot < from {
				continue
			}
			cur, ok := chosen[e.Slot]
			switch {
			case !ok:
				chosen[e.Slot] = e
			case cur.Committed:
				// 已提交值不可替换
			case e.Committed || e.Ballot > cur.Ballot:
				chosen[e.Slot] = e
			}
			if int64(e.Slot) > maxSlot {
				maxSlot = int64(e.Slot)
			}
		}
	}

	// 修补 [from, maxSlot] 区间：无汇报的槽位是空洞，用 noop（nil）填充。
	if maxSlot >= 0 {
		for slot := from; slot <= uint64(maxSlot); slot++ {
			var value []byte
			if e, ok := chosen[slot]; ok {
				value = e.Value
			}
			if err := n.acceptToMajority(slot, value, my); err != nil {
				return err
			}
			n.commit(slot, value, my)
			n.broadcastCommit(slot, value, my)
		}
	}

	next := from
	if maxSlot >= 0 && uint64(maxSlot)+1 > next {
		next = uint64(maxSlot) + 1
	}
	n.nextSlot.Store(next)
	n.extendLease()
	return nil
}

// phase2 在租约内直接分配槽位并执行 Accept/Accepted，多数派接受后立即提交。
func (n *Node) phase2(value []byte) (uint64, error) {
	b := Ballot(n.ballot.Load())
	slot := n.nextSlot.Add(1) - 1
	if err := n.acceptToMajority(slot, value, b); err != nil {
		n.leaseExp.Store(0) // 租约失效，下次重新竞选
		return 0, err
	}
	n.commit(slot, value, b)
	n.broadcastCommit(slot, value, b)
	n.extendLease()
	return slot, nil
}

// acceptToMajority 向自己及所有可达同伴发送 Accept，多数派成功则返回 nil。
func (n *Node) acceptToMajority(slot uint64, value []byte, b Ballot) error {
	req := AcceptRequest{Ballot: b, Slot: slot, Value: value}
	count := 0
	if n.HandleAccept(req).OK {
		count++
	}
	for _, p := range n.peers {
		if p == n.id {
			continue
		}
		if resp, ok := n.nw.Accept(n.id, p, req); ok && resp.OK {
			count++
		}
	}
	if count < n.majority {
		return ErrNoQuorum
	}
	return nil
}

func (n *Node) commit(slot uint64, value []byte, b Ballot) {
	n.HandleCommit(CommitMessage{Ballot: b, Slot: slot, Value: value})
}

func (n *Node) broadcastCommit(slot uint64, value []byte, b Ballot) {
	msg := CommitMessage{Ballot: b, Slot: slot, Value: value}
	for _, p := range n.peers {
		if p != n.id {
			n.nw.Commit(n.id, p, msg)
		}
	}
}

func (n *Node) extendLease() {
	n.leaseExp.Store(time.Now().Add(n.leaseDur).UnixNano())
}

// SyncTo 把本节点所有已提交条目重发给指定同伴，
// 用于分区愈合后的追赶（catch-up），覆写少数派上的脏日志。
func (n *Node) SyncTo(peer int) {
	max := n.log.maxIndex()
	for i := uint64(0); i <= uint64(max) && max >= 0; i++ {
		if s := n.log.load(i); s != nil && s.committed {
			n.nw.Commit(n.id, peer, CommitMessage{
				Ballot: s.acceptedBallot,
				Slot:   i,
				Value:  s.value,
			})
		}
	}
}
