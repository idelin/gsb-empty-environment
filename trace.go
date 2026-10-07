// Package trace 追溯实验配液过程中各容器的溶质质量与来源批次份额。
//
// 模型假设：容器内溶液始终均匀（混匀为即时操作），转移按源容器
// 当前浓度比例取各溶质质量。所有质量与体积均以 math/big.Rat 精确
// 表示，初始溶质质量为有理数，转移/加水体积为整数。
//
// 批次语义：一个转移批次内的所有转移按同一旧状态计算取量；
// 若任一源容器的总取量超过其现体积，整批拒绝且状态不变。
package trace

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Transfer 描述一次整数体积的容器间转移。
type Transfer struct {
	From   string
	To     string
	Volume int64
}

// BatchRecord 记录一个已提交转移批次的内容，ID 从 1 开始（0 为初始装载）。
type BatchRecord struct {
	ID        int
	Transfers []Transfer
}

// SoluteState 是某容器内一种溶质的状态：总质量及按来源批次划分的份额。
type SoluteState struct {
	Mass   *big.Rat
	Shares map[int]*big.Rat // 批次 ID -> 该批次贡献的质量
}

// Container 是单个容器的状态。
type Container struct {
	Volume  *big.Rat
	Solutes map[string]*SoluteState
}

// Lab 管理全部容器、初始装载记录与批次日志。
type Lab struct {
	containers map[string]*Container
	initial    map[string]map[string]*big.Rat // 容器 -> 溶质 -> 初始质量（独立守恒基准）
	batches    []BatchRecord
}

// New 创建空的追溯实验台。
func New() *Lab {
	return &Lab{
		containers: make(map[string]*Container),
		initial:    make(map[string]map[string]*big.Rat),
	}
}

// AddContainer 注册一个空容器。
func (l *Lab) AddContainer(name string) error {
	if _, ok := l.containers[name]; ok {
		return fmt.Errorf("容器 %q 已存在", name)
	}
	l.containers[name] = &Container{Volume: new(big.Rat), Solutes: make(map[string]*SoluteState)}
	l.initial[name] = make(map[string]*big.Rat)
	return nil
}

// Load 向空容器注入初始溶液：整数体积与各有理数质量的溶质。
// 初始装载记为批次 0 的来源份额。容器必须为空（零体积且无溶质）。
func (l *Lab) Load(name string, volume int64, masses map[string]string) error {
	c, ok := l.containers[name]
	if !ok {
		return fmt.Errorf("容器 %q 不存在", name)
	}
	if c.Volume.Sign() != 0 || len(c.Solutes) != 0 {
		return fmt.Errorf("容器 %q 非空，不能初始装载", name)
	}
	if volume < 0 {
		return fmt.Errorf("体积不能为负: %d", volume)
	}
	c.Volume = new(big.Rat).SetInt64(volume)
	for solute, text := range masses {
		m, ok := new(big.Rat).SetString(text)
		if !ok {
			return fmt.Errorf("溶质 %q 质量 %q 不是有理数", solute, text)
		}
		if m.Sign() < 0 {
			return fmt.Errorf("溶质 %q 质量不能为负", solute)
		}
		c.Solutes[solute] = &SoluteState{
			Mass:   new(big.Rat).Set(m),
			Shares: map[int]*big.Rat{0: new(big.Rat).Set(m)},
		}
		l.initial[name][solute] = new(big.Rat).Set(m)
	}
	return nil
}

// AddWater 向容器加入整数体积的水，只改变体积（稀释浓度），不改变溶质质量。
func (l *Lab) AddWater(name string, volume int64) error {
	c, ok := l.containers[name]
	if !ok {
		return fmt.Errorf("容器 %q 不存在", name)
	}
	if volume < 0 {
		return fmt.Errorf("体积不能为负: %d", volume)
	}
	c.Volume.Add(c.Volume, new(big.Rat).SetInt64(volume))
	return nil
}

// Mix 混匀容器。模型假设溶液始终均匀，故仅校验容器存在，无状态变化。
func (l *Lab) Mix(name string) error {
	if _, ok := l.containers[name]; !ok {
		return fmt.Errorf("容器 %q 不存在", name)
	}
	return nil
}

