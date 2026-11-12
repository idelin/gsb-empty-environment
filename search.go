package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// ReplaceKind 区分攻击者的两种能力。
type ReplaceKind string

const (
	// ModifyLeaf：修改未覆盖叶子字段（取值须来自已观察消息同路径）。
	ModifyLeaf ReplaceKind = "modify"
	// CopySubtree：把已观察消息中同路径的子树整体复制过来。
	CopySubtree ReplaceKind = "copy"
)

// Replacement 描述一次替换动作。
type Replacement struct {
	Path   string
	Kind   ReplaceKind
	Source int // 取值/子树来源的观察消息下标
	Value  *Node
}

// Finding 是一组通过验证谓词且改变授权目标的最小替换。
type Finding struct {
	Replacements []Replacement
	Forged       *Node
	Before       Target
	After        Target
}

// Options 控制搜索；零值取默认值。
type Options struct {
	SigPath string // 默认 "sig"
	ToPath  string // 默认 "body.to"
	OpPath  string // 默认 "body.op"
	// MaxCandidates 是候选子树上限，默认 16，最大 16。
	MaxCandidates int
	// MaxSetSize 是枚举的最大替换集合大小，默认 4。
	MaxSetSize int
}

func (o Options) withDefaults() Options {
	if o.SigPath == "" {
		o.SigPath = "sig"
	}
	if o.ToPath == "" {
		o.ToPath = "body.to"
	}
	if o.OpPath == "" {
		o.OpPath = "body.op"
	}
	if o.MaxCandidates <= 0 || o.MaxCandidates > 16 {
		o.MaxCandidates = 16
	}
	if o.MaxSetSize <= 0 {
		o.MaxSetSize = 4
	}
	return o
}

// pathCovered 报告 path 是否被某条覆盖路径包含。
func pathCovered(path string, covered []string) bool {
	for _, c := range covered {
		if c == path || strings.HasPrefix(path, c+".") {
			return true
		}
	}
	return false
}

// pathCoversOther 报告 path 是否是某条覆盖路径的祖先。
func pathCoversOther(path string, covered []string) bool {
	for _, c := range covered {
		if strings.HasPrefix(c, path+".") {
			return true
		}
	}
	return false
}

type candidate struct {
	repl Replacement
	rank int // 0=未覆盖叶子修改，1=子树复制
}

// buildCandidates 生成候选替换：攻击者只能修改未覆盖叶子，
// 或从已观察消息复制同路径子树；候选总数截断到上限。
func buildCandidates(original *Node, observations []*Node, covered []string, opts Options) []candidate {
	seen := make(map[string]bool)
	var cands []candidate
	for _, path := range Paths(original) {
		if path == "" || path == opts.SigPath || strings.HasPrefix(path, opts.SigPath+".") {
			continue
		}
		origSub, err := original.Get(path)
		if err != nil {
			continue
		}
		for src, obs := range observations {
			obsSub, err := obs.Get(path)
			if err != nil || Equal(origSub, obsSub) {
				continue
			}
			key := path + "\x00" + string(Canonical(obsSub))
			if seen[key] {
				continue
			}
			seen[key] = true
			isLeaf := origSub.Kind != KindMap && origSub.Kind != KindList
			switch {
			case isLeaf && !pathCovered(path, covered) && !pathCoversOther(path, covered):
				cands = append(cands, candidate{
					repl: Replacement{Path: path, Kind: ModifyLeaf, Source: src, Value: obsSub.Clone()},
					rank: 0,
				})
			default:
				cands = append(cands, candidate{
					repl: Replacement{Path: path, Kind: CopySubtree, Source: src, Value: obsSub.Clone()},
					rank: 1,
				})
			}
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].rank != cands[j].rank {
			return cands[i].rank < cands[j].rank
		}
		return cands[i].repl.Path < cands[j].repl.Path
	})
	if len(cands) > opts.MaxCandidates {
		cands = cands[:opts.MaxCandidates]
	}
	return cands
}

func applyReplacements(original *Node, repls []Replacement) (*Node, error) {
	root := original.Clone()
	for _, r := range repls {
		if err := root.Set(r.Path, r.Value.Clone()); err != nil {
			return nil, err
		}
	}
	return root, nil
}

// Search 在攻击者能力内寻找最小替换集：通过 verify 谓词且改变授权目标。
// verify 由测试宿主提供（持有密钥），攻击者只掌握覆盖声明与观察消息。
// 找不到时返回 (nil, nil)。
func Search(original *Node, observations []*Node, verify func(*Node) bool, opts Options) (*Finding, error) {
	opts = opts.withDefaults()
	covered, err := CoveredPaths(original, opts.SigPath)
	if err != nil {
		return nil, err
	}
	before, err := AuthTarget(original, opts.ToPath, opts.OpPath)
	if err != nil {
		return nil, err
	}
	cands := buildCandidates(original, observations, covered, opts)
	maxK := opts.MaxSetSize
	if maxK > len(cands) {
		maxK = len(cands)
	}
	for k := 1; k <= maxK; k++ {
		idx := make([]int, k)
		for i := range idx {
			idx[i] = i
		}
		for {
			repls := make([]Replacement, k)
			for i, j := range idx {
				repls[i] = cands[j].repl
			}
			forged, err := applyReplacements(original, repls)
			if err != nil {
				return nil, err
			}
			if verify(forged) {
				after, err := AuthTarget(forged, opts.ToPath, opts.OpPath)
				if err == nil && after != before {
					return &Finding{
						Replacements: repls,
						Forged:       forged,
						Before:       before,
						After:        after,
					}, nil
				}
			}
			if !nextCombination(idx, len(cands)) {
				break
			}
		}
	}
	return nil, nil
}

// nextCombination 生成下一个 k 组合的下标，返回 false 表示枚举完毕。
func nextCombination(idx []int, n int) bool {
	k := len(idx)
	for i := k - 1; i >= 0; i-- {
		if idx[i] < n-k+i {
			idx[i]++
			for j := i + 1; j < k; j++ {
				idx[j] = idx[j-1] + 1
			}
			return true
		}
	}
	return false
}

// Describe 给出 Finding 的可读摘要，供测试宿主记录。
func (f *Finding) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "目标 %s/%s -> %s/%s，替换 %d 处:",
		f.Before.Recipient, f.Before.Operation,
		f.After.Recipient, f.After.Operation, len(f.Replacements))
	for _, r := range f.Replacements {
		fmt.Fprintf(&b, " [%s %s 来源#%d]", r.Kind, r.Path, r.Source)
	}
	return b.String()
}
