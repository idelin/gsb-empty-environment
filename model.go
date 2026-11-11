package netsec

import "fmt"

// 消息种类：确认与数据。
const (
	msgConfirm uint8 = iota
	msgData
)

// maxChanCap 是单方向信道容量的上限（State 内嵌数组的固定长度）。
const maxChanCap = 2

// Msg 是信道中的在途消息，只携带其关联的密钥代。
type Msg struct {
	kind uint8
	gen  int8
}

// Endpoint 描述一个协议端点的本地状态。
type Endpoint struct {
	cur       int8  // 当前安装（正在使用）的密钥代
	derived   int8  // 已派生的最高密钥代
	peerAck   uint8 // 位掩码：已收到对端确认的密钥代
	announced uint8 // 位掩码：已向对端宣告（发送过确认）的密钥代
	oldGone   bool  // 上一代密钥是否已销毁
	stuck     bool  // 重试时旧钥已销毁，进入不可恢复状态
}

// State 是全局状态：两个端点加两条有界无序信道。
// ch[i] 为发往端点 i 的在途消息，仅前 n[i] 项有效，且保持排序以便规范化去重。
type State struct {
	ep [2]Endpoint
	ch [2][maxChanCap]Msg
	n  [2]int8
}

// Config 控制模型规模与可用动作。
type Config struct {
	MaxGen           int8 // 最高密钥代；轮换次数至多 MaxGen 次
	ChanCap          int8 // 单方向信道容量，1..maxChanCap
	AllowDup         bool // 允许信道复制消息
	AllowRetry       bool // 允许确认重试动作
	RequireAckToSend bool // 修复变体：仅在当前密钥代被双方共同认可后才允许发数据
}

// DefaultConfig 返回完整模型：至多两次轮换，消息可丢失、可重复，含重试路径。
func DefaultConfig() Config {
	return Config{MaxGen: 2, ChanCap: maxChanCap, AllowDup: true, AllowRetry: true}
}

// Initial 返回初始状态：密钥代 0 已预置，且被双方共同认可、共同宣告。
func Initial(cfg Config) State {
	var s State
	for i := range s.ep {
		s.ep[i] = Endpoint{cur: 0, derived: 0, peerAck: 0b001, announced: 0b001}
	}
	return s
}

func hasBit(mask uint8, g int8) bool {
	return g >= 0 && g < 8 && mask&(1<<uint(g)) != 0
}

// MutualAck 报告密钥代 g 是否被双方共同认可：
// 即两个端点都收到了对方针对 g 的确认。
func MutualAck(s State, g int8) bool {
	return hasBit(s.ep[0].peerAck, g) && hasBit(s.ep[1].peerAck, g)
}

// keyAvailable 报告端点是否仍持有密钥代 g：当前代，或未销毁的上一代。
func keyAvailable(ep Endpoint, g int8) bool {
	return g == ep.cur || (g == ep.cur-1 && !ep.oldGone)
}

// Step 是一次状态迁移。Violates 为真表示该迁移交付了一份明文，
// 而其密钥代在交付时刻未被双方共同认可（违反不变式）。
type Step struct {
	Name     string
	Next     State
	Violates bool
}

var epName = [2]string{"A", "B"}

func msgName(m Msg) string {
	if m.kind == msgConfirm {
		return fmt.Sprintf("confirm(gen%d)", m.gen)
	}
	return fmt.Sprintf("data(gen%d)", m.gen)
}

// sortCh 对信道前 n 项排序，使等价信道状态规范化。
func sortCh(ch *[maxChanCap]Msg, n int8) {
	for i := int8(1); i < n; i++ {
		for j := i; j > 0; j-- {
			x, y := ch[j-1], ch[j]
			if x.kind > y.kind || (x.kind == y.kind && x.gen > y.gen) {
				ch[j-1], ch[j] = y, x
			} else {
				break
			}
		}
	}
}

// send 向发往对端的信道追加一条消息（调用方保证有容量）。
func send(s State, peer int, m Msg) State {
	s.ch[peer][s.n[peer]] = m
	s.n[peer]++
	sortCh(&s.ch[peer], s.n[peer])
	return s
}

// remove 从信道 ch 删除第 idx 条消息。
func remove(s State, ch, idx int8) State {
	copy(s.ch[ch][idx:], s.ch[ch][idx+1:s.n[ch]])
	s.ch[ch][s.n[ch]-1] = Msg{}
	s.n[ch]--
	return s
}

