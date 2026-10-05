package sim

import "fmt"

// Migration 迁移控制器的持久化记录。
type Migration struct {
	Slot        int    `json:"slot"`
	Source      string `json:"source"`
	Target      string `json:"target"`
	BaselineSeq int    `json:"baselineSeq"` // 基线截止序号
	FinalSeq    int    `json:"finalSeq"`    // 冻结时确定的最终序号
	SentUpTo    int    `json:"sentUpTo"`    // 已发送到的序号（恢复时回退到目标已确认处）
	Phase       string `json:"phase"`       // Baseline/Delta/Frozen/CaughtUp/Done
	NewEpoch    int    `json:"newEpoch,omitempty"`
}

// StartMigration 启动迁移：目标必须与源不同故障域；记录基线序号。
func (c *Cluster) StartMigration(slot int, target string) error {
	if _, ok := c.Migs[slot]; ok {
		return fmt.Errorf("槽 %d 已有进行中的迁移", slot)
	}
	src := c.Cfg.Primary[slot]
	if target == src {
		return fmt.Errorf("槽 %d: 目标不能与源相同", slot)
	}
	tn, ok := c.Nodes[target]
	if !ok {
		return fmt.Errorf("槽 %d: 目标节点 %s 不存在", slot, target)
	}
	if tn.Domain != "" && tn.Domain == c.Nodes[src].Domain {
		return fmt.Errorf("槽 %d: 目标 %s 与源 %s 同故障域 %s，拒绝", slot, target, src, tn.Domain)
	}
	base := c.Nodes[src].Disk.Slots[slot].lastSeq()
	c.Migs[slot] = &Migration{Slot: slot, Source: src, Target: target, BaselineSeq: base, Phase: "Baseline"}
	c.emit(Event{Kind: "phase", Slot: slot, Phase: "Baseline",
		Detail: fmt.Sprintf("迁移开始 %s->%s, 基线序号=%d", src, target, base)})
	return nil
}

// Pump 从源向全局缓存推送复制消息（基线段或增量段），受 B 字节背压限制。
// 返回实际入队条数；缓存满时立即返回（背压），源记录保留不丢。
func (c *Cluster) Pump(slot, maxMsgs int) int {
	mig, ok := c.Migs[slot]
	if !ok || mig.Phase == "Done" {
		return 0
	}
	src := c.Nodes[mig.Source]
	s := src.Disk.Slots[slot]
	limit := 0
	switch mig.Phase {
	case "Baseline":
		limit = mig.BaselineSeq
	case "Delta", "Frozen", "CaughtUp":
		limit = s.lastSeq() // 冻结后 lastSeq == FinalSeq
	}
	sent := 0
	for mig.SentUpTo < limit && sent < maxMsgs {
		ch := s.Log[mig.SentUpTo] // seq = SentUpTo+1
		msg := Msg{Slot: slot, Ch: ch, To: mig.Target, Size: entrySize(ch)}
		if !c.tryPush(msg) {
			c.emit(Event{Kind: "backpressure", Slot: slot, Seq: ch.Seq,
				Detail: fmt.Sprintf("全局缓存已满(B=%d, 在途=%d, 暂存=%d)，背压暂停发送", c.B, c.BufUsed, c.PendingBytes)})
			return sent
		}
		mig.SentUpTo++
		sent++
	}
	return sent
}

// maybeAdvance 在目标确认前缀推进后更新迁移阶段（持久化在控制器记录里）。
func (c *Cluster) maybeAdvance(slot int) {
	mig, ok := c.Migs[slot]
	if !ok {
		return
	}
	confirmed := c.Nodes[mig.Target].Disk.Slots[slot].ConfirmedSeq
	switch mig.Phase {
	case "Baseline":
		if confirmed >= mig.BaselineSeq {
			mig.Phase = "Delta"
			c.emit(Event{Kind: "phase", Slot: slot, Phase: "Delta",
				Detail: fmt.Sprintf("基线复制完成(seq<=%d)，进入增量同步", mig.BaselineSeq)})
		}
	case "Frozen":
		if confirmed >= mig.FinalSeq {
			mig.Phase = "CaughtUp"
			c.emit(Event{Kind: "phase", Slot: slot, Phase: "CaughtUp",
				Detail: fmt.Sprintf("目标已持久化完整前缀(seq<=%d)，可决议切换", mig.FinalSeq)})
		}
	}
}

