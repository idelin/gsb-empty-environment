package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// MatchPolicy 决定规则中的 apiVersions 如何与集群启用版本匹配。
type MatchPolicy string

const (
	// MatchPolicyExact 仅精确匹配列出的版本，旧版本不算覆盖。
	MatchPolicyExact MatchPolicy = "Exact"
	// MatchPolicyEquivalent 按集群版本表的等价版本补全匹配。
	MatchPolicyEquivalent MatchPolicy = "Equivalent"
)

// Scope 限定规则作用的资源范围。
type Scope string

const (
	ScopeAll        Scope = "*"
	ScopeCluster    Scope = "Cluster"
	ScopeNamespaced Scope = "Namespaced"
)

// AllOperations 是操作通配 * 展开后的全集。
var AllOperations = []string{"CREATE", "UPDATE", "DELETE", "CONNECT"}

// MatchRule 对应 ValidatingAdmissionPolicy 的一条 matchRules / excludeRules 条目。
type MatchRule struct {
	APIGroups   []string
	APIVersions []string
	Operations  []string
	Resources   []string
	Scope       Scope // 空视为 "*"
}

// PolicySpec 是待保存策略的匹配相关字段。
type PolicySpec struct {
	MatchPolicy  MatchPolicy // 空视为 Exact
	MatchRules   []MatchRule
	ExcludeRules []MatchRule
}

// EnabledResource 描述集群启用的一个资源版本。
type EnabledResource struct {
	Group              string
	Version            string
	Resource           string
	Subresources       []string
	Namespaced         bool
	EquivalentVersions []string // 与该版本等价的其他启用版本
}

// Requirement 是组织要求被策略覆盖的一项。
type Requirement struct {
	Group       string
	Resource    string
	Subresource string   // 空表示主资源
	Operations  []string // 支持 "*"
	Scope       Scope    // 可选，空表示不限
}

// Gap 报告一项未覆盖的要求。
type Gap struct {
	Group       string
	Resource    string
	Subresource string
	Operation   string
	Versions    []string // 未覆盖的启用版本
	Reason      string
}

func (g Gap) String() string {
	name := g.Group + "/" + g.Resource
	if g.Subresource != "" {
		name += "/" + g.Subresource
	}
	return fmt.Sprintf("%s %s 未覆盖版本 %v: %s", name, g.Operation, g.Versions, g.Reason)
}

// tuple 是覆盖集合的最小粒度：(组, 版本, 资源, 子资源, 操作)。
type tuple struct {
	group       string
	version     string
	resource    string
	subresource string
	operation   string
}

// AnalyzeCoverage 在策略保存前分析匹配约束对要求清单的覆盖情况，
// 返回全部未覆盖项；返回空切片表示完整覆盖。
func AnalyzeCoverage(spec PolicySpec, cluster []EnabledResource, reqs []Requirement) []Gap {
	policy := spec.MatchPolicy
	if policy == "" {
		policy = MatchPolicyExact
	}

	covered := map[tuple]bool{}
	for _, rule := range spec.MatchRules {
		for _, t := range expandRule(rule, cluster, policy) {
			covered[t] = true
		}
	}
	// 排除规则先从包含规则里扣除。
	for _, rule := range spec.ExcludeRules {
		for _, t := range expandRule(rule, cluster, policy) {
			delete(covered, t)
		}
	}

	var gaps []Gap
	for _, req := range reqs {
		gaps = append(gaps, checkRequirement(req, cluster, covered)...)
	}
	return gaps
}

// expandRule 把一条规则展开为覆盖元组集合。
func expandRule(rule MatchRule, cluster []EnabledResource, policy MatchPolicy) []tuple {
	ops := expandOperations(rule.Operations)
	scope := rule.Scope
	if scope == "" {
		scope = ScopeAll
	}

	var out []tuple
	for _, er := range cluster {
		if !matchAny(rule.APIGroups, er.Group) {
			continue
		}
		if !versionMatches(rule.APIVersions, er, policy) {
			continue
		}
		if !scopeCovers(scope, er.Namespaced) {
			continue
		}
		for _, res := range expandResources(rule.Resources, er) {
			for _, op := range ops {
				out = append(out, tuple{
					group:       er.Group,
					version:     er.Version,
					resource:    res.resource,
					subresource: res.subresource,
					operation:   op,
				})
			}
		}
	}
	return out
}

