package netsec

import (
	"errors"
	"fmt"
)

// Validate 校验模块图内部一致性：requires/opens/exports 的目标模块、
// 包归属以及补丁目标是否可解析。判定方法本身对缺失信息采取失败关闭
// （返回 false），Validate 供宿主在启动期显式发现描述符错误。
func (u *Universe) Validate() error {
	if u == nil {
		return errors.New("netsec: nil universe")
	}
	for _, m := range u.modules {
		known := packageSet(m.Packages)
		for _, req := range m.Requires {
			if _, ok := u.modules[req.Module]; !ok {
				return fmt.Errorf("netsec: module %q requires unknown module %q", m.Name, req.Module)
			}
		}
		for _, dir := range m.Opens {
			if err := validateDirective(m.Name, "opens", dir, known, u); err != nil {
				return err
			}
		}
		for _, dir := range m.Exports {
			if err := validateDirective(m.Name, "exports", dir, known, u); err != nil {
				return err
			}
		}
	}
	for _, patch := range u.patches {
		if _, ok := u.modules[patch.Source]; !ok {
			return fmt.Errorf("netsec: add-opens references unknown source module %q", patch.Source)
		}
		for _, target := range patch.Targets {
			if target == AllUnnamed {
				continue
			}
			if _, ok := u.modules[target]; !ok {
				return fmt.Errorf("netsec: add-opens %s/%s targets unknown module %q", patch.Source, patch.Package, target)
			}
		}
	}
	return nil
}

func validateDirective(moduleName, kind string, dir PackageDirective, known map[string]bool, u *Universe) error {
	if dir.Package == "" {
		return fmt.Errorf("netsec: module %q has empty %s package", moduleName, kind)
	}
	if known != nil && !known[dir.Package] {
		return fmt.Errorf("netsec: module %q %s unknown package %q", moduleName, kind, dir.Package)
	}
	for _, target := range dir.To {
		if target == AllUnnamed {
			return fmt.Errorf("netsec: %s %q in module %q cannot target %s; it is reserved for --add-opens", kind, dir.Package, moduleName, AllUnnamed)
		}
		if _, ok := u.modules[target]; !ok {
			return fmt.Errorf("netsec: module %q %s %q to unknown module %q", moduleName, kind, dir.Package, target)
		}
	}
	return nil
}

func packageSet(packages []string) map[string]bool {
	if len(packages) == 0 {
		return nil
	}
	set := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		set[pkg] = true
	}
	return set
}

// CanRead 判定 caller 模块是否可读 target 模块，即 JPMS 的
// Module.canRead 语义（含 requires transitive 传递可读性）。
//
// 规则：
//   - 未命名模块读取所有模块；
//   - 自动模块读取所有命名模块与未命名模块；
//   - 命名模块通过 requires 边直接读取，并沿 requires transitive 边
//     传递（普通 requires 不继续传播）；
//   - --add-opens 命中时按 JVM 语义隐含建立一条可读性边。
func (u *Universe) CanRead(caller, target string) bool {
	if u == nil {
		return false
	}
	if caller == target {
		return u.moduleKnown(caller)
	}
	if caller == UnnamedModule {
		return u.unnamed > 0 && u.moduleKnown(target)
	}
	callerMod, ok := u.modules[caller]
	if !ok {
		return false
	}
	if target == UnnamedModule {
		// 自动模块隐含读取未命名模块；普通命名模块需要显式读取边，
		// 本模型不接受隐式边，保持失败关闭。
		return u.unnamed > 0 && callerMod.Automatic
	}
	if _, ok := u.modules[target]; !ok {
		return false
	}
	if callerMod.Automatic {
		return true
	}
	if u.patchImpliesRead(caller, target) {
		return true
	}

	// BFS：从 caller 的直接 requires 出发，只沿 transitive 边继续传播。
	visited := map[string]bool{caller: true}
	frontier := make([]string, 0, len(callerMod.Requires))
	for _, req := range callerMod.Requires {
		if visited[req.Module] {
			continue
		}
		visited[req.Module] = true
		frontier = append(frontier, req.Module)
	}
	for len(frontier) > 0 {
		name := frontier[0]
		frontier = frontier[1:]
		if name == target {
			return true
		}
		next, ok := u.modules[name]
		if !ok {
			continue
		}
		for _, req := range next.Requires {
			if !req.Transitive || visited[req.Module] {
				continue
			}
			visited[req.Module] = true
			frontier = append(frontier, req.Module)
		}
	}
	return false
}

