package main

import (
	"fmt"
	"math/rand"
	"strings"
)

// ---------------------------------------------------------------------------
// 日志项
// ---------------------------------------------------------------------------

type Item struct {
	Seq  int
	Data string
}

func makeItems(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		items[i] = Item{Seq: i, Data: fmt.Sprintf("log-%04d", i)}
	}
	return items
}

// ---------------------------------------------------------------------------
// 发送方：维护 已发送水位(sent) 与 累计持久化确认水位(acked)
//   - sent  : 下一个待发送的下标，即 [0, sent) 已经交给过连接（不代表已持久化）
//   - acked : 接收方已持久化的连续前缀长度，只有它才构成“可提交前缀”
// ---------------------------------------------------------------------------

type Sender struct {
	items []Item
	sent  int // 已发送水位（不含持久化语义）
	acked int // 累计持久化确认水位（可提交前缀）
}

func NewSender(items []Item) *Sender {
	return &Sender{items: items}
}

// OnAck 处理累计确认。规则：
//   1. ack <= acked：旧确认/重复确认，忽略 —— 确认绝不能降低水位；
//   2. ack >  sent ：确认越过了已发送水位，非法，忽略并告警；
//   3. 其余情况推进 acked。
func (s *Sender) OnAck(ack int) bool {
	switch {
	case ack <= s.acked:
		fmt.Printf("    [sender] 忽略过期/重复确认 ack=%d（当前已确认水位 %d，不允许回退）\n", ack, s.acked)
		return false
	case ack > s.sent:
		fmt.Printf("    [sender] 拒绝非法确认 ack=%d：越过已发送水位 %d\n", ack, s.sent)
		return false
	default:
		s.acked = ack
		fmt.Printf("    [sender] 接受累计确认 ack=%d（可提交前缀推进到 [0,%d)）\n", ack, ack)
		return true
	}
}

// CommittedPrefix 可提交前缀 = 已持久化确认水位，与“是否发送完成”无关。
func (s *Sender) CommittedPrefix() int { return s.acked }

// ---------------------------------------------------------------------------
// 接收方：只承认连续持久化前缀；模拟短写与断链
// ---------------------------------------------------------------------------

type Receiver struct {
	persisted int // 已持久化的连续前缀长度
}

// ReceiveBatch 接收一批 [lo, hi) 的日志项。
// 模拟短写：可能只持久化该批的前 k 项（0<=k<=batch）就“断线”。
// 返回新的持久化水位与是否仍在线。
func (r *Receiver) ReceiveBatch(lo, hi int, rng *rand.Rand) (newWatermark int, alive bool) {
	batch := hi - lo
	// 短写：随机持久化 [0, batch] 中的一部分
	k := rng.Intn(batch + 1)
	r.persisted = lo + k // 只接受与已有前缀连续的部分
	// 持久化整批则有较大概率保持在线，否则视为断链
	alive = k == batch && rng.Intn(100) < 70
	return r.persisted, alive
}

// ---------------------------------------------------------------------------
// 模拟器
// ---------------------------------------------------------------------------

func simulate(total, batchSize int, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	sender := NewSender(makeItems(total))
	receiver := &Receiver{}

	fmt.Printf("=== 开始模拟：共 %d 条日志，批大小 %d ===\n", total, batchSize)

	round := 0
	for sender.acked < total {
		round++
		// ---- （重）连接：接收方上报自己的持久化水位，发送方据此计算重发区间 ----
		resendLo := sender.acked
		resendHi := sender.sent
		if resendLo < resendHi {
			fmt.Printf("\n[第 %d 轮] 重新连接，接收方水位=%d，发送方已发送水位=%d\n",
				round, receiver.persisted, sender.sent)
			fmt.Printf("  >>> 重发区间 [%d, %d)，随后续发新项\n", resendLo, resendHi)
		} else {
			fmt.Printf("\n[第 %d 轮] 建立连接，无需重发（已确认水位=%d）\n", round, sender.acked)
		}

		// ---- 从重发起点开始，按批发送直到断链或发完全部 ----
		cursor := resendLo
		for cursor < total {
			hi := cursor + batchSize
			if hi > total {
				hi = total
			}
			fmt.Printf("  [sender] 发送批次 [%d, %d)：%s ... %s\n",
				cursor, hi, sender.items[cursor].Data, sender.items[hi-1].Data)
			sender.sent = hi // 发送完成 ≠ 提交：仅推进已发送水位

			wm, alive := receiver.ReceiveBatch(cursor, hi, rng)
			if wm < hi {
				fmt.Printf("  [receiver] 短写！批 [%d,%d) 只持久化到 %d\n", cursor, hi, wm)
			} else {
				fmt.Printf("  [receiver] 批次完整持久化，水位=%d\n", wm)
			}
			sender.OnAck(wm) // 累计确认（带水位校验）

			if !alive {
				fmt.Println("  [receiver] *** 连接断开 ***")
				break
			}
			cursor = hi
		}

		// ---- 故障注入：演示旧确认不得降低水位、确认不得越过已发送水位 ----
		if round == 1 {
			fmt.Println("  [fault] 注入乱序旧确认 ack=0（上一连接的迟到包）")
			sender.OnAck(0)
		}
		if round == 2 && sender.sent < total {
			fmt.Printf("  [fault] 注入伪造确认 ack=%d（越过已发送水位 %d）\n", total, sender.sent)
			sender.OnAck(total)
		}
	}

	// ---- 收尾汇报 ----
	fmt.Println("\n=== 模拟结束 ===")
	fmt.Printf("已发送水位 sent  = %d（发送完成不等于提交）\n", sender.sent)
	fmt.Printf("可提交前缀 acked = [0, %d)\n", sender.CommittedPrefix())
	if sender.CommittedPrefix() < total {
		fmt.Printf("未提交尾部       = [%d, %d)，需后续重发\n", sender.CommittedPrefix(), total)
	} else {
		fmt.Println("全部日志均已持久化，可整体提交。")
	}

	// 不变式自检
	var problems []string
	if sender.acked > sender.sent {
		problems = append(problems, "确认水位越过已发送水位")
	}
	if receiver.persisted != sender.acked {
		problems = append(problems, fmt.Sprintf("收发水位不一致：recv=%d sender=%d", receiver.persisted, sender.acked))
	}
	if len(problems) > 0 {
		fmt.Println("不变式检查失败: " + strings.Join(problems, "; "))
	} else {
		fmt.Println("不变式检查通过：acked <= sent，且与接收方持久化水位一致。")
	}
}

func main() {
	simulate(25, 6, 42)
	fmt.Println()
	simulate(18, 5, 7)
}
