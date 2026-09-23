package main

import "math/rand"

// MsgKind 标识消息类型，用于故障注入与统计。
type MsgKind int

const (
	MsgReadReq MsgKind = iota
	MsgReadResp
	MsgWriteReq
	MsgRepairReq
	MsgRepairResp
)

func (k MsgKind) String() string {
	switch k {
	case MsgReadReq:
		return "ReadReq"
	case MsgReadResp:
		return "ReadResp"
	case MsgWriteReq:
		return "WriteReq"
	case MsgRepairReq:
		return "RepairReq"
	case MsgRepairResp:
		return "RepairResp"
	}
	return "?"
}

// CoordinatorID 表示协调者（客户端侧）的虚拟节点 ID。
const CoordinatorID = -1

// Network 是模拟网络：消息经过随机虚拟延迟后投递；
// DropHook 返回 true 时消息被丢弃（故障注入点）。
type Network struct {
	sim      *Sim
	MinLat   int64
	MaxLat   int64
	rng      *rand.Rand
	DropHook func(kind MsgKind, from, to int) bool

	Sent    map[MsgKind]int // 已发送（含被丢弃）的消息数
	Dropped map[MsgKind]int // 被丢弃的消息数
}

func NewNetwork(sim *Sim, minLat, maxLat int64, seed int64) *Network {
	return &Network{
		sim: sim, MinLat: minLat, MaxLat: maxLat,
		rng:     rand.New(rand.NewSource(seed)),
		Sent:    map[MsgKind]int{},
		Dropped: map[MsgKind]int{},
	}
}

// Send 发送一条消息；返回 false 表示被丢弃（不会投递）。
func (n *Network) Send(kind MsgKind, from, to int, deliver func()) bool {
	n.Sent[kind]++
	if n.DropHook != nil && n.DropHook(kind, from, to) {
		n.Dropped[kind]++
		return false
	}
	lat := n.MinLat
	if n.MaxLat > n.MinLat {
		lat += n.rng.Int63n(n.MaxLat - n.MinLat + 1)
	}
	n.sim.After(lat, deliver)
	return true
}
