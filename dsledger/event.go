package dsledger

// walEventType 枚举按成员 WAL 中记录的事件类型。
type walEventType string

const (
	// evAccepted：份额通过全部规则并进入已签索引。
	evAccepted walEventType = "accepted"
	// evRoundAdvanced：同槽位同区块出现更高轮次，更新最大轮次（份额本体不覆盖）。
	evRoundAdvanced walEventType = "round_advanced"
	// evStaged：份额因前置缺失进入暂存。
	evStaged walEventType = "staged"
	// evUnstaged：份额离开暂存（被发布、被同槽位新高轮次替换或被清理）。
	evUnstaged walEventType = "unstaged"
	// evPurged：冻结成员的暂存被显式清空。
	evPurged walEventType = "purged"
	// evFrozen：成员被冻结，附最小双签证据。
	evFrozen walEventType = "frozen"
	// evUnfrozen：冻结成员在暂存矛盾清理并归档证据后被解冻。
	evUnfrozen walEventType = "unfrozen"
)

// walEvent 是 WAL 中的单条记录。指针字段按事件类型取用。
type walEvent struct {
	Type     walEventType `json:"type"`
	Share    *Share       `json:"share,omitempty"`
	MaxRound uint64       `json:"max_round,omitempty"`
	Evidence *Evidence    `json:"evidence,omitempty"`
}
