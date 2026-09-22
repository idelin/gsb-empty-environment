package twopc

// 参与者。Txs / LockedBy / Applied 均为易失状态，崩溃后仅从 WAL 重建。
type Participant struct {
	ID  string
	WAL *WAL

	Txs        map[string]string // txid -> prepared / committed / aborted
	LockedBy   string            // 当前持有资源锁的事务（"" 表示空闲）
	Applied    []string          // 已提交生效的事务（模拟资源写入）
	Violations []string          // 安全性违例记录（测试中必须为空）

	send func(Message)

	// AfterPromiseDurable 在 promise 记录 fsync 之后、YES 投票发出之前触发（故障注入点）。
	AfterPromiseDurable func()
}

func NewParticipant(id string, wal *WAL, send func(Message)) *Participant {
	p := &Participant{ID: id, WAL: wal, send: send}
	p.recoverState()
	return p
}

// recoverState 重放 WAL：未完成的 promise 恢复为 prepared 并重新占锁
// （参与者崩溃不丢承诺，重启后仍处于阻塞窗口，等待协调者决策）。
func (p *Participant) recoverState() {
	p.Txs = map[string]string{}
	p.LockedBy = ""
	p.Applied = nil
	for _, r := range p.WAL.Records() {
		switch r.Kind {
		case RPromise:
			p.Txs[r.TxID] = "prepared"
			p.LockedBy = r.TxID
		case RDone:
			p.Txs[r.TxID] = r.Outcome
			if p.LockedBy == r.TxID {
				p.LockedBy = ""
			}
			if r.Outcome == "committed" {
				p.Applied = append(p.Applied, r.TxID)
			}
		}
	}
}

// Recover 在崩溃重启后调用：仅重建状态，不擅自行动（阻塞等待决策）。
func (p *Participant) Recover() { p.recoverState() }

// OnTimeout 模拟参与者侧的重传定时器：对已 promise 但未决的事务重发 YES，
// 推动已恢复的协调者重新决策或重放持久决策。
func (p *Participant) OnTimeout(coord string) {
	for tx, st := range p.Txs {
		if st == "prepared" {
			p.send(Message{From: p.ID, To: coord, Type: "vote", TxID: tx, Yes: true})
		}
	}
}

func (p *Participant) OnPrepare(m Message) {
	switch p.Txs[m.TxID] {
	case "prepared":
		// 重复 prepare：承诺仍有效，重答 YES（不重复加锁）。
		p.send(Message{From: p.ID, To: m.From, Type: "vote", TxID: m.TxID, Yes: true})
	case "committed", "aborted":
		// 迟到 prepare：事务已有终态（含 abort 墓碑），拒绝且绝不重新占锁。
		p.send(Message{From: p.ID, To: m.From, Type: "vote", TxID: m.TxID, Yes: false})
	default:
		if p.LockedBy != "" {
			// 资源被其他事务锁住：一票否决。
			p.send(Message{From: p.ID, To: m.From, Type: "vote", TxID: m.TxID, Yes: false})
			return
		}
		// 先写 promise 并 fsync（此刻起进入阻塞窗口），再投 YES。
		_ = p.WAL.Append(Record{Kind: RPromise, TxID: m.TxID})
		if p.AfterPromiseDurable != nil {
			p.AfterPromiseDurable()
		}
		p.Txs[m.TxID] = "prepared"
		p.LockedBy = m.TxID
		p.send(Message{From: p.ID, To: m.From, Type: "vote", TxID: m.TxID, Yes: true})
	}
}

func (p *Participant) OnCommit(m Message) {
	switch p.Txs[m.TxID] {
	case "committed":
		// 重复 commit：幂等，直接确认，不重复应用、不重复写日志。
	case "aborted":
		p.Violations = append(p.Violations, "commit after abort: "+m.TxID)
	case "prepared":
		_ = p.WAL.Append(Record{Kind: RDone, TxID: m.TxID, Outcome: "committed"})
		p.Txs[m.TxID] = "committed"
		p.LockedBy = ""
		p.Applied = append(p.Applied, m.TxID)
	default:
		p.Violations = append(p.Violations, "commit for unknown tx: "+m.TxID)
	}
	p.send(Message{From: p.ID, To: m.From, Type: "ack", TxID: m.TxID})
}

func (p *Participant) OnAbort(m Message) {
	switch p.Txs[m.TxID] {
	case "aborted":
		// 重复 abort：幂等。
	case "committed":
		p.Violations = append(p.Violations, "abort after commit: "+m.TxID)
	case "prepared":
		_ = p.WAL.Append(Record{Kind: RDone, TxID: m.TxID, Outcome: "aborted"})
		p.Txs[m.TxID] = "aborted"
		p.LockedBy = ""
	default:
		// 未知事务的 abort：写中止墓碑（presumptive abort），
		// 之后迟到的 prepare 只能得到 NO，不能重新占锁。
		_ = p.WAL.Append(Record{Kind: RDone, TxID: m.TxID, Outcome: "aborted"})
		p.Txs[m.TxID] = "aborted"
	}
	p.send(Message{From: p.ID, To: m.From, Type: "ack", TxID: m.TxID})
}
