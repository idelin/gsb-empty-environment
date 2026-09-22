package readindexsim

func (s *Sim) startElection(id int) {
	n := s.nodes[id]
	n.term++
	n.role = Candidate
	n.leaderID = -1
	n.votedFor = id
	n.votes = map[int]bool{id: true}
	n.reads = map[int]*ReadState{}

	s.logf(id, "start election term %d", n.term)
	for peer := range s.nodes {
		if peer == id {
			continue
		}
		s.send(Message{
			From:         id,
			To:           peer,
			Type:         RequestVote,
			Term:         n.term,
			PrevLogIndex: lastLogIndex(n),
			PrevLogTerm:  lastLogTerm(n),
		})
	}
	s.scheduleElection(id, int64(n.electionTimeout))
}

func (s *Sim) becomeLeader(id int, term int) {
	n := s.nodes[id]
	n.role = Leader
	n.term = term
	n.leaderID = id
	n.votes = map[int]bool{}
	n.reads = map[int]*ReadState{}
	n.electionGen++
	s.leaderTerm[id] = term

	last := lastLogIndex(n)
	n.nextIndex = map[int]int{}
	n.matchIndex = map[int]int{}
	for peer := range s.nodes {
		if peer != id {
			n.nextIndex[peer] = last + 1
			n.matchIndex[peer] = 0
		}
	}

	n.log = append(n.log, Entry{
		Index: last + 1,
		Term:  term,
		Op:    Op{Kind: "noop"},
	})
	n.matchIndex[id] = last + 1
	n.nextIndex[id] = last + 2

	s.logf(id, "became leader term %d; appends noop at %d", term, last+1)
	s.broadcastAppend(id, 0)
	s.scheduleHeartbeat(id)
}

func (s *Sim) becomeFollower(id, term int) {
	n := s.nodes[id]
	n.role = Follower
	n.term = term
	n.leaderID = -1
	n.votedFor = -1
	n.votes = map[int]bool{}
	n.reads = map[int]*ReadState{}
	n.nextIndex = map[int]int{}
	n.matchIndex = map[int]int{}
}

func (s *Sim) handleMessage(msg Message) {
	if !s.connected[msg.From] || !s.connected[msg.To] {
		return
	}
	n := s.nodes[msg.To]

	if msg.Term > n.term {
		if msg.Type == AppendEntries || msg.Type == VoteResult || msg.Type == AppendResult {
			s.becomeFollower(msg.To, msg.Term)
			n = s.nodes[msg.To]
		}
	}

	switch msg.Type {
	case RequestVote:
		s.handleRequestVote(msg, n)
	case VoteResult:
		s.handleVoteResult(msg, n)
	case AppendEntries:
		s.handleAppendEntries(msg, n)
	case AppendResult:
		s.handleAppendResult(msg, n)
	}
}

func (s *Sim) handleRequestVote(msg Message, n *Node) {
	granted := false
	if msg.Term < n.term {
		granted = false
	} else if msg.Term > n.term {
		s.becomeFollower(n.id, msg.Term)
		n = s.nodes[msg.To]
	}

	if msg.Term == n.term {
		upToDate := msg.PrevLogTerm > lastLogTerm(n) ||
			(msg.PrevLogTerm == lastLogTerm(n) && msg.PrevLogIndex >= lastLogIndex(n))
		if (n.votedFor == -1 || n.votedFor == msg.From) && upToDate {
			granted = true
			n.votedFor = msg.From
			n.leaderID = -1
			s.resetElectionTimer(n.id)
		}
	}

	s.logf(n.id, "vote n%d=%v t%d", msg.From, granted, n.term)
	s.send(Message{
		From:    n.id,
		To:      msg.From,
		Type:    VoteResult,
		Term:    n.term,
		Granted: granted,
	})
}

func (s *Sim) handleVoteResult(msg Message, n *Node) {
	if n.role != Candidate || msg.Term != n.term {
		return
	}
	if msg.Granted {
		n.votes[msg.From] = true
		if ackQuorum(n.votes) {
			s.becomeLeader(n.id, n.term)
		}
	}
}

func (s *Sim) broadcastAppend(leader, readID int) {
	for peer := range s.nodes {
		if peer == leader {
			continue
		}
		s.sendAppend(leader, peer, readID)
	}
}

func (s *Sim) oldestReadID(leader int) int {
	oldest := 0
	for id := range s.nodes[leader].reads {
		if oldest == 0 || id < oldest {
			oldest = id
		}
	}
	return oldest
}

func (s *Sim) sendAppend(leader, peer, readID int) {
	l := s.nodes[leader]
	prev := l.nextIndex[peer] - 1
	entries := []Entry{}
	if next := l.nextIndex[peer]; next < len(l.log) {
		entries = append(entries, l.log[next:]...)
	}
	s.send(Message{
		From:         leader,
		To:           peer,
		Type:         AppendEntries,
		Term:         l.term,
		ReadID:       readID,
		PrevLogIndex: prev,
		PrevLogTerm:  entryAt(l, prev).Term,
		Entries:      entries,
		LeaderCommit: l.commitIndex,
	})
}

