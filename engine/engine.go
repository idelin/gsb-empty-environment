package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// Series 一个输入系列：标签集合加上一个可聚合的贡献值。
type Series struct {
	ID     string   `json:"id"`
	Labels LabelSet `json:"labels"`
	Value  float64  `json:"value"`
}

// Conflict 多个输入系列映射到同一输出且未开启聚合时产生的冲突。
// MinimalPair 是该冲突下字典序最小的一对输入系列。
type Conflict struct {
	Output      LabelSet
	Series      []string
	MinimalPair [2]string
}

// MergeGroup 显式聚合模式下映射到同一输出的系列集合，
// 记录每个系列的独立贡献，撤回时可恢复。
type MergeGroup struct {
	Output   LabelSet
	contribs map[string]float64
}

// Total 返回聚合后的总值。
func (g *MergeGroup) Total() float64 {
	var sum float64
	for _, v := range g.contribs {
		sum += v
	}
	return sum
}

// Contributions 返回各系列的独立贡献副本。
func (g *MergeGroup) Contributions() map[string]float64 {
	out := make(map[string]float64, len(g.contribs))
	for k, v := range g.contribs {
		out[k] = v
	}
	return out
}

// Engine 标签重写引擎：维护规则、系列、冲突图、聚合组与按版本隔离的缓存。
type Engine struct {
	rules   *RuleSet
	series  map[string]Series
	outputs map[string]LabelSet
	byKey   map[string][]string // 冲突图：输出标识 -> 输入系列（已排序）
	groups  map[string]*MergeGroup
	cache   map[uint64]map[string]LabelSet // 规则版本 -> 输入标识 -> 输出
	tele    *Telemetry
}

// New 创建引擎；tele 为 nil 时使用本地空遥测。
func New(tele *Telemetry) *Engine {
	if tele == nil {
		tele = NewTelemetry()
	}
	return &Engine{
		series:  make(map[string]Series),
		outputs: make(map[string]LabelSet),
		byKey:   make(map[string][]string),
		groups:  make(map[string]*MergeGroup),
		cache:   make(map[uint64]map[string]LabelSet),
		tele:    tele,
	}
}

// Publish 校验并原子发布一批规则：任何一条非法则整体不生效。
func (e *Engine) Publish(version uint64, merge bool, reserved []string, rules []Rule) error {
	rs, err := Compile(version, merge, reserved, rules)
	if err != nil {
		return err
	}
	return e.PublishSet(rs)
}

// PublishSet 原子发布一个已编译的规则集；版本必须严格递增。
func (e *Engine) PublishSet(rs *RuleSet) error {
	if rs == nil {
		return errors.New("nil ruleset")
	}
	if _, err := Compile(rs.Version, rs.Merge, rs.Reserved, rs.Rules); err != nil {
		return fmt.Errorf("invalid ruleset: %w", err)
	}
	if e.rules != nil && rs.Version <= e.rules.Version {
		return fmt.Errorf("ruleset version %d not greater than current %d", rs.Version, e.rules.Version)
	}
	prev := uint64(0)
	if e.rules != nil {
		prev = e.rules.Version
	}
	e.rules = rs
	payload, _ := json.Marshal(rs)
	e.tele.Emit(EvRulesetPublished, rs.Version, map[string]string{
		"ruleset": string(payload),
		"merge":   strconv.FormatBool(rs.Merge),
	})
	if prev > 0 {
		e.tele.Emit(EvCacheIsolated, rs.Version, map[string]string{
			"isolated_version": strconv.FormatUint(prev, 10),
		})
	}
	e.rebuild()
	return nil
}

// Upsert 新增或更新一个输入系列，并增量刷新冲突图与聚合组。
func (e *Engine) Upsert(s Series) error {
	if e.rules == nil {
		return errors.New("no ruleset published")
	}
	if s.ID == "" {
		return errors.New("series id required")
	}
	if len(s.Labels) == 0 {
		return errors.New("series labels required")
	}
	e.series[s.ID] = s
	payload, _ := json.Marshal(s)
	e.tele.Emit(EvSeriesUpserted, e.rules.Version, map[string]string{"series": string(payload)})
	e.rebuild()
	return nil
}

// Retract 动态撤回一个输入系列；聚合模式下其余系列恢复各自贡献。
func (e *Engine) Retract(id string) bool {
	if _, ok := e.series[id]; !ok {
		return false
	}
	delete(e.series, id)
	e.tele.Emit(EvSeriesRetracted, e.rules.Version, map[string]string{"id": id})
	e.rebuild()
	return true
}

