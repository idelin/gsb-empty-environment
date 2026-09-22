package bank

import (
	"fmt"
	"strconv"

	"outboxsim/sim"
	"outboxsim/store"
)

// Service 是上游服务；同一个节点上还周期运行 relay 轮询。
type Service struct {
	DB  *store.DB
	Bus *sim.Bus
}

// NewService 创建上游服务。commitHook 在每次业务事务提交后调用，
// 便于外部审计“提交时点”（用于证明未提交不外发）。
func NewService(db *store.DB, bus *sim.Bus, commitHook func(eventIDs []string)) *Service {
	db.OnCommit = func(changed []string) {
		var ids []string
		for _, k := range changed {
			if n, ok := stripPrefix(k, keyOutbox); ok {
				ids = append(ids, n)
			}
		}
		if len(ids) > 0 && commitHook != nil {
			commitHook(ids)
		}
	}
	return &Service{DB: db, Bus: bus}
}

// Start 在每次（重）启动时注册消息处理并调度 relay 轮询。
func (s *Service) Start(n *sim.Node) {
	s.Bus.Handle(NodeSvc, s.handle)
	n.After(pollInterval, func() { s.relay(n) })
}

const pollInterval int64 = 10

func (s *Service) handle(n *sim.Node, m *sim.Msg) {
	switch p := m.Payload.(type) {
	case Command:
		s.onCommand(n, m, p)
	case Ack:
		s.onAck(n, p)
	default:
		panic("svc: unexpected message")
	}
}

// onCommand 是核心：接收记录（inbox）、本地业务修改（余额）、
// outbox 记录在同一个事务中原子提交。
func (s *Service) onCommand(n *sim.Node, m *sim.Msg, c Command) {
	// 窗口 1（重投）：命令已处理过。不再修改任何业务状态，只重放响应。
	if raw, ok := readInbox(s.DB, c.CmdID); ok {
		n.Fault("after_commit:" + c.CmdID) // 与首次处理的同位置窗口，便于注入响应丢失
		s.reply(m.From, raw)
		return
	}

	tx := s.DB.Begin()
	defer tx.Rollback()
	var balance int64
	if cur, ok := tx.Get(keyAcct + c.Acct); ok {
		balance = atoi(cur)
	}
	balance += c.Amount

	ev := Event{EventID: "ev-" + c.CmdID, CmdID: c.CmdID, Acct: c.Acct, Amount: c.Amount}
	row := OutboxRow{Event: ev, Status: "pending"}
	receipt := Receipt{CmdID: c.CmdID, Balance: balance}

	tx.Put(keyAcct+c.Acct, strconv.FormatInt(balance, 10))
	tx.Put(keyInbox+c.CmdID, mustJSON(receipt))
	tx.Put(keyOutbox+ev.EventID, mustJSON(row))

	// 窗口 2（未提交不外发）：提交前崩溃 -> 上面三行全部不生效。
	n.Fault("before_commit:" + c.CmdID)
	tx.Commit()

	// 窗口 3（提交后响应丢失）：响应可能丢失；客户端会重投，
	// 重投命中 inbox，业务不再变化。
	n.Fault("after_commit:" + c.CmdID)
	s.reply(m.From, receipt)
}

func (s *Service) reply(to string, r Receipt) {
	s.Bus.Send(&sim.Msg{
		From: NodeSvc, To: to, Kind: "receipt", ID: r.CmdID, Payload: r,
	})
}

// relay 周期扫描 pending outbox 并发布；收到下游 ack 才标记 sent。
// 未收到 ack 的记录会被下一轮重新发布 -> 下游用 inbox 去重。
func (s *Service) relay(n *sim.Node) {
	tx := s.DB.Begin()
	pending := tx.ScanPrefix(keyOutbox)
	tx.Commit()

	for _, kv := range pending {
		row := parseJSON[OutboxRow](kv.Value)
		if row.Status != "pending" {
			continue
		}
		s.Bus.Send(&sim.Msg{
			From: NodeSvc, To: NodeDS, Kind: "event",
			ID: row.Event.EventID, Payload: row.Event,
		})
		// 窗口 4（已发布未标记）：此处崩溃或 ack 丢失，下一轮重发。
		n.Fault("after_publish:" + row.Event.EventID)
	}
	n.After(pollInterval, func() { s.relay(n) })
}

func (s *Service) onAck(n *sim.Node, a Ack) {
	tx := s.DB.Begin()
	defer tx.Rollback()
	raw, ok := tx.Get(keyOutbox + a.EventID)
	if !ok {
		tx.Rollback()
		return
	}
	row := parseJSON[OutboxRow](raw)
	row.Status = "sent"
	tx.Put(keyOutbox+a.EventID, mustJSON(row))
	// 窗口 5（标记前崩溃）：ack 丢失等效，下轮重发，下游去重。
	n.Fault("before_mark:" + a.EventID)
	tx.Commit()
}

func readInbox(db *store.DB, cmdID string) (Receipt, bool) {
	var r Receipt
	var ok bool
	db.Read(func(tx *store.Tx) {
		var v string
		v, ok = tx.Get(keyInbox + cmdID)
		if ok {
			r = parseJSON[Receipt](v)
		}
	})
	return r, ok
}

func stripPrefix(k, prefix string) (string, bool) {
	if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
		return k[len(prefix):], true
	}
	return "", false
}

func atoi(s string) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		panic(fmt.Sprintf("bad balance %q", s))
	}
	return v
}
