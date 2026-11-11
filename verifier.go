package netsec

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	// ErrUnknownRecord 在撤回不存在的记录 ID 时返回。
	ErrUnknownRecord = errors.New("netsec: 未知记录 ID")
	// ErrNoReport 在尚无核验报告时请求对照更新。
	ErrNoReport = errors.New("netsec: 尚未生成核验报告，无法对照更新")
)

// Verifier 维护带版本的记录集，支持全量重分组、核验与撤回对照。
// 所有方法对并发调用安全。
type Verifier struct {
	cfg      Config
	mu       sync.RWMutex
	records  map[string]Record
	last     *Report
	revision uint64
}

// New 校验配置并返回一个空记录集的核验器。
func New(cfg Config) (*Verifier, error) {
	if len(cfg.QuasiIDs) == 0 {
		return nil, errors.New("netsec: 至少需要一个准标识属性")
	}
	if len(cfg.QuasiIDs) != len(cfg.Trees) || len(cfg.QuasiIDs) != len(cfg.Levels) {
		return nil, errors.New("netsec: QuasiIDs、Trees、Levels 长度必须一致")
	}
	seen := make(map[string]struct{}, len(cfg.QuasiIDs))
	for i, name := range cfg.QuasiIDs {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("netsec: 第 %d 个准标识名称为空", i)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("netsec: 准标识 %q 重复", name)
		}
		seen[name] = struct{}{}
		if cfg.Trees[i] == nil {
			return nil, fmt.Errorf("netsec: 准标识 %q 的泛化树为 nil", name)
		}
		if cfg.Trees[i].Name() != name {
			return nil, fmt.Errorf("netsec: 准标识 %q 与树名 %q 不一致", name, cfg.Trees[i].Name())
		}
		if cfg.Levels[i] < 0 {
			return nil, fmt.Errorf("netsec: 准标识 %q 的泛化步数不能为负", name)
		}
		if cfg.Levels[i] > cfg.Trees[i].Height() {
			return nil, fmt.Errorf("netsec: 准标识 %q 泛化步数 %d 超过树高 %d", name, cfg.Levels[i], cfg.Trees[i].Height())
		}
	}
	if cfg.L < 1 {
		return nil, fmt.Errorf("netsec: L 必须 >= 1，收到 %d", cfg.L)
	}
	if cfg.MinGroupSize < 0 {
		return nil, errors.New("netsec: MinGroupSize 不能为负数")
	}
	if cfg.MaxRatio != nil && cfg.MaxRatio.Sign() < 0 {
		return nil, errors.New("netsec: MaxRatio 不能为负数")
	}
	if cfg.RatioDenominator != RatioOverAll && cfg.RatioDenominator != RatioOverKnown {
		return nil, errors.New("netsec: 非法的 RatioDenominator 取值")
	}
	if strings.ContainsRune(cfg.UnknownSentinel, 0) {
		return nil, errors.New("netsec: 未知值哨兵不能包含 NUL 字符")
	}

	return &Verifier{
		cfg:     cfg,
		records: make(map[string]Record),
	}, nil
}

// Verify 是一次性便捷入口：按 cfg 核验 records，不保留可撤回状态。
func Verify(cfg Config, records []Record) (*Report, error) {
	v, err := New(cfg)
	if err != nil {
		return nil, err
	}
	if err := v.Replace(records); err != nil {
		return nil, err
	}
	return v.RegroupAndVerify()
}

// Replace 以原子方式替换全部记录，任何一条非法则整体拒绝。
func (v *Verifier) Replace(records []Record) error {
	indexed := make(map[string]Record, len(records))
	for i, rec := range records {
		if err := v.validateRecord(rec); err != nil {
			return fmt.Errorf("第 %d 条记录（ID=%q）非法：%w", i, rec.ID, err)
		}
		if _, dup := indexed[rec.ID]; dup {
			return fmt.Errorf("netsec: 记录 ID %q 重复", rec.ID)
		}
		indexed[rec.ID] = rec
	}
	v.mu.Lock()
	v.records = indexed
	v.last = nil
	v.revision++
	v.mu.Unlock()
	return nil
}

