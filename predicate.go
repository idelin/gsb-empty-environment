package policycheck

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Arch 是系统调用 ABI 架构。调用号命名空间按 Arch 隔离。
type Arch string

const (
	ArchX86_64 Arch = "x86_64"
	ArchIA32   Arch = "ia32"
)

// NormalizeArch 归一化常见架构别名。
func NormalizeArch(raw string) (Arch, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "x86_64", "amd64", "x64":
		return ArchX86_64, true
	case "ia32", "i386", "i486", "i586", "i686", "x86", "386":
		return ArchIA32, true
	default:
		return Arch(raw), false
	}
}

// Input 是一次合法系统调用输入。
//
// Args 固定为六个 64 位寄存器/参数槽。Named 可在无注册函数时
// 提供命名谓词的具体布尔值，也可用于全枚举。
type Input struct {
	Arch  Arch            `json:"arch"`
	Nr    uint64          `json:"nr"`
	Args  [6]uint64       `json:"args"`
	Named map[string]bool `json:"named,omitempty"`
}

// EvalContext 传给调用方自定义命名谓词。
type EvalContext struct {
	Input    Input
	Registry Registry
}

// NamedFunc 是调用方提供的命名谓词实现。
type NamedFunc func(EvalContext) (bool, error)

// NamedPredicate 描述一个自定义命名谓词。
//
// Version 或 SourceDigest 应随实现语义变化而变化；Go 函数值本身
// 无法被稳定地内容哈希。密码学溯源图会把该摘要纳入节点证书。
type NamedPredicate struct {
	Name         string    `json:"name"`
	Version      string    `json:"version,omitempty"`
	SourceDigest Digest    `json:"source_digest,omitempty"`
	Func         NamedFunc `json:"-"`
}

// ContentDigest 返回命名谓词语义版本的绑定摘要。
func (p NamedPredicate) ContentDigest() Digest {
	if p.SourceDigest != "" {
		return p.SourceDigest
	}
	return HashCanonical("named-predicate", struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}{Name: p.Name, Version: p.Version})
}

// Registry 是命名谓词注册表。
type Registry map[string]NamedPredicate

func (r Registry) get(name string) (NamedPredicate, bool) {
	if r == nil {
		return NamedPredicate{}, false
	}
	p, ok := r[name]
	return p, ok
}

// PredKind 是受支持谓词的种类。
type PredKind string

const (
	KindArch      PredKind = "arch"
	KindNr        PredKind = "nr"
	KindArgMask   PredKind = "arg_mask"
	KindArgRange  PredKind = "arg_range"
	KindArgsEqual PredKind = "args_equal"
	KindMaskLEArg PredKind = "mask_le_arg"
	KindNamed     PredKind = "named"
	KindAnd       PredKind = "and"
	KindOr        PredKind = "or"
	KindNot       PredKind = "not"
)

// Pred 是可分析、可哈希的谓词 AST。
//
// 不要直接依赖零值；请使用 ArchIn、ArgMaskEquals、ArgsEqual、
// ArgMaskLEArg、NamedPred、And、Or、Not 等构造器。
type Pred struct {
	ID       string      `json:"id,omitempty"`
	Kind     PredKind    `json:"kind"`
	Width    uint8       `json:"width,omitempty"`
	Archs    []Arch      `json:"archs,omitempty"`
	NrRanges [][2]uint64 `json:"nr_ranges,omitempty"`
	Index    int         `json:"index,omitempty"`
	Mask     uint64      `json:"mask,omitempty"`
	Value    uint64      `json:"value,omitempty"`
	Lo       uint64      `json:"lo,omitempty"`
	Hi       uint64      `json:"hi,omitempty"`
	I        int         `json:"i,omitempty"`
	J        int         `json:"j,omitempty"`
	Name     string      `json:"name,omitempty"`
	Children []*Pred     `json:"children,omitempty"`
}

// ArchIn 构造架构谓词。
func ArchIn(archs ...Arch) *Pred {
	return &Pred{Kind: KindArch, Archs: append([]Arch(nil), archs...)}
}

