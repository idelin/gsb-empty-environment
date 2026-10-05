package sim

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
)

// Op 是一条可序列化的模拟指令；场景 = 指令脚本，可记录、可重放。
type Op struct {
	Do     string `json:"do"` // write/del/migrate/pump/deliver/freeze/decide/crash/recover/conflict
	Key    string `json:"key,omitempty"`
	Val    string `json:"val,omitempty"`
	Idem   string `json:"idem,omitempty"`
	CfgVer int    `json:"cfgVer,omitempty"` // 0 = 用当前版本
	Slot   int    `json:"slot,omitempty"`
	Target string `json:"target,omitempty"`
	Count  int    `json:"count,omitempty"`
	Mode   string `json:"mode,omitempty"` // deliver: fifo/lifo/dup
}

// Runner 执行指令脚本并产出证据。
type Runner struct {
	C   *Cluster
	Res []WriteResult
	Err []string
}

// NewRunner 用标准测试拓扑建集群：4 节点 2 故障域。
func NewRunner(M, B int) (*Runner, error) {
	c, err := NewCluster(M, B, []Node{
		{ID: "n1", Domain: "d1"},
		{ID: "n2", Domain: "d1"},
		{ID: "n3", Domain: "d2"},
		{ID: "n4", Domain: "d2"},
	})
	if err != nil {
		return nil, err
	}
	return &Runner{C: c}, nil
}

// Run 逐条执行脚本。
func (r *Runner) Run(ops []Op) {
	for i, op := range ops {
		if err := r.step(op); err != nil {
			r.Err = append(r.Err, fmt.Sprintf("op#%d %+v: %v", i, op, err))
			r.C.emit(Event{Kind: "note", Detail: fmt.Sprintf("op#%d 报错: %v", i, err)})
		}
	}
}

func (r *Runner) step(op Op) error {
	c := r.C
	switch op.Do {
	case "write":
		r.Res = append(r.Res, c.Handle(Request{Op: OpSet, Key: op.Key, Val: op.Val, Idem: op.Idem, CfgVer: op.CfgVer}))
	case "del":
		r.Res = append(r.Res, c.Handle(Request{Op: OpDel, Key: op.Key, Idem: op.Idem, CfgVer: op.CfgVer}))
	case "migrate":
		return c.StartMigration(op.Slot, op.Target)
	case "pump":
		c.Pump(op.Slot, op.Count)
	case "deliver":
		c.Deliver(op.Count, op.Mode)
	case "freeze":
		return c.Freeze(op.Slot)
	case "decide":
		return c.Decide(op.Slot)
	case "crash":
		c.CrashAll()
	case "recover":
		c.Recover()
	case "conflict":
		if !c.InjectConflict() {
			return fmt.Errorf("无在途消息可注入冲突")
		}
	default:
		return fmt.Errorf("未知指令 %q", op.Do)
	}
	return nil
}

// Check 运行独立不变量检查器。
func (r *Runner) Check() []string {
	return Check(r.C.Ev, r.C.Dump(), r.C.M)
}

// Digest 证据摘要，用于重放比对。
func (r *Runner) Digest() string {
	b, _ := json.Marshal(r.C.Ev)
	h := fnv.New64a()
	h.Write(b)
	return fmt.Sprintf("%016x", h.Sum64())
}

// --- 场景脚本 ---

// pickSlot 选一个初始主属于 domain d1 节点的槽，便于跨域迁移。
func pickSlot(c *Cluster, wantSrcDomain string) int {
	for s := 0; s < c.M; s++ {
		if c.Nodes[c.Cfg.Primary[s]].Domain == wantSrcDomain {
			return s
		}
	}
	return 0
}

// targetFor 给槽选不同故障域的目标。
func targetFor(c *Cluster, slot int) string {
	srcDom := c.Nodes[c.Cfg.Primary[slot]].Domain
	for _, id := range c.Order {
		if c.Nodes[id].Domain != srcDom {
			return id
		}
	}
	return ""
}

// keyOn 生成落在指定槽上的键。
func keyOn(c *Cluster, slot int, tag string) string {
	for i := 0; ; i++ {
		k := fmt.Sprintf("%s-%d", tag, i)
		if SlotOf(k, c.M) == slot {
			return k
		}
	}
}

