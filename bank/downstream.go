package bank

import (
	"outboxsim/sim"
	"outboxsim/store"
)

// Downstream 是幂等的下游消费者：处理事件与记录 inbox 在同一事务，
// 因此重复事件的业务效果恰好一次；处理成功后回复 ack。
type Downstream struct {
	DB  *store.DB
	Bus *sim.Bus
}

func NewDownstream(db *store.DB, bus *sim.Bus) *Downstream {
	return &Downstream{DB: db, Bus: bus}
}

func (d *Downstream) Start(n *sim.Node) {
	d.Bus.Handle(NodeDS, d.handle)
}

func (d *Downstream) handle(n *sim.Node, m *sim.Msg) {
	ev, ok := m.Payload.(Event)
	if !ok {
		panic("ds: unexpected message")
	}

	// 重复事件直接确认，不再产生业务效果。
	var dup bool
	d.DB.Read(func(tx *store.Tx) {
		_, dup = tx.Get(keyDSInbox + ev.EventID)
	})
	if dup {
		d.ack(ev.EventID)
		return
	}

	tx := d.DB.Begin()
	defer tx.Rollback()
	tx.Put(keyDSInbox+ev.EventID, mustJSON(ev))
	tx.Put(keyEffect+ev.EventID, mustJSON(ev))
	// 窗口 6：效果提交前崩溃 -> 效果不存在，上游重发后重试。
	n.Fault("before_commit:" + ev.EventID)
	tx.Commit()

	// 窗口 7：提交后、ack 前崩溃/丢包 -> 上游重发，命中去重，只重发 ack。
	n.Fault("after_commit:" + ev.EventID)
	d.ack(ev.EventID)
}

func (d *Downstream) ack(eventID string) {
	d.Bus.Send(&sim.Msg{
		From: NodeDS, To: NodeSvc, Kind: "ack",
		ID: eventID, Payload: Ack{EventID: eventID},
	})
}