// NrIs 构造调用号相等谓词。
func NrIs(nr uint64) *Pred {
	return &Pred{Kind: KindNr, NrRanges: [][2]uint64{{nr, nr}}}
}

// NrInRange 构造无符号调用号闭区间谓词。
func NrInRange(lo, hi uint64) *Pred {
	return &Pred{Kind: KindNr, NrRanges: [][2]uint64{{lo, hi}}}
}

// NrInRanges 构造调用号区间并集谓词。
func NrInRanges(ranges ...[2]uint64) *Pred {
	return &Pred{Kind: KindNr, NrRanges: append([][2]uint64(nil), ranges...)}
}

// ArgMaskEquals 构造 `(arg[index] & mask) == value`。
func ArgMaskEquals(index int, mask, value uint64) *Pred {
	return &Pred{Kind: KindArgMask, Index: index, Mask: mask, Value: value & mask}
}

// ArgInRange 构造无符号闭区间谓词。
func ArgInRange(index int, lo, hi uint64) *Pred {
	return &Pred{Kind: KindArgRange, Index: index, Lo: lo, Hi: hi}
}

// ArgsEqual 构造 `arg[i] == arg[j]`。
func ArgsEqual(i, j int) *Pred {
	return &Pred{Kind: KindArgsEqual, I: i, J: j}
}

// ArgMaskLEArg 构造无符号双参数关系 `(arg[i] & mask) <= arg[j]`。
//
// 比较始终按 uint64 无符号完成；2^63 不会被当作负数。
func ArgMaskLEArg(i int, mask uint64, j int) *Pred {
	return &Pred{Kind: KindMaskLEArg, I: i, Mask: mask, J: j}
}

// NamedPred 引用调用方注册的命名谓词。
func NamedPred(name string) *Pred {
	return &Pred{Kind: KindNamed, Name: name}
}

// And 构造合取。
func And(children ...*Pred) *Pred {
	return &Pred{Kind: KindAnd, Children: flatten(KindAnd, children)}
}

// Or 构造析取。
func Or(children ...*Pred) *Pred {
	return &Pred{Kind: KindOr, Children: flatten(KindOr, children)}
}

// Not 构造否定。
func Not(child *Pred) *Pred { return &Pred{Kind: KindNot, Children: []*Pred{child}} }

func flatten(kind PredKind, children []*Pred) []*Pred {
	var out []*Pred
	for _, child := range children {
		if child == nil {
			out = append(out, child)
			continue
		}
		if child.Kind == kind {
			out = append(out, child.Children...)
		} else {
			out = append(out, child)
		}
	}
	return out
}

// WithWidth 返回一个指定位宽的副本，用于小位宽全枚举模型。
func (p *Pred) WithWidth(bits uint) *Pred {
	if p == nil {
		return nil
	}
	cp := *p
	if bits == 0 || bits >= 64 {
		cp.Width = 0
	} else {
		cp.Width = uint8(bits)
	}
	cp.Children = make([]*Pred, len(p.Children))
	for i, child := range p.Children {
		cp.Children[i] = child.WithWidth(bits)
	}
	return &cp
}

func (p *Pred) wordBits() int {
	if p == nil || p.Width == 0 || p.Width >= 64 {
		return 64
	}
	return int(p.Width)
}

func wordMask(bits int) uint64 {
	if bits <= 0 || bits >= 64 {
		return math.MaxUint64
	}
	return (uint64(1) << uint(bits)) - 1
}

