package cacheaudit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Verdict 是审计结论。
type Verdict int

const (
	// Linearizable 表示存在满足公开一致性契约的合法线性化顺序。
	Linearizable Verdict = iota
	// Stale 表示证据完整且不存在合法顺序：确定发生陈旧返回。
	Stale
	// Inconclusive 表示证据不足（日志丢片、环形覆盖、来源缺失等），无法判定。
	Inconclusive
)

func (v Verdict) String() string {
	switch v {
	case Linearizable:
		return "LINEARIZABLE"
	case Stale:
		return "STALE"
	case Inconclusive:
		return "INCONCLUSIVE"
	}
	return "UNKNOWN"
}

// StaleReport 定位一次确定的陈旧返回及其来源版本证据。
type StaleReport struct {
	HitSeq      uint64 // 陈旧命中事件
	Key         string
	GotVersion  uint64 // 实际返回版本
	WantVersion uint64 // 按契约应返回的版本；0 表示当时无任何合法可见版本
	SourceSeq   uint64 // 产生 GotVersion 的加载事件（来源版本证据）
}

func (r StaleReport) String() string {
	return fmt.Sprintf("hit #%d key=%q got v%d want v%d (source load #%d)",
		r.HitSeq, r.Key, r.GotVersion, r.WantVersion, r.SourceSeq)
}

// Report 是审计结果。
type Report struct {
	Verdict  Verdict
	Reason   string
	Culprits []StaleReport
	Dropped  uint64 // 审计时观察到的丢失事件数
}

func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "verdict=%s", r.Verdict)
	if r.Reason != "" {
		fmt.Fprintf(&b, " reason=%q", r.Reason)
	}
	if r.Dropped > 0 {
		fmt.Fprintf(&b, " dropped=%d", r.Dropped)
	}
	for _, c := range r.Culprits {
		fmt.Fprintf(&b, "\n  culprit: %s", c)
	}
	return b.String()
}

// 公开一致性契约（顺序规范）：
//   - LOAD(v)：暂存版本 v，暂存版本对命中不可见；
//   - PUBLISH：要求存在暂存版本，将其提升为可见版本；
//   - EVICT：清空暂存与可见版本；
//   - HIT(v)：合法当且仅当 v 等于当前可见版本且非 0（未发布加载不得命中）；
//   - MISS：合法当且仅当当前无可见版本。
//
// 实序约束：若事件 A 的返回不晚于事件 B 的调用（A.End <= B.Start），
// 则任何合法线性化中 A 必须排在 B 之前。

// step 按契约推进状态，返回事件在该顺序位置是否合法；不合法时不产生副作用。
func step(visible, pending map[string]uint64, ev Event) bool {
	switch ev.Kind {
	case KindLoad:
		pending[ev.Key] = ev.Version
		return true
	case KindPublish:
		if pending[ev.Key] == 0 {
			return false
		}
		visible[ev.Key] = pending[ev.Key]
		return true
	case KindEvict:
		pending[ev.Key] = 0
		visible[ev.Key] = 0
		return true
	case KindHit:
		return ev.Version != 0 && visible[ev.Key] == ev.Version
	case KindMiss:
		return visible[ev.Key] == 0
	}
	return false
}

// maxExhaustive 是可判定性上限：超出后报告证据不足而非猜测。
const maxExhaustive = 62

// Audit 审计一段带调用-返回区间的缓存历史。
// dropped 为日志报告的丢失事件数；dropped>0 或 Seq 不连续时，
// 必须报告证据不足，不得猜测完整历史。
func Audit(events []Event, dropped uint64) Report {
	if dropped > 0 {
		return Report{Verdict: Inconclusive, Dropped: dropped,
			Reason: fmt.Sprintf("log incomplete: %d events dropped by ring overwrite", dropped)}
	}
	if gap := seqGap(events); gap != "" {
		return Report{Verdict: Inconclusive, Reason: gap}
	}
	if miss := missingSources(events); miss != "" {
		return Report{Verdict: Inconclusive, Reason: miss}
	}
	if culprits := localizeStale(events); len(culprits) > 0 {
		return Report{Verdict: Stale, Culprits: culprits,
			Reason: "hit version contradicts a publish/evict that completed before the hit started"}
	}
	if len(events) > maxExhaustive {
		return Report{Verdict: Inconclusive,
			Reason: fmt.Sprintf("history too large (%d events) for exhaustive linearization check", len(events))}
	}
	if linearizable(events) {
		return Report{Verdict: Linearizable}
	}
	return Report{Verdict: Stale, Reason: "no legal linearization order exists under the contract"}
}