// ScenarioFull 综合场景：基线期间同键修改、乱序补缺、删除重建、重复请求跨切换、
// 目标崩溃、每持久化边界重启、背压，全部交错在一次迁移里。
func ScenarioFull(M, B int) ([]Op, error) {
	r, err := NewRunner(M, B)
	if err != nil {
		return nil, err
	}
	c := r.C
	slot := pickSlot(c, "d1")
	tgt := targetFor(c, slot)
	k1 := keyOn(c, slot, "alpha")
	k2 := keyOn(c, slot, "beta")
	ver := 1
	ops := []Op{
		{Do: "write", Key: k1, Val: "v1", Idem: "w1", CfgVer: ver},
		{Do: "write", Key: k2, Val: "x1", Idem: "w2", CfgVer: ver},
		{Do: "del", Key: k2, Idem: "w3", CfgVer: ver},              // 删除也是变更
		{Do: "write", Key: k2, Val: "x2", Idem: "w4", CfgVer: ver}, // 删除后重建
		{Do: "migrate", Slot: slot, Target: tgt},
		{Do: "pump", Slot: slot, Count: 2}, // 基线只复制一部分
		{Do: "deliver", Count: 2, Mode: "fifo"},
		{Do: "write", Key: k1, Val: "v2", Idem: "w5", CfgVer: ver}, // 基线复制期间修改同键
		{Do: "crash"}, {Do: "recover"}, // 持久化边界重启
		{Do: "pump", Slot: slot, Count: 8},
		{Do: "deliver", Count: 1, Mode: "lifo"}, // 乱序：后发先到，目标暂存
		{Do: "deliver", Count: 1, Mode: "dup"},  // 重复投递
		{Do: "deliver", Count: 8, Mode: "fifo"}, // 补缺，连续前缀推进
		{Do: "freeze", Slot: slot},
		{Do: "write", Key: k1, Val: "v3", Idem: "w6", CfgVer: ver}, // 冻结期间新写 -> Retry，绝不确认
		{Do: "crash"}, {Do: "recover"}, // 目标崩溃：乱序暂存丢失，从已确认进度继续
		{Do: "pump", Slot: slot, Count: 8},
		{Do: "deliver", Count: 8, Mode: "fifo"},
		{Do: "decide", Slot: slot},
		{Do: "write", Key: k1, Val: "v9", Idem: "w5", CfgVer: ver}, // 旧配置写旧主 -> fence 拒绝
		{Do: "write", Key: k1, Val: "v9", Idem: "w5", CfgVer: 0},   // 重复请求跨切换 -> 幂等命中原结果
		{Do: "write", Key: k1, Val: "v4", Idem: "w7", CfgVer: 0},   // 新主以新 epoch 接受写
		{Do: "crash"}, {Do: "recover"}, // 旧主恢复：fence 仍在，不得回退
		{Do: "write", Key: k2, Val: "x3", Idem: "w8", CfgVer: ver}, // 旧配置仍不得写旧主
	}
	return ops, nil
}

// ScenarioCapacity 容量耗尽：B 很小，制造背压，验证不丢记录。
func ScenarioCapacity(M, B int) ([]Op, error) {
	r, err := NewRunner(M, B)
	if err != nil {
		return nil, err
	}
	c := r.C
	slot := pickSlot(c, "d1")
	tgt := targetFor(c, slot)
	ops := []Op{}
	for i := 0; i < 6; i++ {
		ops = append(ops, Op{Do: "write", Key: keyOn(c, slot, fmt.Sprintf("cap%d", i)), Val: "payload-payload", Idem: fmt.Sprintf("c%d", i), CfgVer: 1})
	}
	ops = append(ops,
		Op{Do: "migrate", Slot: slot, Target: tgt},
		Op{Do: "pump", Slot: slot, Count: 100}, // 缓存迅速打满 -> 背压
		Op{Do: "deliver", Count: 1, Mode: "fifo"},
		Op{Do: "pump", Slot: slot, Count: 100},
		Op{Do: "deliver", Count: 2, Mode: "fifo"},
		Op{Do: "pump", Slot: slot, Count: 100},
		Op{Do: "deliver", Count: 100, Mode: "fifo"},
		Op{Do: "freeze", Slot: slot},
		Op{Do: "pump", Slot: slot, Count: 100},
		Op{Do: "deliver", Count: 100, Mode: "fifo"},
		Op{Do: "decide", Slot: slot},
	)
	return ops, nil
}