// Validate 检查谓词 AST 和参数下标。
func (p *Pred) Validate() error {
	if p == nil {
		return fmt.Errorf("policycheck: nil predicate")
	}
	switch p.Kind {
	case KindArch:
		if len(p.Archs) == 0 {
			return fmt.Errorf("policycheck: arch predicate requires at least one architecture")
		}
		for _, a := range p.Archs {
			if _, ok := NormalizeArch(string(a)); !ok {
				return fmt.Errorf("policycheck: unsupported architecture %q", a)
			}
		}
	case KindNr:
		if len(p.NrRanges) == 0 {
			return fmt.Errorf("policycheck: nr predicate requires a range")
		}
		for _, r := range p.NrRanges {
			if r[0] > r[1] {
				return fmt.Errorf("policycheck: invalid nr range [%d,%d]", r[0], r[1])
			}
		}
	case KindArgMask:
		if p.Index < 0 || p.Index >= 6 {
			return fmt.Errorf("policycheck: arg index %d out of range", p.Index)
		}
	case KindArgRange:
		if p.Index < 0 || p.Index >= 6 {
			return fmt.Errorf("policycheck: arg index %d out of range", p.Index)
		}
		if p.Lo > p.Hi {
			return fmt.Errorf("policycheck: invalid arg range [%d,%d]", p.Lo, p.Hi)
		}
	case KindArgsEqual:
		if p.I < 0 || p.I >= 6 || p.J < 0 || p.J >= 6 {
			return fmt.Errorf("policycheck: arg indices %d,%d out of range", p.I, p.J)
		}
	case KindMaskLEArg:
		if p.I < 0 || p.I >= 6 || p.J < 0 || p.J >= 6 {
			return fmt.Errorf("policycheck: arg indices %d,%d out of range", p.I, p.J)
		}
	case KindNamed:
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("policycheck: named predicate has empty name")
		}
	case KindAnd, KindOr:
		if len(p.Children) == 0 {
			return fmt.Errorf("policycheck: %s predicate requires children", p.Kind)
		}
		for _, c := range p.Children {
			if err := c.Validate(); err != nil {
				return err
			}
		}
	case KindNot:
		if len(p.Children) != 1 || p.Children[0] == nil {
			return fmt.Errorf("policycheck: not predicate requires one child")
		}
		return p.Children[0].Validate()
	default:
		return fmt.Errorf("policycheck: unknown predicate kind %q", p.Kind)
	}
	return nil
}

// Eval 在具体输入上求值谓词。
func (p *Pred) Eval(in Input, reg Registry) (bool, error) {
	if err := p.Validate(); err != nil {
		return false, err
	}
	bits := p.wordBits()
	mask := wordMask(bits)
	switch p.Kind {
	case KindArch:
		arch, ok := NormalizeArch(string(in.Arch))
		if !ok {
			return false, fmt.Errorf("policycheck: unsupported input architecture %q", in.Arch)
		}
		for _, a := range p.Archs {
			na, _ := NormalizeArch(string(a))
			if arch == na {
				return true, nil
			}
		}
		return false, nil
	case KindNr:
		for _, r := range p.NrRanges {
			if r[0] <= in.Nr && in.Nr <= r[1] {
				return true, nil
			}
		}
		return false, nil
	case KindArgMask:
		effectiveMask := p.Mask & mask
		return (in.Args[p.Index]&mask)&effectiveMask == (p.Value & effectiveMask), nil
	case KindArgRange:
		x := in.Args[p.Index] & mask
		return p.Lo <= x && x <= p.Hi, nil
	case KindArgsEqual:
		return in.Args[p.I]&mask == in.Args[p.J]&mask, nil
	case KindMaskLEArg:
		left := (in.Args[p.I] & mask) & (p.Mask & mask)
		right := in.Args[p.J] & mask
		return left <= right, nil
	case KindNamed:
		if np, ok := reg.get(p.Name); ok && np.Func != nil {
			return np.Func(EvalContext{Input: in, Registry: reg})
		}
		return in.Named[p.Name], nil
	case KindAnd:
		for _, c := range p.Children {
			v, err := c.Eval(in, reg)
			if err != nil {
				return false, err
			}
			if !v {
				return false, nil
			}
		}
		return true, nil
	case KindOr:
		for _, c := range p.Children {
			v, err := c.Eval(in, reg)
			if err != nil {
				return false, err
			}
			if v {
				return true, nil
			}
		}
		return false, nil
	case KindNot:
		v, err := p.Children[0].Eval(in, reg)
		return !v, err
	default:
		return false, fmt.Errorf("policycheck: cannot evaluate kind %q", p.Kind)
	}
}

