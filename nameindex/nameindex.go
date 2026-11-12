// Package nameindex 为跨平台制品安装宿主提供目标路径名称索引。
//
// 不同来源的两条路径在目标文件系统上可能指向同一对象（大小写折叠、
// 一对多字符折叠、尾部字符被忽略等）。调用方提供字符折叠表与尾部
// 忽略字符集，索引逐段计算路径在目标文件系统上的身份，拒绝文件/
// 目录冲突与折叠后重复路径，并在错误中保留双方原始名称作为证据。
// 任何被拒绝的登记都不会改动索引，杜绝静默覆盖。
package nameindex

import (
	"fmt"
	"strings"
)

// FoldTable 将单个字符映射为其折叠形式。折叠可以是一对多的
// （如 'ß' → "ss"，'é' → "e" + 组合尖音符）；表中未出现的
// 字符保持原样。
type FoldTable map[rune]string

// Config 描述目标文件系统的名称归一化行为。
type Config struct {
	// Fold 为字符折叠表，可为 nil（不折叠）。
	Fold FoldTable
	// IgnoreTrailing 为段名尾部需要忽略的字符集合，例如
	// Windows 风格的 '.' 与 ' '。可为 nil。
	IgnoreTrailing map[rune]bool
}

// Identity 计算单段名称在目标文件系统上的身份：先按折叠表
// 逐字符折叠，再剥离折叠结果尾部的忽略字符。身份为空的段
// （如忽略集为 {'.'} 时的 "..."）是非法的。
func (c Config) Identity(segment string) (string, error) {
	var b strings.Builder
	for _, r := range segment {
		if rep, ok := c.Fold[r]; ok {
			b.WriteString(rep)
		} else {
			b.WriteRune(r)
		}
	}
	id := strings.TrimRightFunc(b.String(), func(r rune) bool {
		return c.IgnoreTrailing[r]
	})
	if id == "" {
		return "", fmt.Errorf("nameindex: segment %q has empty target identity", segment)
	}
	return id, nil
}

// ConflictKind 区分冲突类型。
type ConflictKind int

const (
	// ConflictDuplicate 表示折叠后与已登记路径重复。
	ConflictDuplicate ConflictKind = iota
	// ConflictFileDir 表示文件与目录身份冲突。
	ConflictFileDir
)

func (k ConflictKind) String() string {
	switch k {
	case ConflictDuplicate:
		return "duplicate path"
	case ConflictFileDir:
		return "file/directory conflict"
	}
	return "unknown conflict"
}

// ConflictError 描述一次被拒绝的登记，保留双方原始路径与
// 段名作为错误证据。
type ConflictError struct {
	Kind ConflictKind
	// Path 为本次被拒绝的原始路径。
	Path string
	// Segment 为发生冲突的原始段名。
	Segment string
	// ExistingPath 为此前已登记的原始路径。
	ExistingPath string
	// ExistingSegment 为此前已登记的原始段名。
	ExistingSegment string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("nameindex: %s: %q (segment %q) collides with %q (segment %q)",
		e.Kind, e.Path, e.Segment, e.ExistingPath, e.ExistingSegment)
}

type node struct {
	orig     string // 首次登记该身份的原始段名
	origPath string // 首次登记时的完整原始路径
	isFile   bool
	children map[string]*node
}

// Index 是目标文件系统的名称索引，按折叠后的段身份组织。
type Index struct {
	cfg  Config
	root *node
}

// New 创建使用给定归一化配置的空索引。
func New(cfg Config) *Index {
	return &Index{cfg: cfg, root: &node{children: make(map[string]*node)}}
}

// Add 登记一条斜杠分隔的相对路径。isFile 为 true 表示末段是
// 文件，否则为目录；中间段一律视为目录。路径非法或发生冲突时
// 返回错误且索引保持不变（不静默覆盖）；冲突错误为
// *ConflictError，携带双方原始名称。
func (ix *Index) Add(path string, isFile bool) error {
	segments := strings.Split(path, "/")
	ids := make([]string, len(segments))
	for i, seg := range segments {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("nameindex: invalid segment %q in path %q", seg, path)
		}
		id, err := ix.cfg.Identity(seg)
		if err != nil {
			return fmt.Errorf("nameindex: path %q: %w", path, err)
		}
		ids[i] = id
	}

	cur := ix.root
	for i, seg := range segments {
		last := i == len(segments)-1
		child, ok := cur.children[ids[i]]
		if ok {
			switch {
			case last && child.isFile != isFile:
				return &ConflictError{ConflictFileDir, path, seg, child.origPath, child.orig}
			case last:
				return &ConflictError{ConflictDuplicate, path, seg, child.origPath, child.orig}
			case child.isFile:
				return &ConflictError{ConflictFileDir, path, seg, child.origPath, child.orig}
			}
			cur = child
			continue
		}
		child = &node{orig: seg, origPath: path, children: make(map[string]*node)}
		if last {
			child.isFile = isFile
		}
		cur.children[ids[i]] = child
		cur = child
	}
	return nil
}

// Lookup 按折叠后的身份解析路径，返回首次登记时的原始路径、
// 末段是否为文件以及是否存在。供安装宿主在写入前查询目标。
func (ix *Index) Lookup(path string) (origPath string, isFile bool, ok bool) {
	cur := ix.root
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		id, err := ix.cfg.Identity(seg)
		if err != nil {
			return "", false, false
		}
		child, exists := cur.children[id]
		if !exists {
			return "", false, false
		}
		if i < len(segments)-1 && child.isFile {
			return "", false, false
		}
		if i == len(segments)-1 {
			return child.origPath, child.isFile, true
		}
		cur = child
	}
	return "", false, false
}