func (s *Sim) handleAppendEntries(msg Message, n *Node) {
	if msg.Term < n.term {
		s.send(Message{
			From:       n.id,
			To:         msg.From,
			Type:       AppendResult,
			Term:       n.term,
			Success:    false,
			MatchIndex: 0,
		})
		return
	}

	if msg.Term > n.term || n.role != Follower {
		s.becomeFollower(n.id, msg.Term)
		n = s.nodes[msg.To]
	}

	n.leaderID = msg.From
	s.resetElectionTimer(n.id)

	if msg.PrevLogIndex > lastLogIndex(n) || entryAt(n, msg.PrevLogIndex).Term != msg.PrevLogTerm {
		s.logf(n.id, "reject append prev=%d/%d local=%d", msg.PrevLogIndex, msg.PrevLogTerm, lastLogIndex(n))
		s.send(Message{
			From:       n.id,
			To:         msg.From,
			Type:       AppendResult,
			Term:       n.term,
			ReadID:     msg.ReadID,
			Success:    false,
			MatchIndex: 0,
		})
		return
	}

	for _, remote := range msg.Entries {
		if remote.Index > len(n.log) {
			s.send(Message{
				From:       n.id,
				To:         msg.From,
				Type:       AppendResult,
				Term:       n.term,
				ReadID:     msg.ReadID,
				Success:    false,
				MatchIndex: 0,
			})
			return
		}
		if remote.Index == len(n.log) {
			n.log = append(n.log, remote)
			continue
		}
		if remote.Index < len(n.log) {
			if n.log[remote.Index].Term != remote.Term {
				n.log = append(append([]Entry(nil), n.log[:remote.Index]...), remote)
			}
		}
	}

	lastGood := msg.PrevLogIndex
	if len(msg.Entries) > 0 {
		lastGood = msg.Entries[len(msg.Entries)-1].Index
	}

	if msg.LeaderCommit > n.commitIndex {
		if msg.LeaderCommit < lastGood {
			n.commitIndex = msg.LeaderCommit
		} else {
			n.commitIndex = lastGood
		}
		s.applyCommitted(n)
	}

	s.send(Message{
		From:       n.id,
		To:         msg.From,
		Type:       AppendResult,
		Term:       n.term,
		ReadID:     msg.ReadID,
		Success:    true,
		MatchIndex: lastGood,
	})
}

func (s *Sim) handleAppendResult(msg Message, l *Node) {
	if l.role != Leader {
		return
	}
	if msg.Term > l.term {
		s.becomeFollower(l.id, msg.Term)
		return
	}
	if msg.Term < l.term {
		return
	}

	if !msg.Success {
		if l.nextIndex[msg.From] > 1 {
			l.nextIndex[msg.From]--
		}
		s.sendAppend(l.id, msg.From, msg.ReadID)
		return
	}

	if msg.MatchIndex > l.matchIndex[msg.From] {
		l.matchIndex[msg.From] = msg.MatchIndex
	}
	if l.nextIndex[msg.From] < msg.MatchIndex+1 {
		l.nextIndex[msg.From] = msg.MatchIndex + 1
	}

	s.advanceCommit(l)
	s.applyCommitted(l)

	if read, ok := l.reads[msg.ReadID]; ok && read.term == l.term {
		read.acks[msg.From] = true
		s.maybeCompleteRead(l, read)
	}
}

func (s *Sim) advanceCommit(l *Node) {
	for index := l.commitIndex + 1; index < len(l.log); index++ {
		if l.log[index].Term != l.term {
			continue
		}
		count := 0
		for _, match := range l.matchIndex {
			if match >= index {
				count++
			}
		}
		if count >= 2 {
			l.commitIndex = index
		}
	}
}

func (s *Sim) applyCommitted(n *Node) {
	if n.applyPaused {
		return
	}
	for n.appliedIndex < n.commitIndex {
		n.appliedIndex++
		entry := n.log[n.appliedIndex]
		if entry.Op.Kind == "set" {
			n.state[entry.Op.Key] = entry.Op.Value
		}
	}
	if n.role == Leader {
		for _, read := range n.reads {
			s.maybeCompleteRead(n, read)
		}
	}
}

func (s *Sim) requestRead(leader int, req ReadRequest) {
	n := s.nodes[leader]
	if n.role != Leader {
		return
	}
	if _, exists := n.reads[req.ID]; exists {
		return
	}
	read := &ReadState{
		id:        req.ID,
		key:       req.Key,
		term:      n.term,
		readIndex: n.commitIndex,
		acks:      map[int]bool{n.id: true},
	}
	n.reads[req.ID] = read
	s.logf(n.id, "client read %d waits for quorum at readIndex=%d", req.ID, n.commitIndex)
	for peer := range s.nodes {
		if peer == n.id {
			continue
		}
		s.sendAppend(n.id, peer, req.ID)
	}
	s.maybeCompleteRead(n, read)
}

func (s *Sim) maybeCompleteRead(l *Node, read *ReadState) {
	if read.acks == nil || !ackQuorum(read.acks) || l.appliedIndex < read.readIndex {
		return
	}
	value := l.state[read.key]
	result := ReadResult{
		ReadID:   read.id,
		LeaderID: l.id,
		Key:      read.key,
		Value:    value,
		Evidence: ReadEvidence{
			LeaderID:     l.id,
			Term:         read.term,
			ReadIndex:    read.readIndex,
			Acks:         sortedAcks(read.acks),
			AppliedIndex: l.appliedIndex,
		},
	}
	delete(l.reads, read.id)
	s.results = append(s.results, result)
	s.logf(l.id, "read %d complete value=%q evidence term=%d index=%d acks=%v applied=%d",
		read.id, value, read.term, read.readIndex, read.acks, l.appliedIndex)
}
