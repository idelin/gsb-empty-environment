package netsec

import (
	"errors"
	"fmt"
)

// UnnamedModule 是未命名模块在 Universe 中的标识。JPMS 中未命名模块
// 按类加载器区分，本判定模型将所有未命名模块归一到同一标识；它们的
// 可读性与开放性规则一致（读取所有模块、所有包开放）。
const UnnamedModule = ""

// AllUnnamed 是 --add-opens 目标列表中的特殊标记 ALL-UNNAMED，
// 表示对当前所有未命名模块开放。
const AllUnnamed = "ALL-UNNAMED"

// Require 描述模块描述符中的一条 requires 指令。
type Require struct {
	// Module 是被依赖模块的名称。
	Module string

	// Transitive 对应 requires transitive：依赖模块会把对该模块的
	// 可读性传递给读取自己的模块。
	Transitive bool
}

// PackageDirective 描述 exports/opens 指令。To 为空表示无限定指令
// （对所有模块开放/导出）；非空表示限定指令，仅对 To 中点名的模块生效。
type PackageDirective struct {
	// Package 是包的全限定名，例如 "com.acme.internal"。
	Package string

	// To 是限定目标模块名列表；为空表示无限定。
	To []string
}

// Descriptor 是一个命名模块的模块描述符快照。
type Descriptor struct {
	// Name 是模块名；空字符串保留给未命名模块。
	Name string

	// Open 对应 open module：开放模块的所有包对所有模块无条件开放，
	// 描述符中仍可出现 opens 指令但语义上是冗余的。
	Open bool

	// Automatic 对应自动模块。自动模块隐含 open 语义（所有包开放），
	// 并读取图中的全部其他模块（含未命名模块）。
	Automatic bool

	// Requires 是 requires（含 requires transitive）指令列表。
	Requires []Require

	// Exports 是 exports 指令列表。exports 只允许编译期/普通 public
	// 访问，不授予非公开成员的反射访问，因此不参与 setAccessible 判定，
	// 仅保留用于描述符校验与宿主审计。
	Exports []PackageDirective

	// Opens 是 opens 指令列表，含限定（to）与未限定两种形式。
	Opens []PackageDirective

	// Packages 枚举该模块拥有的包。为空时不做包归属校验；非空时
	// opens/exports 以及判定目标包都必须属于该模块。
	Packages []string
}

// AddOpensPatch 对应启动参数 --add-opens <source>/<package>=<target>(,<target>)*。
type AddOpensPatch struct {
	// Source 是被开放包所属的模块名，例如 "java.base"。
	Source string

	// Package 是被开放的包名。
	Package string

	// Targets 是开放目标模块名列表；AllUnnamed 表示所有未命名模块。
	Targets []string
}

// Universe 是插件加载器视角下的模块图与启动补丁集合。
type Universe struct {
	modules map[string]*Descriptor
	unnamed int
	patches []AddOpensPatch
}

// NewUniverse 创建空模块图。
func NewUniverse() *Universe {
	return &Universe{modules: make(map[string]*Descriptor)}
}

// Add 注册一个命名模块描述符。自动模块隐含开放，Open 字段会被置位。
func (u *Universe) Add(d Descriptor) error {
	if u == nil {
		return errors.New("netsec: nil universe")
	}
	if d.Name == UnnamedModule {
		return errors.New("netsec: named module must have a non-empty name; use AddUnnamed for the unnamed module")
	}
	if _, exists := u.modules[d.Name]; exists {
		return fmt.Errorf("netsec: duplicate module %q", d.Name)
	}
	if d.Automatic {
		d.Open = true
	}
	cp := d
	u.modules[d.Name] = &cp
	return nil
}

// AddUnnamed 登记一个未命名模块（按类加载器计次，判定规则相同）。
func (u *Universe) AddUnnamed() error {
	if u == nil {
		return errors.New("netsec: nil universe")
	}
	u.unnamed++
	return nil
}

// AddPatch 登记一条 --add-opens 启动补丁。
func (u *Universe) AddPatch(p AddOpensPatch) error {
	if u == nil {
		return errors.New("netsec: nil universe")
	}
	if p.Source == UnnamedModule {
		return errors.New("netsec: add-opens source module must be named")
	}
	if p.Package == "" {
		return fmt.Errorf("netsec: add-opens for %q must name a package", p.Source)
	}
	if len(p.Targets) == 0 {
		return fmt.Errorf("netsec: add-opens %s/%s must name at least one target", p.Source, p.Package)
	}
	u.patches = append(u.patches, p)
	return nil
}