func (v *Verifier) validateRecord(rec Record) error {
	if rec.ID == "" {
		return errors.New("记录 ID 不能为空")
	}
	if strings.ContainsRune(rec.ID, 0) {
		return errors.New("记录 ID 不能包含 NUL 字符")
	}
	if len(rec.QuasiValues) != len(v.cfg.QuasiIDs) {
		return fmt.Errorf("准标识值数量为 %d，期望 %d", len(rec.QuasiValues), len(v.cfg.QuasiIDs))
	}
	for i, value := range rec.QuasiValues {
		tree := v.cfg.Trees[i]
		if strings.ContainsRune(value, 0) {
			return fmt.Errorf("准标识 %q 的值包含 NUL 字符", v.cfg.QuasiIDs[i])
		}
		if _, err := tree.Generalize(value, v.cfg.Levels[i]); err != nil {
			return err
		}
	}
	if strings.ContainsRune(rec.Sensitive, 0) {
		return errors.New("敏感值不能包含 NUL 字符")
	}
	return nil
}

// Revision 返回记录集版本号，每次 Replace/Withdraw 成功后递增。
func (v *Verifier) Revision() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.revision
}

// RecordCount 返回当前持有的记录数。
func (v *Verifier) RecordCount() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.records)
}

// Records 返回按 ID 排序的当前记录快照。
func (v *Verifier) Records() []Record {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]Record, 0, len(v.records))
	for _, rec := range v.records {
		out = append(out, rec)
	}
	slices.SortFunc(out, func(a, b Record) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// LastReport 返回最近一次核验报告；尚未核验时为 nil。
// 返回值属于核验器内部状态的直接引用，调用方不得修改。
func (v *Verifier) LastReport() *Report {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.last
}

// RegroupAndVerify 丢弃增量补丁，从当前全量记录重新泛化分组并核验。
func (v *Verifier) RegroupAndVerify() (*Report, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	report, err := v.analyzeLocked()
	if err != nil {
		return nil, err
	}
	v.last = report
	return report, nil
}

type rawGroup struct {
	key     GroupKey
	records []Record
}

func (v *Verifier) analyzeLocked() (*Report, error) {
	groups := make(map[string]*rawGroup)
	var order []string
	for _, rec := range v.records {
		key, err := v.groupKeyLocked(rec)
		if err != nil {
			return nil, err
		}
		g, ok := groups[key.joined]
		if !ok {
			g = &rawGroup{key: key}
			groups[key.joined] = g
			order = append(order, key.joined)
		}
		g.records = append(g.records, rec)
	}
	slices.Sort(order)

	report := &Report{
		GeneratedAt:  time.Now().UTC(),
		TotalRecords: len(v.records),
		Groups:       make([]GroupInfo, 0, len(order)),
		Violations:   make([]Violation, 0),
	}
	for _, joined := range order {
		info, violation := v.evaluateGroupLocked(groups[joined])
		report.Groups = append(report.Groups, info)
		if violation != nil {
			report.Violations = append(report.Violations, *violation)
		}
	}
	slices.SortFunc(report.Violations, func(a, b Violation) int {
		if a.Size != b.Size {
			return a.Size - b.Size
		}
		return strings.Compare(a.Key.joined, b.Key.joined)
	})
	return report, nil
}

func (v *Verifier) groupKeyLocked(rec Record) (GroupKey, error) {
	attrs := make([]string, len(v.cfg.QuasiIDs))
	for i, value := range rec.QuasiValues {
		generalized, err := v.cfg.Trees[i].Generalize(value, v.cfg.Levels[i])
		if err != nil {
			return GroupKey{}, err
		}
		attrs[i] = generalized
	}
	return GroupKey{attrs: attrs, joined: strings.Join(attrs, "\x00")}, nil
}