// Successors 枚举状态 s 的全部后继迁移：
// 两个端点的本地动作（派生、安装、确认、重试、销毁、发数据、收消息），
// 以及环境动作（消息丢失、消息重复）。
func Successors(cfg Config, s State) []Step {
	var out []Step
	for a := 0; a < 2; a++ {
		out = append(out, localSteps(cfg, s, a)...)
	}
	for i := 0; i < 2; i++ {
		for idx := int8(0); idx < s.n[i]; idx++ {
			m := s.ch[i][idx]
			// 消息丢失。
			out = append(out, Step{
				Name: fmt.Sprintf("net.lose(to %s): %s", epName[i], msgName(m)),
				Next: remove(s, int8(i), idx),
			})
			// 消息重复。
			if cfg.AllowDup && s.n[i] < cfg.ChanCap {
				ns := s
				ns.ch[i][ns.n[i]] = m
				ns.n[i]++
				sortCh(&ns.ch[i], ns.n[i])
				out = append(out, Step{
					Name: fmt.Sprintf("net.dup(to %s): %s", epName[i], msgName(m)),
					Next: ns,
				})
			}
		}
	}
	return out
}

// localSteps 枚举端点 a 的本地动作。
func localSteps(cfg Config, s State, a int) []Step {
	var out []Step
	ep := s.ep[a]
	peer := 1 - a
	name := epName[a]
	if ep.stuck {
		return nil // 不可恢复：该端点不再有动作
	}

	// 派生：从当前密钥派生下一代，一次只挂起一代。
	if ep.derived < cfg.MaxGen && ep.derived == ep.cur {
		ns := s
		ns.ep[a].derived++
		out = append(out, Step{
			Name: fmt.Sprintf("%s.derive(gen%d)", name, ns.ep[a].derived),
			Next: ns,
		})
	}

	// 安装：切换到已派生的新一代；旧的“上一代”重新变为可销毁。
	if ep.derived > ep.cur {
		ns := s
		ns.ep[a].cur = ns.ep[a].derived
		ns.ep[a].oldGone = false
		out = append(out, Step{
			Name: fmt.Sprintf("%s.install(gen%d)", name, ns.ep[a].cur),
			Next: ns,
		})
	}

	// 确认：向对端宣告当前密钥代。
	if s.n[peer] < cfg.ChanCap {
		ns := send(s, peer, Msg{kind: msgConfirm, gen: ep.cur})
		ns.ep[a].announced |= 1 << uint(ep.cur)
		out = append(out, Step{
			Name: fmt.Sprintf("%s.confirm(gen%d)", name, ep.cur),
			Next: ns,
		})
	}

	// 重试：未收到对端对当前代的确认时尝试重传；
	// 旧钥已销毁则无法回退，进入不可恢复状态。
	if cfg.AllowRetry && ep.cur > 0 && !hasBit(ep.peerAck, ep.cur) {
		if ep.oldGone {
			ns := s
			ns.ep[a].stuck = true
			out = append(out, Step{
				Name: fmt.Sprintf("%s.retry(gen%d) -> stuck: old key destroyed", name, ep.cur),
				Next: ns,
			})
		} else if s.n[peer] < cfg.ChanCap {
			ns := send(s, peer, Msg{kind: msgConfirm, gen: ep.cur})
			ns.ep[a].announced |= 1 << uint(ep.cur)
			out = append(out, Step{
				Name: fmt.Sprintf("%s.retry-confirm(gen%d)", name, ep.cur),
				Next: ns,
			})
		}
	}

	// 销毁：销毁上一代密钥。
	if ep.cur > 0 && !ep.oldGone {
		ns := s
		ns.ep[a].oldGone = true
		out = append(out, Step{
			Name: fmt.Sprintf("%s.destroy(gen%d)", name, ep.cur-1),
			Next: ns,
		})
	}

	// 发数据：用当前密钥代加密发送；仅允许发送已宣告过的密钥代。
	if s.n[peer] < cfg.ChanCap && hasBit(ep.announced, ep.cur) &&
		(!cfg.RequireAckToSend || MutualAck(s, ep.cur)) {
		ns := send(s, peer, Msg{kind: msgData, gen: ep.cur})
		out = append(out, Step{
			Name: fmt.Sprintf("%s.send-data(gen%d)", name, ep.cur),
			Next: ns,
		})
	}

	// 收消息：消费发往本端点的任意一条在途消息。
	for idx := int8(0); idx < s.n[a]; idx++ {
		m := s.ch[a][idx]
		ns := remove(s, int8(a), idx)
		if m.kind == msgConfirm {
			ns.ep[a].peerAck |= 1 << uint(m.gen)
			out = append(out, Step{
				Name: fmt.Sprintf("%s.recv-confirm(gen%d)", name, m.gen),
				Next: ns,
			})
			continue
		}
		// 数据消息：持有对应密钥才能交付明文，否则丢弃。
		if !keyAvailable(ep, m.gen) {
			out = append(out, Step{
				Name: fmt.Sprintf("%s.recv-data(gen%d) dropped: key unavailable", name, m.gen),
				Next: ns,
			})
			continue
		}
		violates := !MutualAck(s, m.gen)
		stepName := fmt.Sprintf("%s.recv-data(gen%d) delivered", name, m.gen)
		if violates {
			stepName = fmt.Sprintf("%s.recv-data(gen%d) DELIVERED -- VIOLATION: gen%d not mutually acknowledged", name, m.gen, m.gen)
		}
		out = append(out, Step{Name: stepName, Next: ns, Violates: violates})
	}
	return out
}