// rebuild 全量重算输出、冲突图与聚合组。缓存按当前规则版本隔离存取。
func (e *Engine) rebuild() {
	vc := e.cache[e.rules.Version]
	if vc == nil {
		vc = make(map[string]LabelSet)
		e.cache[e.rules.Version] = vc
	}
	outputs := make(map[string]LabelSet, len(e.series))
	byKey := make(map[string][]string)
	groups := make(map[string]*MergeGroup)
	for _, id := range sortedSeriesIDs(e.series) {
		s := e.series[id]
		inKey := s.Labels.Key()
		out, ok := vc[inKey]
		if !ok {
			out = e.rules.Apply(s.Labels)
			vc[inKey] = out
		}
		outputs[id] = out
		k := out.Key()
		byKey[k] = append(byKey[k], id)
		g := groups[k]
		if g == nil {
			g = &MergeGroup{Output: out, contribs: make(map[string]float64)}
			groups[k] = g
		}
		g.contribs[id] = s.Value
	}
	e.outputs = outputs
	e.byKey = byKey
	e.groups = groups
	for _, k := range sortedKeys(byKey) {
		ids := byKey[k]
		if len(ids) < 2 {
			continue
		}
		if e.rules.Merge {
			e.tele.Emit(EvSeriesMerged, e.rules.Version, map[string]string{
				"output":  k,
				"members": joinIDs(ids),
			})
		} else {
			e.tele.Emit(EvConflictDetected, e.rules.Version, map[string]string{
				"output": k,
				"pair":   ids[0] + "|" + ids[1],
			})
		}
	}
}

// Version 返回当前规则版本，未发布时为 0。
func (e *Engine) Version() uint64 {
	if e.rules == nil {
		return 0
	}
	return e.rules.Version
}

// Series 按 ID 查询输入系列。
func (e *Engine) Series(id string) (Series, bool) {
	s, ok := e.series[id]
	return s, ok
}

// Output 返回某输入系列当前的输出标签。
func (e *Engine) Output(id string) (LabelSet, bool) {
	out, ok := e.outputs[id]
	return out, ok
}

// Conflicts 返回当前全部冲突；显式聚合模式下为空（冲突被合法合并）。
func (e *Engine) Conflicts() []Conflict {
	if e.rules == nil || e.rules.Merge {
		return nil
	}
	var out []Conflict
	for _, k := range sortedKeys(e.byKey) {
		ids := e.byKey[k]
		if len(ids) < 2 {
			continue
		}
		out = append(out, Conflict{
			Output:      e.outputs[ids[0]],
			Series:      append([]string(nil), ids...),
			MinimalPair: [2]string{ids[0], ids[1]},
		})
	}
	return out
}

// MergedGroups 返回显式聚合模式下的合并组：输出标识 -> 系列 -> 贡献。
func (e *Engine) MergedGroups() map[string]map[string]float64 {
	out := make(map[string]map[string]float64)
	if e.rules == nil || !e.rules.Merge {
		return out
	}
	for k, g := range e.groups {
		out[k] = g.Contributions()
	}
	return out
}

// Value 返回系列的有效值：聚合模式下为其所在合并组的总值，
// 否则为其自身贡献。系列重新分离后自然恢复各自贡献。
func (e *Engine) Value(id string) (float64, bool) {
	s, ok := e.series[id]
	if !ok {
		return 0, false
	}
	if e.rules != nil && e.rules.Merge {
		if g := e.groups[e.outputs[id].Key()]; g != nil {
			return g.Total(), true
		}
	}
	return s.Value, true
}

// CacheEntries 返回指定规则版本的缓存条目数，用于验证版本隔离。
func (e *Engine) CacheEntries(version uint64) int {
	return len(e.cache[version])
}

// Telemetry 返回引擎关联的本地遥测。
func (e *Engine) Telemetry() *Telemetry {
	return e.tele
}

// Snapshot 捕获引擎可观察状态，用于回放一致性校验。
type Snapshot struct {
	Version   uint64
	Outputs   map[string]string
	Conflicts []string
	Groups    map[string]float64
}

// Snapshot 返回当前状态快照。
func (e *Engine) Snapshot() Snapshot {
	snap := Snapshot{
		Version: e.Version(),
		Outputs: make(map[string]string, len(e.outputs)),
		Groups:  make(map[string]float64),
	}
	for id, out := range e.outputs {
		snap.Outputs[id] = out.Key()
	}
	for _, c := range e.Conflicts() {
		snap.Conflicts = append(snap.Conflicts,
			c.MinimalPair[0]+"|"+c.MinimalPair[1]+"@"+c.Output.Key())
	}
	if e.rules != nil && e.rules.Merge {
		for k, g := range e.groups {
			snap.Groups[k] = g.Total()
		}
	}
	return snap
}

func sortedSeriesIDs(m map[string]Series) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func joinIDs(ids []string) string {
	out := ids[0]
	for _, id := range ids[1:] {
		out += "," + id
	}
	return out
}
