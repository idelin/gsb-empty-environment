// Command demo 用确定性脚本在离散事件模型中演示乱序重排器：
// 同 tick 按序号释放、丢包等待超时、缓存上限溢出、迟到/重复/陈旧
// 旁路、epoch 重置不串流。全程整数 tick，无真实网络流量。
package main

import (
	"fmt"

	"reorder"
	"reorder/sim"
)

func main() {
	const (
		maxWait  = 4 // 缺失序号最多等待 4 个 tick
		cacheCap = 3 // 乱序缓存最多 3 个包
	)

	rec, err := reorder.New(reorder.Config{MaxWaitTicks: maxWait, CacheCap: cacheCap}, 0)
	if err != nil {
		panic(err)
	}
	s := sim.NewScheduler()

	// 接收方动作：在到达事件的当前 tick 交给重排器。
	recv := func(p reorder.Packet) {
		if err := rec.Handle(p, s.Now()); err != nil {
			panic(err)
		}
	}

	// 确定性链路脚本：按序号给定延迟/丢包。
	link := sim.ScriptLink{
		Offset: func(p reorder.Packet) int64 {
			switch p.Seq {
			case 1:
				return 3 // 1@3
			case 2:
				return 5 // 2@5（与 3 乱序）
			case 3:
				return 3 // 3@4（发送 tick 1 + 3）
			case 4:
				return 5 // 4@6
			case 6:
				return 5 // 6@7
			case 7:
				return 5 // 7@8
			case 8:
				return 6 // 8@9
			case 9:
				return 6 // 9@10
			case 11:
				return 6 // 11@11
			default:
				return 4
			}
		},
		Drop: func(p reorder.Packet) bool {
			return p.Seq == 5 || p.Seq == 10 // 5、10 丢失
		},
	}

	// 发送方：事件时间随序号单调；同事件时间的包靠序号区分。
	sends := []reorder.Packet{
		{Epoch: 0, Seq: 1, Tick: 0, Body: "A"},
		{Epoch: 0, Seq: 2, Tick: 0, Body: "B"}, // 与 1 同一事件 tick
		{Epoch: 0, Seq: 3, Tick: 1, Body: "C"},
		{Epoch: 0, Seq: 4, Tick: 1, Body: "D"},
		{Epoch: 0, Seq: 5, Tick: 2, Body: "E"}, // 链路丢弃
		{Epoch: 0, Seq: 6, Tick: 2, Body: "F"},
		{Epoch: 0, Seq: 7, Tick: 3, Body: "G"},
		{Epoch: 0, Seq: 8, Tick: 3, Body: "H"},
		{Epoch: 0, Seq: 9, Tick: 4, Body: "I"},
		{Epoch: 0, Seq: 10, Tick: 5, Body: "J"}, // 链路丢弃
		{Epoch: 0, Seq: 11, Tick: 5, Body: "K"},
	}
	for _, p := range sends {
		p := p
		if err := link.Send(s, p.Tick, p, recv); err != nil {
			panic(err)
		}
	}

	// 额外注入的特殊事件。
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	// seq1 的重复副本在 tick 12 到达 -> 重复旁路。
	must(s.Schedule(12, func(int64) []sim.Event { recv(sends[0]); return nil }))
	// seq5 的迟到副本在缺口早已跳过后的 tick 16 到达 -> 迟到旁路。
	must(s.Schedule(16, func(int64) []sim.Event { recv(sends[4]); return nil }))
	// tick 18 重置 epoch 到 1，清空队列。
	must(s.Schedule(18, func(int64) []sim.Event {
		fmt.Println("---- reset epoch -> 1 @ tick 18 ----")
		must(rec.Reset(1, s.Now()))
		return nil
	}))
	// 旧 epoch 残留包 tick 19 到达 -> 陈旧旁路，不串入新流。
	must(s.Schedule(19, func(int64) []sim.Event {
		recv(reorder.Packet{Epoch: 0, Seq: 12, Tick: 9, Body: "old"})
		return nil
	}))
	// 新 epoch：3 先到，1、2 随后，验证新流从序号 1 重新排序。
	must(s.Schedule(22, func(int64) []sim.Event {
		recv(reorder.Packet{Epoch: 1, Seq: 3, Tick: 20, Body: "X"})
		return nil
	}))
	must(s.Schedule(23, func(int64) []sim.Event {
		recv(reorder.Packet{Epoch: 1, Seq: 1, Tick: 20, Body: "Y"})
		return nil
	}))
	must(s.Schedule(24, func(int64) []sim.Event {
		recv(reorder.Packet{Epoch: 1, Seq: 2, Tick: 21, Body: "Z"})
		return nil
	}))

	// 每个 tick（事件处理完后）驱动接收器时钟，等待超时在此触发。
	must(s.RunUntil(26, func(tick int64) { must(rec.Tick(tick)) }))
	rec.Close()

	fmt.Println("---- ordered output ----")
	for it := range rec.Out() {
		switch {
		case it.Pkg != nil:
			fmt.Printf("[release] epoch=%d seq=%-2d eventTick=%-2d body=%s\n",
				it.Pkg.Epoch, it.Pkg.Seq, it.Pkg.Tick, it.Pkg.Body)
		case it.Gap != nil:
			fmt.Printf("[gap ]      seq %d..%d @tick=%d reason=%s\n",
				it.Gap.From, it.Gap.To, it.Gap.Tick, it.Gap.Reason)
		}
	}

	fmt.Println("---- side channel ----")
	for sd := range rec.Side() {
		fmt.Printf("[side] %-9s epoch=%d seq=%-2d @tick=%d\n",
			sd.Kind, sd.Pkg.Epoch, sd.Pkg.Seq, sd.At)
	}

	st := rec.Stats()
	fmt.Println("---- stats ----")
	fmt.Printf("released=%d gaps=%d missingSeq=%d late=%d stale=%d duplicate=%d\n",
		st.Released, st.Gaps, st.MissingSeq, st.Late, st.Stale, st.Duplicate)
}