type resourceRef struct {
	resource    string
	subresource string
}

// expandResources 展开 resources 模式：
// "*" 只匹配主资源；"*/*" 匹配全部主资源及子资源；
// "pods" 只匹配主资源；"pods/*" 匹配 pods 及其全部子资源；
// "pods/status" 只匹配该子资源。
func expandResources(patterns []string, er EnabledResource) []resourceRef {
	var out []resourceRef
	seen := map[resourceRef]bool{}
	add := func(resource, sub string) {
		r := resourceRef{resource: resource, subresource: sub}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, p := range patterns {
		name, sub, hasSub := strings.Cut(p, "/")
		switch {
		case !hasSub:
			// "pods" 或 "*"：仅主资源。
			if name == "*" || name == er.Resource {
				add(er.Resource, "")
			}
		case sub == "*":
			// "pods/*" 或 "*/*"：主资源加全部子资源。
			if name == "*" || name == er.Resource {
				add(er.Resource, "")
				for _, s := range er.Subresources {
					add(er.Resource, s)
				}
			}
		default:
			// "pods/status"：指定子资源。
			if name == er.Resource && contains(er.Subresources, sub) {
				add(er.Resource, sub)
			}
		}
	}
	return out
}

// versionMatches 判断规则的 apiVersions 是否覆盖该启用版本。
func versionMatches(patterns []string, er EnabledResource, policy MatchPolicy) bool {
	if contains(patterns, "*") || contains(patterns, er.Version) {
		return true
	}
	if policy != MatchPolicyEquivalent {
		return false
	}
	// Equivalent：按集群版本表的等价版本补全。
	for _, p := range patterns {
		if contains(er.EquivalentVersions, p) {
			return true
		}
	}
	return false
}

func expandOperations(ops []string) []string {
	if contains(ops, "*") {
		return AllOperations
	}
	return ops
}

func scopeCovers(scope Scope, namespaced bool) bool {
	switch scope {
	case ScopeAll:
		return true
	case ScopeNamespaced:
		return namespaced
	case ScopeCluster:
		return !namespaced
	}
	return false
}

// checkRequirement 检查单条要求在所有启用版本上是否被覆盖。
func checkRequirement(req Requirement, cluster []EnabledResource, covered map[tuple]bool) []Gap {
	var entries []EnabledResource
	for _, er := range cluster {
		if er.Group == req.Group && er.Resource == req.Resource &&
			(req.Scope == "" || req.Scope == ScopeAll || scopeCovers(req.Scope, er.Namespaced)) {
			entries = append(entries, er)
		}
	}

	ops := expandOperations(req.Operations)
	if len(entries) == 0 {
		gaps := make([]Gap, 0, len(ops))
		for _, op := range ops {
			gaps = append(gaps, Gap{
				Group:       req.Group,
				Resource:    req.Resource,
				Subresource: req.Subresource,
				Operation:   op,
				Reason:      "资源未在集群启用",
			})
		}
		return gaps
	}

	uncovered := map[string][]string{} // operation -> 未覆盖版本
	subresourceMissing := false
	for _, er := range entries {
		if req.Subresource != "" && !contains(er.Subresources, req.Subresource) {
			subresourceMissing = true
			for _, op := range ops {
				uncovered[op] = append(uncovered[op], er.Version)
			}
			continue
		}
		for _, op := range ops {
			t := tuple{
				group:       er.Group,
				version:     er.Version,
				resource:    er.Resource,
				subresource: req.Subresource,
				operation:   op,
			}
			if !covered[t] {
				uncovered[op] = append(uncovered[op], er.Version)
			}
		}
	}

	var gaps []Gap
	for _, op := range ops {
		versions := uncovered[op]
		if len(versions) == 0 {
			continue
		}
		sort.Strings(versions)
		reason := "匹配约束未覆盖"
		if subresourceMissing {
			reason = "子资源未在该版本启用"
		}
		gaps = append(gaps, Gap{
			Group:       req.Group,
			Resource:    req.Resource,
			Subresource: req.Subresource,
			Operation:   op,
			Versions:    versions,
			Reason:      reason,
		})
	}
	return gaps
}

func matchAny(patterns []string, value string) bool {
	for _, p := range patterns {
		if p == "*" || p == value {
			return true
		}
	}
	return false
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}
