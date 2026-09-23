package main

// Coordinator 是客户端侧协调者：负责写路径与读路径。
// 读路径在收集应答时即时检测落后副本，并把修复“承诺”交给
// RepairManager；承诺之后读操作本身是否被取消与修复无关。
type Coordinator struct {
	sim         *Sim
	net         *Network
	replicas    []*Replica
	repairs     *RepairManager
	readTimeout int64

	ver int64 // 单调递增的写版本号（Lamport 式逻辑时钟）
}

func NewCoordinator(sim *Sim, net *Network, replicas []*Replica, repairs *RepairManager, readTimeout int64) *Coordinator {
	return &Coordinator{sim: sim, net: net, replicas: replicas, repairs: repairs, readTimeout: readTimeout}
}

// Write 以新版本把值写到指定副本子集（写子集即制造落后副本的方式）。
func (c *Coordinator) Write(key, val string, to []int) VersionedValue {
	c.ver++
	v := VersionedValue{Val: val, Ver: c.ver}
	for _, idx := range to {
		rep := c.replicas[idx]
		c.net.Send(MsgWriteReq, CoordinatorID, idx, func() {
			rep.Apply(key, v)
		})
	}
	return v
}

// ReadHandle 是读操作的取消句柄。
type ReadHandle struct {
	cancelled bool
}

// Cancel 取消读：不再处理后续应答（也不再产生新的修复承诺），
// 但已经承诺给 RepairManager 的修复不受影响。
func (h *ReadHandle) Cancel() { h.cancelled = true }

// Read 向全部副本读 key。每收到一个应答就增量地：
//   - 更新目前已知的最新版本 best；
//   - 对已知落后的副本（包括之前应答过、现在相形见绌的）承诺修复。
//
// 全部应答到达或超时后，回调 onDone(best, found)。
func (c *Coordinator) Read(key string, onDone func(best VersionedValue, found bool)) *ReadHandle {
	h := &ReadHandle{}
	best := VersionedValue{}
	seen := map[int]VersionedValue{}
	responded := 0
	finished := false

	finish := func() {
		if finished {
			return
		}
		finished = true
		if onDone != nil {
			onDone(best, best.Ver > 0)
		}
	}
	timer := c.sim.After(c.readTimeout, finish)

	for i := range c.replicas {
		idx := i
		rep := c.replicas[idx]
		c.net.Send(MsgReadReq, CoordinatorID, idx, func() {
			v, ok := rep.Read(key)
			if !ok {
				v = VersionedValue{} // 缺失视为版本 0
			}
			c.net.Send(MsgReadResp, idx, CoordinatorID, func() {
				if h.cancelled || finished {
					return // 已取消/已完成：不再产生新承诺
				}
				responded++
				if v.Ver > best.Ver {
					// 之前应答过的副本相对新 best 已落后：补承诺。
					for j, sv := range seen {
						if sv.Ver < v.Ver {
							c.repairs.Schedule(key, j, v)
						}
					}
					best = v
				} else if v.Ver < best.Ver {
					c.repairs.Schedule(key, idx, best)
				}
				seen[idx] = v
				if responded == len(c.replicas) {
					timer.Cancel()
					finish()
				}
			})
		})
	}
	return h
}
