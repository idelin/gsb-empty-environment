package main

import "fmt"

// Entry 是一条日志条目，由任期和索引共同标记（索引 = 在日志中的位置，从 1 开始）。
type Entry struct {
	Term    int
	Command string
}

func (e Entry) String() string {
	return fmt.Sprintf("(T%d %s)", e.Term, e.Command)
}

// AppendRequest 对应 Raft 的 AppendEntries RPC。
type AppendRequest struct {
	Term         int     // 领导者任期
	PrevLogIndex int     // 紧接新条目之前的索引
	PrevLogTerm  int     // PrevLogIndex 处条目的任期
	Entries      []Entry // 待追加的条目（心跳时为空）
	LeaderCommit int     // 领导者的提交索引
}

// AppendReply 携带冲突优化信息，供领导者快速回退 nextIndex。
type AppendReply struct {
	Success       bool
	ConflictTerm  int // 冲突条目的任期；-1 表示跟随者日志太短
	ConflictIndex int // ConflictTerm 任期的首个索引；日志太短时 = len(log)+1
	MatchIndex    int // 成功时已匹配的最大索引
}

// Follower 维护日志副本与提交索引。
type Follower struct {
	name        string
	log         []Entry
	commitIndex int
	currentTerm int
}

func NewFollower(name string, log []Entry, commitIndex int) *Follower {
	cp := append([]Entry(nil), log...)
	return &Follower{name: name, log: cp, commitIndex: commitIndex}
}

func (f *Follower) lastIndex() int { return len(f.log) }

func (f *Follower) termAt(index int) int {
	if index <= 0 || index > len(f.log) {
		return 0
	}
	return f.log[index-1].Term
}

// handleAppend 处理一次 AppendEntries，返回回复与本次产生的事件
// （每次截断与追加的范围都会作为事件输出）。
func (f *Follower) handleAppend(req AppendRequest) (AppendReply, []string) {
	var events []string

	// 迟到的旧任期请求直接拒绝。
	if req.Term < f.currentTerm {
		return AppendReply{Success: false, ConflictTerm: -1, ConflictIndex: f.lastIndex() + 1}, events
	}
	f.currentTerm = req.Term

	// 一致性检查：PrevLogIndex/PrevLogTerm 必须匹配。
	if req.PrevLogIndex > f.lastIndex() {
		// 日志太短：告知领导者从末尾之后开始探测。
		return AppendReply{Success: false, ConflictTerm: -1, ConflictIndex: f.lastIndex() + 1}, events
	}
	if req.PrevLogIndex > 0 {
		term := f.termAt(req.PrevLogIndex)
		if term != req.PrevLogTerm {
			// 冲突优化：回退到冲突任期在该日志中的首个索引。
			first := req.PrevLogIndex
			for first > 1 && f.termAt(first-1) == term {
				first--
			}
			return AppendReply{Success: false, ConflictTerm: term, ConflictIndex: first}, events
		}
	}

	// 合并条目：逐条比对，已存在的相同条目直接跳过（重试/重复幂等）。
	match := req.PrevLogIndex
	tail := 0
merge:
	for tail = 0; tail < len(req.Entries); tail++ {
		pos := req.PrevLogIndex + 1 + tail
		switch {
		case pos > f.lastIndex():
			break merge // 之后的都是新条目
		case f.log[pos-1].Term == req.Entries[tail].Term:
			match = pos // 条目一致，保留
		case pos <= f.commitIndex:
			// 已提交条目禁止截断：拒绝覆盖，保留已提交前缀。
			events = append(events, fmt.Sprintf("%s: reject truncate [%d..%d] (committed prefix is immutable)",
				f.name, pos, f.lastIndex()))
			tail = len(req.Entries)
			break merge
		default:
			events = append(events, fmt.Sprintf("%s: truncate [%d..%d]", f.name, pos, f.lastIndex()))
			f.log = f.log[:pos-1]
			break merge
		}
	}
	if tail < len(req.Entries) {
		from := req.PrevLogIndex + 1 + tail
		f.log = append(f.log, req.Entries[tail:]...)
		events = append(events, fmt.Sprintf("%s: append [%d..%d]", f.name, from, f.lastIndex()))
		match = f.lastIndex()
	}

	// 推进提交索引。
	if req.LeaderCommit > f.commitIndex {
		newCommit := req.LeaderCommit
		if newCommit > f.lastIndex() {
			newCommit = f.lastIndex()
		}
		if newCommit > f.commitIndex {
			f.commitIndex = newCommit
			events = append(events, fmt.Sprintf("%s: commit ..%d", f.name, newCommit))
		}
	}
	return AppendReply{Success: true, MatchIndex: match}, events
}

// Leader 持有权威日志，按 nextIndex 向跟随者复制。
type Leader struct {
	log         []Entry
	term        int
	commitIndex int
}

func NewLeader(term int, log []Entry) *Leader {
	return &Leader{log: append([]Entry(nil), log...), term: term}
}

// requestFor 构造从 nextIndex 开始的 AppendEntries 请求。
func (l *Leader) requestFor(nextIndex int) AppendRequest {
	prev := nextIndex - 1
	return AppendRequest{
		Term:         l.term,
		PrevLogIndex: prev,
		PrevLogTerm:  l.termAt(prev),
		Entries:      append([]Entry(nil), l.log[prev:]...),
		LeaderCommit: l.commitIndex,
	}
}

func (l *Leader) termAt(index int) int {
	if index <= 0 || index > len(l.log) {
		return 0
	}
	return l.log[index-1].Term
}

// backup 依据冲突回复计算新的 nextIndex（冲突任期快速回退）。
func (l *Leader) backup(rep AppendReply) int {
	if rep.ConflictTerm == -1 {
		return rep.ConflictIndex
	}
	last := 0
	for i, e := range l.log {
		if e.Term == rep.ConflictTerm {
			last = i + 1
		}
	}
	if last > 0 {
		// 领导者持有该任期的条目：跳到该任期最后一条之后。
		return last + 1
	}
	// 领导者没有该任期：跟随者整段都要被覆盖，跳到冲突段起点。
	return rep.ConflictIndex
}