// TransferBatch 原子地执行一批转移。全部转移按提交前的同一旧状态
// 计算各源容器取量比例；若任一源容器总取量超过其现体积，整批拒绝，
// 所有容器状态保持不变。成功时返回新批次 ID。
// 体积为 0 的转移是合法空操作；From == To 的转移按空操作跳过。
func (l *Lab) TransferBatch(ts []Transfer) (int, error) {
	if len(ts) == 0 {
		return 0, errors.New("空批次")
	}
	take := make(map[string]int64)
	for _, t := range ts {
		if t.Volume < 0 {
			return 0, fmt.Errorf("转移体积不能为负: %d", t.Volume)
		}
		if _, ok := l.containers[t.From]; !ok {
			return 0, fmt.Errorf("源容器 %q 不存在", t.From)
		}
		if _, ok := l.containers[t.To]; !ok {
			return 0, fmt.Errorf("目的容器 %q 不存在", t.To)
		}
		if t.From == t.To {
			continue
		}
		take[t.From] += t.Volume
	}
	// 整批校验：按旧状态汇总各源取量，超量则整批拒绝。
	for name, total := range take {
		c := l.containers[name]
		if new(big.Rat).SetInt64(total).Cmp(c.Volume) > 0 {
			return 0, fmt.Errorf("批次拒绝：容器 %q 总取量 %d 超过现体积 %s",
				name, total, c.Volume.RatString())
		}
	}
	id := len(l.batches) + 1
	for _, t := range ts {
		if t.From == t.To || t.Volume == 0 {
			continue
		}
		l.applyTransfer(id, t)
	}
	l.batches = append(l.batches, BatchRecord{ID: id, Transfers: append([]Transfer(nil), ts...)})
	return id, nil
}

// applyTransfer 按源容器旧浓度比例移动体积与各溶质质量。
// 调用前已保证 src.Volume >= t.Volume > 0。
func (l *Lab) applyTransfer(batchID int, t Transfer) {
	src := l.containers[t.From]
	dst := l.containers[t.To]
	frac := new(big.Rat).SetInt64(t.Volume)
	frac.Quo(frac, src.Volume)

	vol := new(big.Rat).SetInt64(t.Volume)
	src.Volume.Sub(src.Volume, vol)
	dst.Volume.Add(dst.Volume, vol)

	names := make([]string, 0, len(src.Solutes))
	for name := range src.Solutes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s := src.Solutes[name]
		moved := new(big.Rat).Mul(s.Mass, frac)
		s.Mass.Sub(s.Mass, moved)
		// 源侧各旧批次份额按比例缩减。
		for b, sh := range s.Shares {
			part := new(big.Rat).Mul(sh, frac)
			sh.Sub(sh, part)
			if sh.Sign() == 0 {
				delete(s.Shares, b)
			}
		}
		// 目的侧新增质量整体记到本转移批次名下。
		d := dst.Solutes[name]
		if d == nil {
			d = &SoluteState{Mass: new(big.Rat), Shares: make(map[int]*big.Rat)}
			dst.Solutes[name] = d
		}
		d.Mass.Add(d.Mass, moved)
		if d.Shares[batchID] == nil {
			d.Shares[batchID] = new(big.Rat)
		}
		d.Shares[batchID].Add(d.Shares[batchID], moved)
	}
}

// SoluteSnapshot 是某溶质状态的只读快照。
type SoluteSnapshot struct {
	Mass   *big.Rat
	Shares map[int]*big.Rat
}

// Snapshot 是某容器状态的只读快照。
type Snapshot struct {
	Volume  *big.Rat
	Solutes map[string]SoluteSnapshot
}

