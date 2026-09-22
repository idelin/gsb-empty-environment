// Package bank 在 sim/store 之上实现“转账命令 + 事务性 outbox +
// 至少一次事件投递 + 下游幂等消费”的完整例子。
package bank

import "encoding/json"

const (
	NodeSvc = "svc" // 上游服务（同时运行 relay）
	NodeDS  = "ds"  // 下游服务
)

// Command 是客户端发给上游的命令：给账户 amount 增加余额。
type Command struct {
	CmdID  string `json:"cmd_id"`
	Acct   string `json:"acct"`
	Amount int64  `json:"amount"`
}

// Receipt 是上游对命令的应答。
type Receipt struct {
	CmdID   string `json:"cmd_id"`
	Balance int64  `json:"balance"`
}

// Event 是 outbox 发布给下游的业务事件。EventID 即去重键。
type Event struct {
	EventID string `json:"event_id"`
	CmdID   string `json:"cmd_id"`
	Acct    string `json:"acct"`
	Amount  int64  `json:"amount"`
}

// Ack 是下游处理成功后发给上游 relay 的确认。
type Ack struct {
	EventID string `json:"event_id"`
}

// 存储键布局（上游）：
//
//	inbox:<cmdID>    -> json Receipt   已处理命令（去重 + 重放响应）
//	acct:<acct>      -> int64 字符串    账户余额
//	outbox:<eventID> -> json OutboxRow  outbox 记录
//
// 存储键布局（下游，独立 DB）：
//
//	dsinbox:<eventID> -> json Event    已处理事件（幂等去重）
//	effect:<eventID>  -> json Event    恰好一次的业务效果
const (
	keyInbox  = "inbox:"
	keyAcct   = "acct:"
	keyOutbox = "outbox:"

	keyDSInbox = "dsinbox:"
	keyEffect  = "effect:"
)

// OutboxRow 是一条 outbox 记录。
type OutboxRow struct {
	Event  Event  `json:"event"`
	Status string `json:"status"` // "pending" | "sent"
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func parseJSON[T any](s string) T {
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		panic(err)
	}
	return v
}
