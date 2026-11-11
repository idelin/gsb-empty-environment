package netem

import "example.org/rollback/netcode"

// Relay 是一个帧驱动的对战中继（可视为极简服务端）：
// 它不解释模拟内容，只按 ToPlayer 把消息经各自的链路转发给目标玩家。
//
// 两条方向链路使用独立的 Emulator，从而能模拟非对称延迟与丢包。
type Relay struct {
	links [2]*Emulator // links[p] 是“发往玩家 p”的链路
}

// NewRelay 用两条链路构造中继。links[0] 通向玩家 0，links[1] 通向玩家 1。
func NewRelay(links [2]*Emulator) *Relay {
	return &Relay{links: links}
}

// Forward 接收一名玩家发出的消息并送入通向对端的链路。
func (r *Relay) Forward(b netcode.Bundle) {
	if b.ToPlayer != 0 && b.ToPlayer != 1 {
		return
	}
	r.links[b.ToPlayer].Send(b)
}

// Tick 推进两条链路一个时钟，返回每名玩家此刻收到的消息。
func (r *Relay) Tick() [2][]netcode.Bundle {
	var delivered [2][]netcode.Bundle
	for p := 0; p < 2; p++ {
		delivered[p] = r.links[p].Tick()
	}
	return delivered
}