// Snapshot 返回容器的深拷贝快照；容器不存在时 ok 为 false。
func (l *Lab) Snapshot(name string) (snap Snapshot, ok bool) {
	c, ok := l.containers[name]
	if !ok {
		return Snapshot{}, false
	}
	snap.Volume = new(big.Rat).Set(c.Volume)
	snap.Solutes = make(map[string]SoluteSnapshot, len(c.Solutes))
	for s, st := range c.Solutes {
		shares := make(map[int]*big.Rat, len(st.Shares))
		for b, v := range st.Shares {
			shares[b] = new(big.Rat).Set(v)
		}
		snap.Solutes[s] = SoluteSnapshot{Mass: new(big.Rat).Set(st.Mass), Shares: shares}
	}
	return snap, true
}

// Batches 返回已提交转移批次日志的拷贝。
func (l *Lab) Batches() []BatchRecord {
	out := make([]BatchRecord, len(l.batches))
	for i, b := range l.batches {
		out[i] = BatchRecord{ID: b.ID, Transfers: append([]Transfer(nil), b.Transfers...)}
	}
	return out
}

// String 以可读形式输出每容器体积、各溶质质量及来源批次份额。
func (l *Lab) String() string {
	names := make([]string, 0, len(l.containers))
	for name := range l.containers {
		names = append(names, name)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, name := range names {
		c := l.containers[name]
		fmt.Fprintf(&sb, "容器 %s: 体积=%s\n", name, c.Volume.RatString())
		solutes := make([]string, 0, len(c.Solutes))
		for s := range c.Solutes {
			solutes = append(solutes, s)
		}
		sort.Strings(solutes)
		for _, s := range solutes {
			st := c.Solutes[s]
			batches := make([]int, 0, len(st.Shares))
			for b := range st.Shares {
				batches = append(batches, b)
			}
			sort.Ints(batches)
			parts := make([]string, 0, len(batches))
			for _, b := range batches {
				parts = append(parts, fmt.Sprintf("批次%d=%s", b, st.Shares[b].RatString()))
			}
			fmt.Fprintf(&sb, "  溶质 %s: 质量=%s 份额{%s}\n", s, st.Mass.RatString(), strings.Join(parts, ", "))
		}
	}
	return sb.String()
}

// Verify 独立核验守恒与一致性：
//  1. 每容器每溶质的来源批次份额之和等于其质量；
//  2. 全场每种溶质总质量等于初始装载记录的总质量（独立基准）；
//  3. 体积、质量、份额均非负。
func (l *Lab) Verify() error {
	totals := make(map[string]*big.Rat)
	for cname, c := range l.containers {
		if c.Volume.Sign() < 0 {
			return fmt.Errorf("容器 %q 体积为负", cname)
		}
		for s, st := range c.Solutes {
			if st.Mass.Sign() < 0 {
				return fmt.Errorf("容器 %q 溶质 %q 质量为负", cname, s)
			}
			sum := new(big.Rat)
			for b, sh := range st.Shares {
				if sh.Sign() < 0 {
					return fmt.Errorf("容器 %q 溶质 %q 批次 %d 份额为负", cname, s, b)
				}
				sum.Add(sum, sh)
			}
			if sum.Cmp(st.Mass) != 0 {
				return fmt.Errorf("容器 %q 溶质 %q 份额和 %s 不等于质量 %s",
					cname, s, sum.RatString(), st.Mass.RatString())
			}
			if totals[s] == nil {
				totals[s] = new(big.Rat)
			}
			totals[s].Add(totals[s], st.Mass)
		}
	}
	initialTotals := make(map[string]*big.Rat)
	for _, masses := range l.initial {
		for s, m := range masses {
			if initialTotals[s] == nil {
				initialTotals[s] = new(big.Rat)
			}
			initialTotals[s].Add(initialTotals[s], m)
		}
	}
	for s, total := range totals {
		init := initialTotals[s]
		if init == nil || total.Cmp(init) != 0 {
			want := "0"
			if init != nil {
				want = init.RatString()
			}
			return fmt.Errorf("溶质 %q 不守恒：现存 %s，初始 %s", s, total.RatString(), want)
		}
	}
	for s, init := range initialTotals {
		if totals[s] == nil && init.Sign() != 0 {
			return fmt.Errorf("溶质 %q 不守恒：现存 0，初始 %s", s, init.RatString())
		}
	}
	return nil
}
