package netsec

import "fmt"

// RefConfig 是供显式小状态参考核对使用的受限配置：
// 仅一次轮换（密钥代 0/1）、单消息信道、无重复、无重试。
func RefConfig() Config {
	return Config{MaxGen: 1, ChanCap: 1}
}

// RefCheck 用一份刻意独立、朴素写法的参考模型重放 RefConfig 的语义，
// 返回可达状态总数与违例首次出现的深度（无违例时为 -1）。
// 它与 model.go 不共享任何实现，用于交叉核对 CountReachable 的结果。
func RefCheck() (states int, violDepth int) {
	start := refState{}
	for i := range start.ep {
		start.ep[i].ack = 1 // 密钥代 0 预置且已被双方认可
		start.ep[i].ann = 1
	}
	seen := map[string]bool{refKey(start): true}
	type item struct {
		s refState
		d int
	}
	queue := []item{{start, 0}}
	violDepth = -1
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		nexts, viol := refSucc(it.s)
		if viol && violDepth < 0 {
			violDepth = it.d + 1
		}
		for _, ns := range nexts {
			k := refKey(ns)
			if seen[k] {
				continue
			}
			seen[k] = true
			queue = append(queue, item{ns, it.d + 1})
		}
	}
	return len(seen), violDepth
}

// --- 以下为参考模型的显式小状态定义，与 model.go 独立 ---

// refMsg 是在途消息：confirm 为真表示确认消息，否则为数据消息。
type refMsg struct {
	confirm bool
	gen     int
}

// refEP 是参考端点状态（无重试，故无 stuck）。
type refEP struct {
	cur, der int
	ack, ann uint
	oldGone  bool
}

// refState 是参考全局状态；ch[i] 为发往端点 i 的消息，容量 1（长度 0 或 1）。
type refState struct {
	ep [2]refEP
	ch [2][]refMsg
}

func refKey(s refState) string {
	mk := func(ms []refMsg) string {
		if len(ms) == 0 {
			return "-"
		}
		if ms[0].confirm {
			return fmt.Sprintf("C%d", ms[0].gen)
		}
		return fmt.Sprintf("D%d", ms[0].gen)
	}
	return fmt.Sprintf("%d%d%d%d%v|%d%d%d%d%v|%s|%s",
		s.ep[0].cur, s.ep[0].der, s.ep[0].ack, s.ep[0].ann, s.ep[0].oldGone,
		s.ep[1].cur, s.ep[1].der, s.ep[1].ack, s.ep[1].ann, s.ep[1].oldGone,
		mk(s.ch[0]), mk(s.ch[1]))
}

func refMutual(s refState, g int) bool {
	bit := uint(1) << uint(g)
	return s.ep[0].ack&bit != 0 && s.ep[1].ack&bit != 0
}

func refHasKey(ep refEP, g int) bool {
	return g == ep.cur || (g == ep.cur-1 && !ep.oldGone)
}

// refSucc 枚举参考状态的全部后继；违反不变式的迁移目标被剔除，
// 并通过第二个返回值报告存在违例迁移。
func refSucc(s refState) (nexts []refState, viol bool) {
	for a := 0; a < 2; a++ {
		ep := s.ep[a]
		peer := 1 - a

		// 派生下一代密钥。
		if ep.der < 1 && ep.der == ep.cur {
			ns := s
			ns.ep[a].der++
			nexts = append(nexts, ns)
		}
		// 安装已派生的密钥。
		if ep.der > ep.cur {
			ns := s
			ns.ep[a].cur = ns.ep[a].der
			ns.ep[a].oldGone = false
			nexts = append(nexts, ns)
		}
		// 发送确认。
		if len(s.ch[peer]) < 1 {
			ns := s
			ns.ch[peer] = []refMsg{{confirm: true, gen: ep.cur}}
			ns.ep[a].ann |= 1 << uint(ep.cur)
			nexts = append(nexts, ns)
		}
		// 销毁旧钥。
		if ep.cur > 0 && !ep.oldGone {
			ns := s
			ns.ep[a].oldGone = true
			nexts = append(nexts, ns)
		}
		// 发送数据（仅限已宣告的密钥代）。
		if len(s.ch[peer]) < 1 && ep.ann&(1<<uint(ep.cur)) != 0 {
			ns := s
			ns.ch[peer] = []refMsg{{confirm: false, gen: ep.cur}}
			nexts = append(nexts, ns)
		}
		// 接收消息。
		if len(s.ch[a]) == 1 {
			m := s.ch[a][0]
			ns := s
			ns.ch[a] = nil
			if m.confirm {
				ns.ep[a].ack |= 1 << uint(m.gen)
				nexts = append(nexts, ns)
			} else if !refHasKey(ep, m.gen) {
				nexts = append(nexts, ns) // 无法解密，丢弃
			} else if !refMutual(s, m.gen) {
				viol = true // 交付了未被双方共同认可的明文
			} else {
				nexts = append(nexts, ns)
			}
		}
	}
	// 环境动作：任一方向上的消息丢失。
	for i := 0; i < 2; i++ {
		if len(s.ch[i]) == 1 {
			ns := s
			ns.ch[i] = nil
			nexts = append(nexts, ns)
		}
	}
	return nexts, viol
}