// AtomKey 返回原子谓词的语义内容键；复合谓词返回其自身摘要。
func (p *Pred) AtomKey() string {
	if p == nil {
		return ""
	}
	if len(p.Children) > 0 {
		return p.ContentDigest().String()
	}
	return p.ContentDigest().String()
}

// ContentDigest 计算不含展示型 ID 的规范语义摘要。
func (p *Pred) ContentDigest() Digest {
	return HashCanonical("pred", p.semanticValue())
}

func (p *Pred) semanticValue() any {
	if p == nil {
		return nil
	}
	cp := *p
	cp.ID = ""
	if p.Children != nil {
		cp.Children = make([]*Pred, len(p.Children))
		for i, c := range p.Children {
			cp.Children[i] = c.semanticCopy()
		}
	}
	return cp
}

func (p *Pred) semanticCopy() *Pred {
	if p == nil {
		return nil
	}
	v, _ := p.semanticValue().(Pred)
	return &v
}

// Expression 返回稳定的人类可读表达式。
func (p *Pred) Expression() string {
	if p == nil {
		return "<nil>"
	}
	switch p.Kind {
	case KindArch:
		parts := make([]string, 0, len(p.Archs))
		for _, a := range p.Archs {
			parts = append(parts, string(a))
		}
		sort.Strings(parts)
		return "arch∈{" + strings.Join(parts, ",") + "}"
	case KindNr:
		parts := make([]string, 0, len(p.NrRanges))
		for _, r := range p.NrRanges {
			if r[0] == r[1] {
				parts = append(parts, fmt.Sprintf("%d", r[0]))
			} else {
				parts = append(parts, fmt.Sprintf("[%d,%d]", r[0], r[1]))
			}
		}
		return "nr∈{" + strings.Join(parts, ",") + "}"
	case KindArgMask:
		return fmt.Sprintf("(arg%d&%#x)==%#x", p.Index, p.Mask, p.Value)
	case KindArgRange:
		return fmt.Sprintf("%d<=arg%d<=%d", p.Lo, p.Index, p.Hi)
	case KindArgsEqual:
		return fmt.Sprintf("arg%d==arg%d", p.I, p.J)
	case KindMaskLEArg:
		return fmt.Sprintf("(arg%d&%#x)<=arg%d", p.I, p.Mask, p.J)
	case KindNamed:
		return "named:" + p.Name
	case KindAnd:
		return "(" + joinExpr(p.Children, " && ") + ")"
	case KindOr:
		return "(" + joinExpr(p.Children, " || ") + ")"
	case KindNot:
		return "!(" + p.Children[0].Expression() + ")"
	default:
		return "unknown"
	}
}

func joinExpr(ps []*Pred, sep string) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.Expression()
	}
	return strings.Join(parts, sep)
}

// CollectAtoms 收集 AST 中的全部原子谓词，按语义键去重并排序。
func CollectAtoms(root *Pred) []*Pred {
	seen := make(map[string]*Pred)
	var walk func(*Pred)
	walk = func(p *Pred) {
		if p == nil {
			return
		}
		switch p.Kind {
		case KindAnd, KindOr:
			for _, c := range p.Children {
				walk(c)
			}
		case KindNot:
			if len(p.Children) == 1 {
				walk(p.Children[0])
			}
		default:
			seen[p.AtomKey()] = p
		}
	}
	walk(root)
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*Pred, 0, len(keys))
	for _, k := range keys {
		out = append(out, seen[k])
	}
	return out
}

// CollectNamedNames 收集谓词中引用的全部命名谓词名。
func CollectNamedNames(preds ...*Pred) []string {
	seen := make(map[string]struct{})
	var walk func(*Pred)
	walk = func(p *Pred) {
		if p == nil {
			return
		}
		if p.Kind == KindNamed {
			seen[p.Name] = struct{}{}
		}
		for _, c := range p.Children {
			walk(c)
		}
	}
	for _, p := range preds {
		walk(p)
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
