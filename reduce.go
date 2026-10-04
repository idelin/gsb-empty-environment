package main

import (
	"fmt"
	"sort"
	"strings"
)

// Verdict 表示判定程序对某个配置的判定结果。
type Verdict int

const (
	// Pass 配置通过（未复现失败）。
	Pass Verdict = iota
	// FailTest 目标测试失败（缩减要保留的“有趣”失败）。
	FailTest
	// FailCompile 编译失败（与目标测试失败相区分，不视为目标失败）。
	FailCompile
)

func (v Verdict) String() string {
	switch v {
	case Pass:
		return "通过"
	case FailTest:
		return "目标测试失败"
	case FailCompile:
		return "编译失败"
	}
	return "未知"
}

// Oracle 是确定性的判定程序：对同一（规范化后）配置必须总是返回相同结果。
type Oracle interface {
	Judge(items []string) (Verdict, error)
}

// OracleFunc 适配普通函数为 Oracle。
type OracleFunc func(items []string) (Verdict, error)

// Judge 实现 Oracle 接口。
func (f OracleFunc) Judge(items []string) (Verdict, error) { return f(items) }

// Stats 记录判定统计信息。
type Stats struct {
	Calls       int // 实际调用判定程序的次数（缓存未命中）
	CacheHits   int // 缓存命中次数（未真正调用判定程序）
	Pass        int // 实际判定为“通过”的次数
	TestFail    int // 实际判定为“目标测试失败”的次数
	CompileFail int // 实际判定为“编译失败”的次数
}

// Reducer 保持依赖合法的失败配置缩减器。
type Reducer struct {
	requires   map[string][]string // item -> 它依赖的配置项
	dependents map[string][]string // item -> 依赖它的配置项（反向索引）
	oracle     Oracle
	cache      map[string]Verdict // 以规范化配置为键的判定缓存
	stats      Stats
}

// NewReducer 构造缩减器。deps[item] 列出 item 所依赖的配置项。
func NewReducer(deps map[string][]string, oracle Oracle) *Reducer {
	r := &Reducer{
		requires:   make(map[string][]string),
		dependents: make(map[string][]string),
		oracle:     oracle,
		cache:      make(map[string]Verdict),
	}
	for item, ds := range deps {
		seen := make(map[string]bool)
		for _, d := range ds {
			if d == item || seen[d] {
				continue
			}
			seen[d] = true
			r.requires[item] = append(r.requires[item], d)
			r.dependents[d] = append(r.dependents[d], item)
		}
	}
	return r
}

// Stats 返回当前判定统计。
func (r *Reducer) Stats() Stats { return r.stats }

// normalize 去重并排序，得到配置的规范化形式。
func normalize(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	sort.Strings(out)
	return out
}

// configKey 以规范化配置生成缓存键。
func configKey(items []string) string {
	return strings.Join(normalize(items), "\x1f")
}

// judge 带缓存地判定配置；只有缓存未命中才真正调用判定程序。
func (r *Reducer) judge(items []string) (Verdict, error) {
	norm := normalize(items)
	key := strings.Join(norm, "\x1f")
	if v, ok := r.cache[key]; ok {
		r.stats.CacheHits++
		return v, nil
	}
	v, err := r.oracle.Judge(norm)
	if err != nil {
		return Pass, fmt.Errorf("判定程序执行出错: %v", err)
	}
	r.cache[key] = v
	r.stats.Calls++
	switch v {
	case Pass:
		r.stats.Pass++
	case FailTest:
		r.stats.TestFail++
	case FailCompile:
		r.stats.CompileFail++
	}
	return v, nil
}

// missingDeps 返回 config 中违反依赖约束的 “item->缺失依赖” 列表。
func (r *Reducer) missingDeps(config map[string]bool) []string {
	var missing []string
	for item := range config {
		for _, d := range r.requires[item] {
			if !config[d] {
				missing = append(missing, item+"->"+d)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// removalClosure 计算从 config 中删除 seeds 时，为保持依赖合法
// 必须一并删除的配置项闭包（含 seeds 本身及其全部传递依赖者）。
func (r *Reducer) removalClosure(config map[string]bool, seeds []string) map[string]bool {
	removed := make(map[string]bool)
	queue := make([]string, 0, len(seeds))
	for _, s := range seeds {
		if config[s] && !removed[s] {
			removed[s] = true
			queue = append(queue, s)
		}
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, dep := range r.dependents[x] {
			if config[dep] && !removed[dep] {
				removed[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	return removed
}

func subtract(config, removed map[string]bool) map[string]bool {
	out := make(map[string]bool, len(config))
	for it := range config {
		if !removed[it] {
			out[it] = true
		}
	}
	return out
}

func sortedItems(config map[string]bool) []string {
	out := make([]string, 0, len(config))
	for it := range config {
		out = append(out, it)
	}
	sort.Strings(out)
	return out
}

// Reduce 对初始失败配置做缩减，返回一个依赖合法且 1-极小的失败配置：
// 再删除其中任何单项（连同被迫级联删除的依赖者）都不再复现目标测试失败。
func (r *Reducer) Reduce(initial []string) ([]string, error) {
	config := make(map[string]bool)
	for _, it := range normalize(initial) {
		config[it] = true
	}
	if miss := r.missingDeps(config); len(miss) > 0 {
		return nil, fmt.Errorf("初始配置违反依赖约束: %s", strings.Join(miss, ", "))
	}
	v, err := r.judge(sortedItems(config))
	if err != nil {
		return nil, err
	}
	if v != FailTest {
		return nil, fmt.Errorf("初始配置未复现目标测试失败（判定结果: %s）", v)
	}
	if config, err = r.ddmin(config); err != nil {
		return nil, err
	}
	if config, err = r.oneMinimal(config); err != nil {
		return nil, err
	}
	return sortedItems(config), nil
}

// ddmin 按递增粒度尝试整块删除；候选配置始终经依赖闭包修复，保证合法。
func (r *Reducer) ddmin(config map[string]bool) (map[string]bool, error) {
	n := 2
	for len(config) >= 2 {
		items := sortedItems(config)
		chunkSize := (len(items) + n - 1) / n
		progressed := false
		for start := 0; start < len(items); start += chunkSize {
			end := start + chunkSize
			if end > len(items) {
				end = len(items)
			}
			removal := r.removalClosure(config, items[start:end])
			cand := subtract(config, removal)
			v, err := r.judge(sortedItems(cand))
			if err != nil {
				return nil, err
			}
			if v == FailTest {
				config = cand
				n = 2
				progressed = true
				break
			}
		}
		if progressed {
			continue
		}
		if n >= len(items) {
			break
		}
		n *= 2
		if n > len(items) {
			n = len(items)
		}
	}
	return config, nil
}

// oneMinimal 反复尝试删除单项（含其依赖者闭包），直到任何单项删除都不再失败。
func (r *Reducer) oneMinimal(config map[string]bool) (map[string]bool, error) {
	for {
		reduced := false
		for _, item := range sortedItems(config) {
			removal := r.removalClosure(config, []string{item})
			cand := subtract(config, removal)
			v, err := r.judge(sortedItems(cand))
			if err != nil {
				return nil, err
			}
			if v == FailTest {
				config = cand
				reduced = true
				break
			}
		}
		if !reduced {
			return config, nil
		}
	}
}
