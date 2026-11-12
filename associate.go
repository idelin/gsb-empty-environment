package netsec

import (
	"fmt"
	"sort"
)

// Verdict 是跨版本反例关联的结论。
type Verdict string

const (
	// VerdictAssociated 表示同一断言 ID 的反例通过显式迁移完整关联。
	VerdictAssociated Verdict = "associated"
	// VerdictAmbiguous 表示迁移存在多候选，关联方向可信但落点未唯一确定。
	VerdictAmbiguous Verdict = "ambiguous"
	// VerdictDiverged 表示输入结构改变、依赖被破坏或节点未映射，
	// 旧反例在新版本中无法按同一证据链成立。
	VerdictDiverged Verdict = "diverged"
)

// AssociationEvidence 是两个版本间单个反例的关联证据。
type AssociationEvidence struct {
	AssertionID    string
	Verdict        Verdict
	InputEqual     bool
	Violations     []Violation
	EdgeEvidence   []EdgeEvidence
	AmbiguousNodes []string
	Reasons        []string
}

// Associate 依据稳定断言 ID、结构化输入相等性和调用方提供的显式节点迁移
// 映射，关联同一反例在两个版本中的快照。
//
// 关联规则刻意保守：
//   - 断言 ID 不一致的反例永不关联，文本再相近也不合并；
//   - 输入必须递归结构相等，不做模糊或相似度匹配；
//   - 迁移映射必须覆盖旧图全部节点并保持每条依赖边，否则判为 diverged；
//   - 映射给出多个候选时保留全部候选，判为 ambiguous 而非自动选边。
func Associate(oldCounter, newCounter Counterexample, migration *NodeMigration) (*AssociationEvidence, error) {
	if err := oldCounter.Validate(); err != nil {
		return nil, fmt.Errorf("old counterexample invalid: %w", err)
	}
	if err := newCounter.Validate(); err != nil {
		return nil, fmt.Errorf("new counterexample invalid: %w", err)
	}
	if migration == nil {
		return nil, fmt.Errorf("an explicit node migration is required")
	}
	if migration.FromVersion != oldCounter.Version || migration.ToVersion != newCounter.Version {
		return nil, fmt.Errorf(
			"migration versions %q->%q do not match counterexample versions %q->%q",
			migration.FromVersion, migration.ToVersion, oldCounter.Version, newCounter.Version,
		)
	}

	evidence := &AssociationEvidence{
		AssertionID: oldCounter.AssertionID,
		InputEqual:  oldCounter.Input.Equal(newCounter.Input),
	}
	if oldCounter.AssertionID != newCounter.AssertionID {
		evidence.Verdict = VerdictDiverged
		evidence.Reasons = append(evidence.Reasons, fmt.Sprintf(
			"assertion id changed from %q to %q; close-looking defects are never auto-merged",
			oldCounter.AssertionID, newCounter.AssertionID,
		))
		return evidence, nil
	}

	if !evidence.InputEqual {
		evidence.Reasons = append(evidence.Reasons, "structured input differs between versions")
	}

	violations, edgeEvidence, err := migration.Check(oldCounter.Origin, newCounter.Origin)
	if err != nil {
		return nil, err
	}
	evidence.Violations = violations
	evidence.EdgeEvidence = edgeEvidence

	for _, oldID := range migration.oldNodeIDs() {
		if migration.IsAmbiguous(oldID) {
			evidence.AmbiguousNodes = append(evidence.AmbiguousNodes, oldID)
		}
	}
	sort.Strings(evidence.AmbiguousNodes)

	switch {
	case len(violations) > 0 || !evidence.InputEqual:
		evidence.Verdict = VerdictDiverged
		for _, violation := range violations {
			evidence.Reasons = append(evidence.Reasons, violation.Message)
		}
	case len(evidence.AmbiguousNodes) > 0:
		evidence.Verdict = VerdictAmbiguous
		evidence.Reasons = append(evidence.Reasons, fmt.Sprintf(
			"migration keeps multiple candidates for nodes %v; no auto-merge by text similarity",
			evidence.AmbiguousNodes,
		))
	default:
		evidence.Verdict = VerdictAssociated
		evidence.Reasons = append(evidence.Reasons, "assertion id, structured input and dependency chain all preserved")
	}
	return evidence, nil
}

// Tracker 供 Go 测试宿主按版本收纳反例，并在相邻版本间生成关联证据。
type Tracker struct {
	counters map[string]map[string]Counterexample
	order    []string
}

// NewTracker 创建一个空的反例追踪器。
func NewTracker() *Tracker {
	return &Tracker{counters: make(map[string]map[string]Counterexample)}
}

// Add 收纳某个版本的一个反例，断言 ID 在同一版本内必须唯一。
func (t *Tracker) Add(counter Counterexample) error {
	if err := counter.Validate(); err != nil {
		return err
	}
	if _, ok := t.counters[counter.Version]; !ok {
		t.counters[counter.Version] = make(map[string]Counterexample)
		t.order = append(t.order, counter.Version)
	}
	if _, dup := t.counters[counter.Version][counter.AssertionID]; dup {
		return fmt.Errorf("assertion %q already recorded for version %q", counter.AssertionID, counter.Version)
	}
	t.counters[counter.Version][counter.AssertionID] = counter
	return nil
}

// Versions 返回反例被登记的版本顺序。
func (t *Tracker) Versions() []string {
	return append([]string(nil), t.order...)
}

// ErrNotFound 表示追踪器中找不到指定版本或断言。
var ErrNotFound = fmt.Errorf("counterexample not found")

// Lookup 取回指定版本中某个断言 ID 的反例。
func (t *Tracker) Lookup(version, assertionID string) (Counterexample, error) {
	versioned, ok := t.counters[version]
	if !ok {
		return Counterexample{}, fmt.Errorf("%w: version %q", ErrNotFound, version)
	}
	counter, ok := versioned[assertionID]
	if !ok {
		return Counterexample{}, fmt.Errorf("%w: assertion %q in version %q", ErrNotFound, assertionID, version)
	}
	return counter, nil
}

// AssociatePair 关联两个已登记版本中的同一断言反例。
func (t *Tracker) AssociatePair(fromVersion, toVersion, assertionID string, migration *NodeMigration) (*AssociationEvidence, error) {
	oldCounter, err := t.Lookup(fromVersion, assertionID)
	if err != nil {
		return nil, err
	}
	newCounter, err := t.Lookup(toVersion, assertionID)
	if err != nil {
		return nil, err
	}
	return Associate(oldCounter, newCounter, migration)
}