// Freeze 冻结旧主：确定最终序号并持久化 fence，此后旧主拒绝一切新写。
func (c *Cluster) Freeze(slot int) error {
	mig, ok := c.Migs[slot]
	if !ok {
		return fmt.Errorf("槽 %d 无迁移", slot)
	}
	src := c.Nodes[mig.Source]
	s := src.Disk.Slots[slot]
	s.Fenced = true
	s.FenceSeq = s.lastSeq()
	mig.FinalSeq = s.FenceSeq
	mig.Phase = "Frozen"
	c.emit(Event{Kind: "fence", Slot: slot, Node: mig.Source, Seq: mig.FinalSeq, Phase: "Frozen",
		Detail: fmt.Sprintf("旧主冻结，最终序号=%d，fence 已持久化", mig.FinalSeq)})
	c.maybeAdvance(slot)
	return nil
}

// Decide 持久化路由切换决议：仅当目标已持久化完整前缀才允许；
// 新主以新 epoch 接受写入；旧主 fence 永不解除（决议后不得回退）。
func (c *Cluster) Decide(slot int) error {
	mig, ok := c.Migs[slot]
	if !ok {
		return fmt.Errorf("槽 %d 无迁移", slot)
	}
	if mig.Phase == "Done" {
		return fmt.Errorf("槽 %d 迁移已完成", slot)
	}
	tgt := c.Nodes[mig.Target].Disk.Slots[slot]
	if tgt.ConfirmedSeq < mig.FinalSeq {
		return fmt.Errorf("槽 %d: 目标仅确认到 %d，不足最终序号 %d，禁止切换", slot, tgt.ConfirmedSeq, mig.FinalSeq)
	}
	old := c.Cfg
	newEpoch := old.Epoch[slot] + 1
	dec := Decision{Slot: slot, NewPrimary: mig.Target, OldPrimary: mig.Source, Epoch: newEpoch, FinalSeq: mig.FinalSeq}
	c.Decisions[slot] = dec // 决议先持久化，再换路由
	nc := &Config{
		Version: old.Version + 1,
		Primary: append([]string(nil), old.Primary...),
		Replica: append([]string(nil), old.Replica...),
		Epoch:   append([]int(nil), old.Epoch...),
	}
	nc.Primary[slot] = mig.Target
	nc.Epoch[slot] = newEpoch
	// 为新主重选不同故障域的备节点；缺合法备节点时显式报告
	nodes := make([]Node, 0, len(c.Nodes))
	for _, id := range c.Order {
		n := c.Nodes[id]
		nodes = append(nodes, Node{ID: n.ID, Domain: n.Domain})
	}
	rep, err := PickReplica(nodes, c.M, nc.Primary)
	if err != nil {
		c.emit(Event{Kind: "note", Slot: slot, Detail: "备节点重选失败: " + err.Error()})
	} else {
		nc.Replica = rep
	}
	c.Cfg = nc
	c.CfgHistory = append(c.CfgHistory, nc)
	tgtNode := c.Nodes[mig.Target]
	tgtNode.Disk.Slots[slot].Epoch = newEpoch // 决议持久化后新主才可接受写
	mig.Phase = "Done"
	mig.NewEpoch = newEpoch
	changed := DiffConfig(old, nc)
	c.emit(Event{Kind: "decision", Slot: slot, Node: mig.Target, Peer: mig.Source, Epoch: newEpoch, Seq: mig.FinalSeq,
		Detail: fmt.Sprintf("切换决议持久化: %s->%s, epoch=%d, finalSeq=%d", mig.Source, mig.Target, newEpoch, mig.FinalSeq)})
	c.emit(Event{Kind: "route", Version: nc.Version, Detail: fmt.Sprintf("换主槽位集合=%v", changed)})
	// 切换回执通知旧主：允许丢失；旧主 fence 已持久化，永不回退。
	return nil
}
