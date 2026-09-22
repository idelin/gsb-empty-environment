package bank

import (
	"fmt"
	"strconv"

	"outboxsim/sim"
	"outboxsim/store"
)

// AssertSafety 是每事件之后运行的安全不变量集合。任何一条被违反都会
// panic 并使测试失败。
//
//	S1 原子性：inbox 记录、outbox 记录、余额变化要么都在要么都不在，
//	   且数值一致（接收记录与业务修改同事务）。
//	S2 未提交不外发：每个被投递的 event，其 outbox 必须在投递时点之前
//	   就已提交（轨迹顺序保证）。
//	S3 下游幂等：下游每个 eventID 至多一条效果，且有 inbox 记录；
//	   ack/效果只可能来自上游已发布的 outbox。
//	S4 outbox 状态合法：不存在 sent 却没有效果（效果缺失只能是 pending）。
func AssertSafety(svcDB, dsDB *store.DB, s *sim.Sim) {
	// S1
	type acctState struct {
		balance int64
		seen    bool
	}
	expected := map[string]acctState{}
	svcDB.Read(func(tx *store.Tx) {
		for _, kv := range tx.ScanPrefix(keyInbox) {
			r := parseJSON[Receipt](kv.Value)
			evKey := keyOutbox + "ev-" + r.CmdID
			raw, ok := tx.Get(evKey)
			if !ok {
				panic(fmt.Sprintf("S1: inbox %s without outbox", r.CmdID))
			}
			row := parseJSON[OutboxRow](raw)
			if row.Event.CmdID != r.CmdID {
				panic(fmt.Sprintf("S1: outbox mismatch for %s", r.CmdID))
			}
			st := expected[row.Event.Acct]
			st.balance += row.Event.Amount
			st.seen = true
			expected[row.Event.Acct] = st
		}
		for acct, want := range expected {
			cur, ok := tx.Get(keyAcct + acct)
			if !ok || !want.seen {
				panic(fmt.Sprintf("S1: acct %s missing balance", acct))
			}
			if atoi(cur) != want.balance {
				panic(fmt.Sprintf("S1: acct %s balance %d != commands sum %d",
					acct, atoi(cur), want.balance))
			}
		}
	})

	// S3 + S4
	svcDB.Read(func(tx *store.Tx) {
		dsDB.Read(func(dtx *store.Tx) {
			effects := map[string]bool{}
			for _, kv := range dtx.ScanPrefix(keyEffect) {
				ev := parseJSON[Event](kv.Value)
				if effects[ev.EventID] {
					panic("S3: duplicate effect " + ev.EventID)
				}
				effects[ev.EventID] = true
				if _, ok := dtx.Get(keyDSInbox + ev.EventID); !ok {
					panic("S3: effect without inbox " + ev.EventID)
				}
			}
			for _, kv := range tx.ScanPrefix(keyOutbox) {
				row := parseJSON[OutboxRow](kv.Value)
				if row.Status == "sent" && !effects[row.Event.EventID] {
					panic("S4: outbox marked sent without effect " + row.Event.EventID)
				}
			}
		})
	})

	// S2：重放轨迹，event 投递必须晚于对应 outbox 的提交。
	committed := map[string]bool{}
	for _, t := range s.Traces() {
		if t.Kind == "commit" {
			for _, id := range commitEventIDs(t) {
				committed[id] = true
			}
			continue
		}
		if t.Kind == "deliver" && t.Msg != nil && t.Msg.Kind == "event" {
			if !committed[t.Msg.ID] {
				panic("S2: event " + t.Msg.ID + " delivered before commit")
			}
		}
	}
}

// commitEventIDs 从提交轨迹标签中取出 outbox eventID 列表（轨迹由 hook 记录）。
func commitEventIDs(t sim.Trace) []string {
	if t.Kind != "commit" {
		return nil
	}
	return parseCommitIDs(t.Label)
}

// parseCommitIDs 解析 hook 写入的空格分隔 eventID 列表。
func parseCommitIDs(label string) []string {
	if label == "" {
		return nil
	}
	var ids []string
	start := 0
	for i := 0; i <= len(label); i++ {
		if i == len(label) || label[i] == ' ' {
			ids = append(ids, label[start:i])
			start = i + 1
		}
	}
	return ids
}

// Balance 读出某账户余额（供断言），不存在返回 0。
func Balance(db *store.DB, acct string) int64 {
	var v int64
	db.Read(func(tx *store.Tx) {
		if s, ok := tx.Get(keyAcct + acct); ok {
			v, _ = strconv.ParseInt(s, 10, 64)
		}
	})
	return v
}

// CountPrefix 统计数据库中以 prefix 开头的键数。
func CountPrefix(db *store.DB, prefix string) int {
	return len(db.ViewKeys(prefix))
}

// OutboxStatus 读出某条 outbox 状态。
func OutboxStatus(db *store.DB, eventID string) (string, bool) {
	var st string
	var ok bool
	db.Read(func(tx *store.Tx) {
		if raw, exists := tx.Get(keyOutbox + eventID); exists {
			ok = true
			st = parseJSON[OutboxRow](raw).Status
		}
	})
	return st, ok
}
