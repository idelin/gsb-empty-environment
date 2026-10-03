package paxos

// PrepareRequest 是 Phase 1a：领导者请求多数派对某个提案号作出承诺。
// FromSlot 表示领导者已提交的前沿，响应方只需返回 >= FromSlot 的条目。
type PrepareRequest struct {
	Ballot   Ballot
	FromSlot uint64
}

// SlotEntry 描述一个已接受/已提交的日志条目，用于 Phase 1b 的响应与状态同步。
type SlotEntry struct {
	Slot      uint64
	Ballot    Ballot
	Value     []byte
	Committed bool
}

// PrepareResponse 是 Phase 1b：承诺不再接受更小提案号，并汇报本地日志。
type PrepareResponse struct {
	OK       bool
	Promised Ballot
	Entries  []SlotEntry
}

// AcceptRequest 是 Phase 2a：请求多数派在给定槽位接受一个值。
type AcceptRequest struct {
	Ballot Ballot
	Slot   uint64
	Value  []byte
}

// AcceptResponse 是 Phase 2b。
type AcceptResponse struct {
	OK       bool
	Promised Ballot
}

// CommitMessage 通知跟随者某槽位的值已被多数派提交。
type CommitMessage struct {
	Ballot Ballot
	Slot   uint64
	Value  []byte
}
