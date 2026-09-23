package main

// RepairOutcome 记录一个修复目标的最终结果（证据日志）。
type RepairOutcome struct {
	Key      string
	Replica  int
	Ver      int64
	Attempts int
	Success  bool // false 表示重试耗尽、有界放弃
}

// repairTarget 是 (key, replica) 维度上被合并后的修复目标。
type repairTarget struct {
	promised VersionedValue // 已承诺要安装的最新版本（合并点）
	attempts int            // 已发送的 RPC 次数（有界重试计数）
	active   bool           // 当前是否有 RPC 在途
	sentVer  int64          // 在途 RPC 携带的版本
}

type repairRef struct {
	key     string
	replica int
}

// RepairManager 负责把“已承诺的修复”可靠地送达副本：
//   - 按键合并：同一 (key, replica) 的承诺合并为一条，只保留最新版本；
//   - 全局限并发：在途修复 RPC 总数不超过 maxInflight；
//   - 有界重试：单个目标最多 maxAttempts 次尝试，超时/丢包后重排。
//
// 注意：取消读操作不会触碰这里的任何状态——承诺一旦进入管理器，
// 就不再属于某个读操作，因此“取消读不丢已承诺修复”。
type RepairManager struct {
	sim         *Sim
	net         *Network
	replicas    []*Replica
	maxInflight int
	maxAttempts int
	timeout     int64

	inflight int
	pending  map[string]map[int]*repairTarget // key -> replica -> 目标
	ready    []repairRef                      // 等待并发配额的目标（FIFO）

	Outcomes            []RepairOutcome
	MaxInflightObserved int // 运行期观测到的最大在途数（限并发证据）
}

func NewRepairManager(sim *Sim, net *Network, replicas []*Replica, maxInflight, maxAttempts int, timeout int64) *RepairManager {
	return &RepairManager{
		sim: sim, net: net, replicas: replicas,
		maxInflight: maxInflight, maxAttempts: maxAttempts, timeout: timeout,
		pending: map[string]map[int]*repairTarget{},
	}
}

// Schedule 承诺一次修复。对同一 (key, replica) 的重复承诺被合并：
// 只保留版本最大的值；同版本或更旧的承诺直接丢弃。
func (m *RepairManager) Schedule(key string, replica int, v VersionedValue) {
	targets, ok := m.pending[key]
	if !ok {
		targets = map[int]*repairTarget{}
		m.pending[key] = targets
	}
	t, ok := targets[replica]
	if !ok {
		targets[replica] = &repairTarget{promised: v}
		m.ready = append(m.ready, repairRef{key, replica})
	} else if v.Ver > t.promised.Ver {
		t.promised = v // 合并：在途/排队的旧版本被更新版本取代
	}
	m.pump()
}

// pump 在全局并发配额内把排队目标发出去。
func (m *RepairManager) pump() {
	for m.inflight < m.maxInflight && len(m.ready) > 0 {
		ref := m.ready[0]
		m.ready = m.ready[1:]
		t := m.pending[ref.key][ref.replica]
		if t == nil || t.active {
			continue
		}
		m.send(ref.key, ref.replica, t)
	}
}

func (m *RepairManager) send(key string, replica int, t *repairTarget) {
	t.active = true
	t.sentVer = t.promised.Ver
	t.attempts++
	gen := t.attempts // 本次尝试的世代号，用于忽略迟到的旧应答/旧超时
	m.inflight++
	if m.inflight > m.MaxInflightObserved {
		m.MaxInflightObserved = m.inflight
	}
	v := t.promised
	timer := m.sim.After(m.timeout, func() {
		if t.active && t.attempts == gen {
			m.onTimeout(key, replica, t, gen)
		}
	})
	m.net.Send(MsgRepairReq, CoordinatorID, replica, func() {
		m.replicas[replica].Apply(key, v) // 副本侧的 LWW 守卫决定是否落盘
		m.net.Send(MsgRepairResp, replica, CoordinatorID, func() {
			timer.Cancel()
			m.onAck(key, replica, t, gen)
		})
	})
}

func (m *RepairManager) onAck(key string, replica int, t *repairTarget, gen int) {
	if !t.active || t.attempts != gen {
		return // 迟到的旧应答，忽略
	}
	t.active = false
	m.inflight--
	if t.promised.Ver > t.sentVer {
		// 在途期间承诺了更新版本：排队再送一次（合并而非叠加）。
		m.ready = append(m.ready, repairRef{key, replica})
	} else {
		m.Outcomes = append(m.Outcomes, RepairOutcome{
			Key: key, Replica: replica, Ver: t.sentVer,
			Attempts: t.attempts, Success: true,
		})
		m.remove(key, replica)
	}
	m.pump()
}

func (m *RepairManager) onTimeout(key string, replica int, t *repairTarget, gen int) {
	t.active = false
	m.inflight--
	if t.attempts >= m.maxAttempts {
		// 重试有界：放弃并记录失败，绝不无限重试。
		m.Outcomes = append(m.Outcomes, RepairOutcome{
			Key: key, Replica: replica, Ver: t.promised.Ver,
			Attempts: t.attempts, Success: false,
		})
		m.remove(key, replica)
	} else {
		m.ready = append(m.ready, repairRef{key, replica})
	}
	m.pump()
}

func (m *RepairManager) remove(key string, replica int) {
	delete(m.pending[key], replica)
	if len(m.pending[key]) == 0 {
		delete(m.pending, key)
	}
}

// Inflight 返回当前在途修复 RPC 数。
func (m *RepairManager) Inflight() int { return m.inflight }