// ScenarioRestartEveryBoundary 在每个持久化边界（每条指令之间）都崩溃恢复。
func ScenarioRestartEveryBoundary(M, B int) ([]Op, error) {
	base, err := ScenarioFull(M, B)
	if err != nil {
		return nil, err
	}
	var ops []Op
	for _, op := range base {
		ops = append(ops, op, Op{Do: "crash"}, Op{Do: "recover"})
	}
	return ops, nil
}

// ScenarioConflict 同序号不同内容：注入伪造增量，目标必须报错。
func ScenarioConflict(M, B int) ([]Op, error) {
	r, err := NewRunner(M, B)
	if err != nil {
		return nil, err
	}
	c := r.C
	slot := pickSlot(c, "d1")
	tgt := targetFor(c, slot)
	return []Op{
		{Do: "write", Key: keyOn(c, slot, "cf"), Val: "a", Idem: "k1", CfgVer: 1},
		{Do: "write", Key: keyOn(c, slot, "cg"), Val: "b", Idem: "k2", CfgVer: 1},
		{Do: "migrate", Slot: slot, Target: tgt},
		{Do: "pump", Slot: slot, Count: 10},
		{Do: "conflict"}, // 伪造一条与在途首条同序号、不同内容的消息
		{Do: "deliver", Count: 10, Mode: "fifo"},
	}, nil
}

// ScenarioMultiSlot 多个槽独立推进迁移，共享全局缓存。
func ScenarioMultiSlot(M, B int) ([]Op, error) {
	r, err := NewRunner(M, B)
	if err != nil {
		return nil, err
	}
	c := r.C
	s1 := pickSlot(c, "d1")
	s2 := -1
	for s := 0; s < c.M; s++ {
		if s != s1 && c.Nodes[c.Cfg.Primary[s]].Domain == "d1" {
			s2 = s
			break
		}
	}
	if s2 < 0 {
		s2 = (s1 + 1) % c.M
	}
	t1, t2 := targetFor(c, s1), targetFor(c, s2)
	return []Op{
		{Do: "write", Key: keyOn(c, s1, "m1"), Val: "1", Idem: "mm1", CfgVer: 1},
		{Do: "write", Key: keyOn(c, s2, "m2"), Val: "2", Idem: "mm2", CfgVer: 1},
		{Do: "migrate", Slot: s1, Target: t1},
		{Do: "migrate", Slot: s2, Target: t2},
		{Do: "pump", Slot: s1, Count: 5},
		{Do: "pump", Slot: s2, Count: 5},
		{Do: "deliver", Count: 1, Mode: "lifo"},
		{Do: "deliver", Count: 10, Mode: "fifo"},
		{Do: "freeze", Slot: s1},
		{Do: "freeze", Slot: s2},
		{Do: "pump", Slot: s1, Count: 5},
		{Do: "pump", Slot: s2, Count: 5},
		{Do: "deliver", Count: 10, Mode: "fifo"},
		{Do: "decide", Slot: s1},
		{Do: "decide", Slot: s2},
		{Do: "write", Key: keyOn(c, s1, "m3"), Val: "3", Idem: "mm3", CfgVer: 0},
	}, nil
}

// Scenarios 场景注册表。
func Scenarios() map[string]func(M, B int) ([]Op, error) {
	return map[string]func(M, B int) ([]Op, error){
		"full":             ScenarioFull,
		"capacity":         ScenarioCapacity,
		"restart-boundary": ScenarioRestartEveryBoundary,
		"conflict":         ScenarioConflict,
		"multi-slot":       ScenarioMultiSlot,
	}
}

// SortedKeys 便于 CLI 列出场景。
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
