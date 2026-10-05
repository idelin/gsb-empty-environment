package sim

import (
	"fmt"
	"sort"
)

// Check 是不变量检查器：只消费证据事件流与最终 Dump，独立于模拟器内部状态。
// 返回违规列表；空表示全部不变量成立。
func Check(ev []Event, d Dump, M int) []string {
	var bad []string
	fail := func(format string, args ...interface{}) {
		bad = append(bad, fmt.Sprintf(format, args...))
	}

	// epoch -> 主节点 映射（来自路由证据与最终配置历史）
	type slotView struct {
		fenced     bool
		fenceSeq   int
		fencedNode string
		decided    bool
		oldPrimary string
	}
	view := map[int]*slotView{}
	for s := 0; s < M; s++ {
		view[s] = &slotView{}
	}
	type okWrite struct {
		slot, epoch, seq int
		node             string
		op, key, val     string
		idem             string
	}
	var oks []okWrite
	seenIdem := map[int]map[string]int{}    // slot -> idem -> seq
	seenSeq := map[int]map[[2]int]string{}  // slot -> (epoch,seq) -> idem
	epochWriter := map[int]map[int]string{} // slot -> epoch -> node（同 epoch 不得两节点接受写）
	epochMax := map[int]int{}

	for _, e := range ev {
		switch e.Kind {
		case "fence":
			v := view[e.Slot]
			v.fenced = true
			v.fenceSeq = e.Seq
			v.fencedNode = e.Node
		case "decision":
			v := view[e.Slot]
			v.decided = true
			v.oldPrimary = e.Peer
			if e.Epoch <= epochMax[e.Slot] {
				fail("槽 %d epoch 未严格递增: 决议 epoch=%d, 已见最大=%d", e.Slot, e.Epoch, epochMax[e.Slot])
			}
			epochMax[e.Slot] = e.Epoch
		case "req":
			if e.Op == "" || (e.Status != "OK" && e.Status != "Dup") {
				continue
			}
			v := view[e.Slot]
			if e.Status == "OK" {
				// 冻结后旧主不得再确认写
				if v.fenced && e.Node == v.fencedNode {
					fail("槽 %d 旧主 %s 在 fence(seq=%d) 后仍确认写 seq=%d", e.Slot, e.Node, v.fenceSeq, e.Seq)
				}
				// 决议后不得回退旧主
				if v.decided && e.Node == v.oldPrimary {
					fail("槽 %d 决议后旧主 %s 又接受写", e.Slot, e.Node)
				}
				// 同 epoch 不得两个节点接受写
				wr, ok := epochWriter[e.Slot]
				if !ok {
					wr = map[int]string{}
					epochWriter[e.Slot] = wr
				}
				if prev, ok := wr[e.Epoch]; ok && prev != e.Node {
					fail("槽 %d epoch %d 有两个节点接受写: %s 与 %s", e.Slot, e.Epoch, prev, e.Node)
				}
				wr[e.Epoch] = e.Node
				// 幂等唯一：同 idem 不得两次 OK
				im, ok := seenIdem[e.Slot]
				if !ok {
					im = map[string]int{}
					seenIdem[e.Slot] = im
				}
				if _, dup := im[e.Idem]; dup {
					fail("槽 %d idem=%s 出现两次 OK（同 epoch 成功写不唯一）", e.Slot, e.Idem)
				}
				im[e.Idem] = e.Seq
				// (epoch,seq) 不得被不同 idem 复用
				sm, ok := seenSeq[e.Slot]
				if !ok {
					sm = map[[2]int]string{}
					seenSeq[e.Slot] = sm
				}
				k := [2]int{e.Epoch, e.Seq}
				if prev, ok := sm[k]; ok && prev != e.Idem {
					fail("槽 %d (epoch=%d,seq=%d) 被 idem=%s 与 %s 复用", e.Slot, e.Epoch, e.Seq, prev, e.Idem)
				}
				sm[k] = e.Idem
				oks = append(oks, okWrite{e.Slot, e.Epoch, e.Seq, e.Node, e.Op, e.Key, e.Val, e.Idem})
			} else { // Dup：必须对应已见过的 idem，且结果一致
				if im, ok := seenIdem[e.Slot]; !ok || im[e.Idem] != e.Seq {
					// Dup 也可能命中迁移带过去的幂等记录（原始 OK 在更早事件里），两者都必须存在
					fail("槽 %d idem=%s 的 Dup(seq=%d) 找不到一致的原始 OK 记录", e.Slot, e.Idem, e.Seq)
				}
			}
		case "conflict":
			fail("槽 %d 节点 %s 检测到同序号不同内容: %s", e.Slot, e.Node, e.Detail)
		}
	}

	// 单副本串行模型：按 (slot, epoch, seq) 重放全部已确认写
	sort.Slice(oks, func(i, j int) bool {
		if oks[i].slot != oks[j].slot {
			return oks[i].slot < oks[j].slot
		}
		if oks[i].epoch != oks[j].epoch {
			return oks[i].epoch < oks[j].epoch
		}
		return oks[i].seq < oks[j].seq
	})
	model := map[int]map[string]string{}
	for _, w := range oks {
		m, ok := model[w.slot]
		if !ok {
			m = map[string]string{}
			model[w.slot] = m
		}
		if w.op == string(OpSet) {
			m[w.key] = w.val
		} else {
			delete(m, w.key)
		}
	}
	// 与最终主节点状态比对
	for _, sd := range d.Slots {
		m := model[sd.Slot]
		// KV 等价
		keys := map[string]bool{}
		for k := range m {
			keys[k] = true
		}
		for k := range sd.KV {
			keys[k] = true
		}
		for k := range keys {
			mv, mok := m[k]
			dv, dok := sd.KV[k]
			if mok != dok || mv != dv {
				fail("槽 %d 键 %q 串行模型=%q(%v) 与最终状态=%q(%v) 不一致", sd.Slot, k, mv, mok, dv, dok)
			}
		}
		// 每条已确认写必须恰好出现在权威日志中一次（不丢不重）
		logSeq := map[int]int{} // seq -> count
		prevSeq := 0
		prevEpoch := 0
		for _, ch := range sd.Log {
			logSeq[ch.Seq]++
			if ch.Seq != prevSeq+1 {
				fail("槽 %d 权威日志序号不连续: %d 后出现 %d", sd.Slot, prevSeq, ch.Seq)
			}
			if ch.Epoch < prevEpoch {
				fail("槽 %d 权威日志 epoch 倒退: %d 后出现 %d", sd.Slot, prevEpoch, ch.Epoch)
			}
			prevSeq, prevEpoch = ch.Seq, ch.Epoch
		}
		for _, w := range oks {
			if w.slot != sd.Slot {
				continue
			}
			if logSeq[w.seq] != 1 {
				fail("槽 %d 已确认写 seq=%d(idem=%s) 在权威日志中出现 %d 次", sd.Slot, w.seq, w.idem, logSeq[w.seq])
			}
		}
	}
	return bad
}