// IsOpen 判定 target 模块的 pkg 是否对 caller 开放（Module.isOpen 语义），
// 不考虑可读性；完整的反射判定使用 CanSetAccessible。
func (u *Universe) IsOpen(caller, target, pkg string) bool {
	if u == nil || pkg == "" {
		return false
	}
	if caller == target {
		return u.moduleKnown(caller)
	}
	targetMod, ok := u.modules[target]
	if !ok {
		// 未命名模块的所有包对所有模块开放。
		if target == UnnamedModule && u.unnamed > 0 {
			return true
		}
		return false
	}
	if !u.ownsPackage(targetMod, pkg) {
		return false
	}
	// open module 与自动模块（隐含 open）的所有包无条件开放。
	if targetMod.Open {
		return true
	}
	for _, dir := range targetMod.Opens {
		if dir.Package != pkg {
			continue
		}
		if len(dir.To) == 0 {
			return true
		}
		for _, named := range dir.To {
			if named == caller {
				return true
			}
		}
	}
	return u.patchGrants(caller, target, pkg)
}

// CanSetAccessible 判定 caller 模块对 target 模块 pkg 包中的非公开成员
// 调用 setAccessible 是否会被 JVM 允许。
//
// 两个条件必须同时满足：caller 可读 target，且 pkg 对 caller 开放。
// --add-opens 补丁命中时同时满足开放与隐含可读性（JVM 处理补丁的
// 真实语义），因此可绕过缺失的 requires 边。
func (u *Universe) CanSetAccessible(caller, target, pkg string) bool {
	if u == nil || pkg == "" {
		return false
	}
	if !u.knownPair(caller, target) {
		return false
	}
	if !u.ownsTargetPackage(target, pkg) {
		return false
	}
	if !u.patchGrants(caller, target, pkg) && !u.CanRead(caller, target) {
		return false
	}
	return u.IsOpen(caller, target, pkg)
}

func (u *Universe) knownPair(caller, target string) bool {
	if !u.moduleKnown(caller) || !u.moduleKnown(target) {
		return false
	}
	return true
}

func (u *Universe) moduleKnown(name string) bool {
	if name == UnnamedModule {
		return u.unnamed > 0
	}
	_, ok := u.modules[name]
	return ok
}

func (u *Universe) ownsTargetPackage(target, pkg string) bool {
	if target == UnnamedModule {
		return true
	}
	return u.ownsPackage(u.modules[target], pkg)
}

func (u *Universe) ownsPackage(m *Descriptor, pkg string) bool {
	if m == nil || len(m.Packages) == 0 {
		return true
	}
	for _, owned := range m.Packages {
		if owned == pkg {
			return true
		}
	}
	return false
}

// patchGrants 报告 --add-opens 补丁是否把 target/pkg 开放给 caller。
func (u *Universe) patchGrants(caller, target, pkg string) bool {
	for _, patch := range u.patches {
		if patch.Source != target || patch.Package != pkg {
			continue
		}
		for _, named := range patch.Targets {
			if named == caller || (named == AllUnnamed && caller == UnnamedModule) {
				return true
			}
		}
	}
	return false
}

// patchImpliesRead 报告是否存在命中 caller -> target 的补丁。JVM 在
// 应用 add-opens 时会同时 implAddReads，故补丁隐含可读性边。
func (u *Universe) patchImpliesRead(caller, target string) bool {
	for _, patch := range u.patches {
		if patch.Source != target {
			continue
		}
		for _, named := range patch.Targets {
			if named == caller || (named == AllUnnamed && caller == UnnamedModule) {
				return true
			}
		}
	}
	return false
}
