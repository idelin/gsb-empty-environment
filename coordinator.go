package twopc

// 协调者事务状态。Votes 是易失的：崩溃后丢失，
// 恢复时只看 WAL 中是否存在持久决策记录（CDecision）。
type CoordTx struct {
	ID       string
	Parts    []string
	Votes    map[string]bool
	Decision string // "" / "COMMIT" / "ABORT"
}

type Coordinator struct {
	ID  string
	WAL *WAL
	Txs map[string]*CoordTx

	send func(Message)

	// HoldDecision 模拟"收齐全部 YES 后、写决策记录前"的崩溃窗口（测试用）。
	HoldDecision bool
	// AfterDecisionDurable 在决策记录 fsync 之后、决策消息发出之前触发（故障注入点）。
	AfterDecisionDurable func()
}

func NewCoordinator(id string, wal *WAL, send func(Message)) *Coordinator {
	c := &Coordinator{ID: id, WAL: wal, send: send}
	c.recoverState()
	return c
}

// recoverState 仅根据 WAL 重建内存状态（不发送任何消息）。
func (c *Coordinator) recoverState() {
	c.Txs = map[string]*CoordTx{}
	for _, r := range c.WAL.Records() {
		switch r.Kind {
		case RBegin:
			c.Txs[r.TxID] = &CoordTx{ID: r.TxID, Parts: r.Parts, Votes: map[string]bool{}}
		case RDecision:
			if tx := c.Txs[r.TxID]; tx != nil {
				tx.Decision = r.Decision
			}
		}
	}
}

// Recover 在崩溃重启后调用：严格按日志决策。
//   - 日志中已有决策记录 -> 重放该决策（重发，参与者端幂等）；
//   - 日志中无决策记录 -> 不能"猜"提交，按 presumptive abort 补写 ABORT 并广播。
func (c *Coordinator) Recover() {
	c.recoverState()
	for _, tx := range c.Txs {
		if tx.Decision == "" {
			c.decide(tx, "ABORT")
		} else {
			c.broadcast(tx)
		}
	}
}

// Start 开始一个事务：先写 CBegin，再向所有参与者发 prepare。
func (c *Coordinator) Start(txID string, parts []string) {
	_ = c.WAL.Append(Record{Kind: RBegin, TxID: txID, Parts: parts})
	c.Txs[txID] = &CoordTx{ID: txID, Parts: parts, Votes: map[string]bool{}}
	for _, p := range parts {
		c.send(Message{From: c.ID, To: p, Type: "prepare", TxID: txID})
	}
}

func (c *Coordinator) OnVote(m Message) {
	tx := c.Txs[m.TxID]
	if tx == nil {
		return
	}
	if tx.Decision != "" {
		// 已决策：迟到的投票直接回应当前持久决策（帮助阻塞中的参与者收尾）。
		c.sendDecisionTo(tx, m.From)
		return
	}
	tx.Votes[m.From] = m.Yes
	if !m.Yes {
		c.decide(tx, "ABORT") // 一票否决
		return
	}
	if c.HoldDecision {
		return // 模拟收齐 YES 后尚未写决策的窗口
	}
	if len(tx.Votes) == len(tx.Parts) {
		c.decide(tx, "COMMIT")
	}
}

func (c *Coordinator) OnAck(m Message) {}

// decide 先把决策记录 fsync 落盘，再广播——这是安全性的核心：
// 任何 commit 消息发出之前，COMMIT 必须已经持久化，恢复时才能照日志重放。
func (c *Coordinator) decide(tx *CoordTx, d string) {
	if tx.Decision != "" {
		return
	}
	tx.Decision = d
	_ = c.WAL.Append(Record{Kind: RDecision, TxID: tx.ID, Decision: d})
	if c.AfterDecisionDurable != nil {
		c.AfterDecisionDurable()
	}
	c.broadcast(tx)
}

func (c *Coordinator) broadcast(tx *CoordTx) {
	for _, p := range tx.Parts {
		c.sendDecisionTo(tx, p)
	}
}

func (c *Coordinator) sendDecisionTo(tx *CoordTx, part string) {
	typ := "commit"
	if tx.Decision == "ABORT" {
		typ = "abort"
	}
	c.send(Message{From: c.ID, To: part, Type: typ, TxID: tx.ID})
}
