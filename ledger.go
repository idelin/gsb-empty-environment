package quota

import "time"

// EventType 账本事件类型。
type EventType int

const (
	EventReserve        EventType = iota // 预留占用增加
	EventSettle                          // 结算：预留释放，实际计费入账
	EventSettleRejected                  // 结算超额被拒：预留释放，不计费
	EventCancel                          // 取消：预留释放
	EventFail                            // 压缩失败：预留释放
	EventTransfer                        // 块归属迁移：计费在租户间转移
	EventLimitChange                     // 限额调整（不影响占用，仅记录）
)

func (t EventType) String() string {
	switch t {
	case EventReserve:
		return "reserve"
	case EventSettle:
		return "settle"
	case EventSettleRejected:
		return "settle_rejected"
	case EventCancel:
		return "cancel"
	case EventFail:
		return "fail"
	case EventTransfer:
		return "transfer"
	case EventLimitChange:
		return "limit_change"
	}
	return "unknown"
}

// Event 一条账本记录。只追加、不修改。
type Event struct {
	Seq    int64
	Time   time.Time
	Type   EventType
	Tenant string // 占用所属租户（迁移时为源租户）
	ResID  string
	Bytes  int64  // 预留/释放的字节数
	Billed int64  // 结算实际计费字节数
	Limit  int64  // 限额调整后的值
	To     string // 迁移目标租户
	Reason string
}

func (s *Store) appendLocked(e Event) {
	s.seq++
	e.Seq = s.seq
	e.Time = s.now()
	s.ledger = append(s.ledger, e)
}

// Ledger 返回账本快照（副本）。
func (s *Store) Ledger() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.ledger))
	copy(out, s.ledger)
	return out
}

// Peak 峰值占用：Reserved+Used 的历史最大值。
type Peak struct {
	Tenant map[string]int64 // 租户级峰值（预留+已用）
	Global int64            // 全局峰值
}

// ReplayPeak 重放事件账本，复算任一时刻占用（预留+已用）的峰值。
// 账本是唯一事实来源：即使内存状态损坏，也可据此重建计费视图。
func (s *Store) ReplayPeak() Peak {
	s.mu.Lock()
	defer s.mu.Unlock()
	return replayPeak(s.ledger)
}

func replayPeak(ledger []Event) Peak {
	reserved := make(map[string]int64)
	used := make(map[string]int64)
	peak := Peak{Tenant: make(map[string]int64)}

	var global int64
	mark := func(tenant string) {
		total := reserved[tenant] + used[tenant]
		if total > peak.Tenant[tenant] {
			peak.Tenant[tenant] = total
		}
		if global > peak.Global {
			peak.Global = global
		}
	}

	for _, e := range ledger {
		switch e.Type {
		case EventReserve:
			reserved[e.Tenant] += e.Bytes
			global += e.Bytes
		case EventSettle:
			reserved[e.Tenant] -= e.Bytes
			used[e.Tenant] += e.Billed
			global += e.Billed - e.Bytes
		case EventSettleRejected, EventCancel, EventFail:
			reserved[e.Tenant] -= e.Bytes
			global -= e.Bytes
		case EventTransfer:
			used[e.Tenant] -= e.Bytes
			used[e.To] += e.Bytes // 全局总量不变
		case EventLimitChange:
			// 不影响占用
		}
		mark(e.Tenant)
		if e.Type == EventTransfer {
			mark(e.To)
		}
	}
	return peak
}