// seqGap 检查 Seq 是否从最小值开始连续。
func seqGap(events []Event) string {
	if len(events) == 0 {
		return ""
	}
	lo := events[0].Seq
	for i, ev := range events {
		if ev.Seq != lo+uint64(i) {
			return fmt.Sprintf("log incomplete: sequence gap at seq %d (expected %d)", ev.Seq, lo+uint64(i))
		}
	}
	return ""
}

// missingSources 检查每个命中的来源版本证据（load:<seq>）是否仍在日志中。
func missingSources(events []Event) string {
	loads := make(map[uint64]uint64) // load seq -> version
	for _, ev := range events {
		if ev.Kind == KindLoad && ev.Err == "" {
			loads[ev.Seq] = ev.Version
		}
	}
	for _, ev := range events {
		if ev.Kind != KindHit {
			continue
		}
		seq, ok := parseLoadSource(ev.Source)
		if !ok {
			return fmt.Sprintf("log incomplete: hit #%d has no load source evidence", ev.Seq)
		}
		ver, ok := loads[seq]
		if !ok {
			return fmt.Sprintf("log incomplete: hit #%d source load #%d is missing", ev.Seq, seq)
		}
		if ver != ev.Version {
			return fmt.Sprintf("log inconsistent: hit #%d v%d but source load #%d is v%d",
				ev.Seq, ev.Version, seq, ver)
		}
	}
	return ""
}

func parseLoadSource(source string) (uint64, bool) {
	s, ok := strings.CutPrefix(source, "load:")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

// localizeStale 用实序重放定位确定陈旧的命中：
// 对每个命中，只考虑在其调用前已完成（End <= Start）的发布与淘汰，
// 重放得到当时可见版本；与命中版本不一致即为确定陈旧。
func localizeStale(events []Event) []StaleReport {
	sorted := make([]Event, len(events))
	copy(sorted, events)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].End != sorted[j].End {
			return sorted[i].End < sorted[j].End
		}
		return sorted[i].Seq < sorted[j].Seq
	})
	var out []StaleReport
	for _, hit := range events {
		if hit.Kind != KindHit {
			continue
		}
		visible := uint64(0)
		for _, ev := range sorted {
			if ev.End > hit.Start {
				break
			}
			if ev.Key != hit.Key {
				continue
			}
			switch ev.Kind {
			case KindPublish:
				visible = ev.Version
			case KindEvict:
				visible = 0
			}
		}
		if visible != hit.Version {
			src, _ := parseLoadSource(hit.Source)
			out = append(out, StaleReport{
				HitSeq:      hit.Seq,
				Key:         hit.Key,
				GotVersion:  hit.Version,
				WantVersion: visible,
				SourceSeq:   src,
			})
		}
	}
	return out
}

// linearizable 用带回溯与记忆化的搜索判定是否存在合法线性化顺序。
func linearizable(events []Event) bool {
	n := len(events)
	if n == 0 {
		return true
	}
	// pred[i]：按实序必须排在 i 之前的事件集合。
	pred := make([]uint64, n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j && events[j].End <= events[i].Start {
				pred[i] |= 1 << uint(j)
			}
		}
	}
	full := uint64(1)<<uint(n) - 1
	memo := make(map[string]bool) // 已知死路
	var dfs func(done uint64, visible, pending map[string]uint64) bool
	dfs = func(done uint64, visible, pending map[string]uint64) bool {
		if done == full {
			return true
		}
		key := memoKey(done, visible, pending)
		if memo[key] {
			return false
		}
		for i := 0; i < n; i++ {
			bit := uint64(1) << uint(i)
			if done&bit != 0 || pred[i]&^done != 0 {
				continue
			}
			ev := events[i]
			oldVis, hadVis := visible[ev.Key]
			oldPen, hadPen := pending[ev.Key]
			if !step(visible, pending, ev) {
				continue
			}
			if dfs(done|bit, visible, pending) {
				return true
			}
			restore(visible, ev.Key, oldVis, hadVis)
			restore(pending, ev.Key, oldPen, hadPen)
		}
		memo[key] = true
		return false
	}
	return dfs(0, make(map[string]uint64), make(map[string]uint64))
}

// restore 恢复 map 中某键的旧值。
func restore(m map[string]uint64, key string, old uint64, had bool) {
	if had {
		m[key] = old
	} else {
		delete(m, key)
	}
}

func memoKey(done uint64, visible, pending map[string]uint64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%x|", done)
	writeMap(&b, visible)
	b.WriteByte('|')
	writeMap(&b, pending)
	return b.String()
}

func writeMap(b *strings.Builder, m map[string]uint64) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "%s=%d;", k, m[k])
	}
}
