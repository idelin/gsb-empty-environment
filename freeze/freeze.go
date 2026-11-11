// Package freeze 实现检测规则发布前的引用闭包冻结。
//
// 检测规则会引用外部词典与模型版本，这些名字在发布后若改指向新内容，
// 已审核的规则就会静默变更。Freeze 在一次事务内遍历依赖图：
// 每个名字首次解析得到的内容摘要与类型被固定（pin），事务内再次
// 解析同一名字（共享依赖或引用环）时必须与固定值一致，否则拒绝发布。
// 只有全部可达节点验证通过后，才构造并返回根清单，保证不会发布
// 混合版本。
package freeze

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	// ErrResolve 表示名称解析回调失败。
	ErrResolve = errors.New("freeze: resolve failed")
	// ErrDigestSwap 表示事务内同一名字再次解析得到不同摘要（换目标）。
	ErrDigestSwap = errors.New("freeze: digest changed mid-transaction")
	// ErrDigestMismatch 表示解析内容的摘要与图中声明的摘要不一致（损坏）。
	ErrDigestMismatch = errors.New("freeze: content digest mismatch")
	// ErrTypeConflict 表示声明类型与解析类型冲突，或事务内类型发生变化。
	ErrTypeConflict = errors.New("freeze: type conflict")
)

// Node 描述依赖图中的一个节点。Type 与 Digest 为可选声明：
// 非空时作为期望值参与校验；Deps 为出边（被引用的名字）。
type Node struct {
	Type   string
	Digest string
	Deps   []string
}

// ResolveFunc 按名字解析出内容与类型。发布宿主用它读取外部词典、
// 模型版本等命名引用。
type ResolveFunc func(name string) (content []byte, typ string, err error)

// Entry 是清单中的一个冻结条目。
type Entry struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Digest string   `json:"digest"` // 内容 SHA-256 的十六进制编码
	Deps   []string `json:"deps,omitempty"`
}

// Manifest 是验证通过后发布的根清单。
type Manifest struct {
	Root    string  `json:"root"`
	Entries []Entry `json:"entries"` // 按名字排序
	Digest  string  `json:"digest"`  // 条目规范序列化的 SHA-256
}

// Freeze 从 root 出发遍历 graph，冻结整个可达闭包。
//
// 引用环是允许的：环上的再次引用只做一致性校验，不会无限递归。
// 任一节点解析失败、摘要/类型与声明不符、或事务内解析结果发生
// 变化时，返回 nil 清单与错误，调用方不得发布任何内容。
func Freeze(root string, graph map[string]Node, resolve ResolveFunc) (*Manifest, error) {
	if resolve == nil {
		return nil, errors.New("freeze: nil resolver")
	}
	pinned := make(map[string]Entry)
	expanded := make(map[string]bool)

	var visit func(name string) error
	visit = func(name string) error {
		content, typ, err := resolve(name)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrResolve, name, err)
		}
		digest := digestOf(content)
		node := graph[name]

		if node.Type != "" && typ != node.Type {
			return fmt.Errorf("%w: %s: declared %q, resolved %q", ErrTypeConflict, name, node.Type, typ)
		}
		if node.Digest != "" && digest != node.Digest {
			return fmt.Errorf("%w: %s: declared %s, resolved %s", ErrDigestMismatch, name, node.Digest, digest)
		}
		if prev, ok := pinned[name]; ok {
			if prev.Digest != digest {
				return fmt.Errorf("%w: %s: pinned %s, now %s", ErrDigestSwap, name, prev.Digest, digest)
			}
			if prev.Type != typ {
				return fmt.Errorf("%w: %s: pinned %q, now %q", ErrTypeConflict, name, prev.Type, typ)
			}
		} else {
			deps := slices.Clone(node.Deps)
			slices.Sort(deps)
			deps = slices.Compact(deps)
			pinned[name] = Entry{Name: name, Type: typ, Digest: digest, Deps: deps}
		}

		if expanded[name] {
			return nil
		}
		expanded[name] = true
		for _, dep := range pinned[name].Deps {
			if err := visit(dep); err != nil {
				return err
			}
		}
		return nil
	}

	if err := visit(root); err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(pinned))
	for _, e := range pinned {
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.Name, b.Name) })

	return &Manifest{Root: root, Entries: entries, Digest: manifestDigest(entries)}, nil
}

func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func manifestDigest(entries []Entry) string {
	canonical, _ := json.Marshal(entries)
	return digestOf(canonical)
}
