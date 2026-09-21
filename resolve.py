"""声明式“能力 -> 实现”解析器（仅标准库，Python 3.11+）。"""
from __future__ import annotations

import argparse
import json
import re
import sys
from dataclasses import dataclass, field
from functools import cmp_to_key, total_ordering
from typing import Optional


class ResolutionError(Exception):
    """可向用户展示的解析错误。"""


_VERSION_RE = re.compile(
    r"^(?P<major>0|[1-9]\d*)\.(?P<minor>0|[1-9]\d*)\.(?P<patch>0|[1-9]\d*)"
    r"(?:-(?P<prerelease>(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)"
    r"(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?$"
)
_CONSTRAINT_RE = re.compile(r"^\s*(>=|<=|==|!=|>|<|=|\^|~)?\s*(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)\s*$")


def _pre_key(ident: str):
    if ident.isdigit():
        return (0, int(ident))
    return (1, ident)


@total_ordering
class Version:
    def __init__(self, text: str):
        m = _VERSION_RE.match(text.strip())
        if not m:
            raise ResolutionError(f"非法版本号: {text!r}（应为 major.minor.patch[-预发布]）")
        self.text = m.group(0)
        self.major = int(m.group("major"))
        self.minor = int(m.group("minor"))
        self.patch = int(m.group("patch"))
        pre = m.group("prerelease")
        self.prerelease: Optional[tuple] = None
        if pre is not None:
            self.prerelease = tuple(_pre_key(p) for p in pre.split("."))

    @property
    def is_prerelease(self) -> bool:
        return self.prerelease is not None

    def _key(self):
        pre_flag = 0 if self.prerelease is not None else 1
        return (self.major, self.minor, self.patch, pre_flag,
                self.prerelease or ())

    def __eq__(self, other) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() == other._key()

    def __lt__(self, other) -> bool:
        return self._key() < other._key()

    def __hash__(self):
        return hash(self._key())

    def __repr__(self):
        return f"Version({self.text!r})"


class Constraint:
    def __init__(self, op: str, version: Version):
        self.op = op
        self.version = version

    @classmethod
    def parse(cls, spec: str) -> "Constraint":
        m = _CONSTRAINT_RE.match(spec or "")
        if not m:
            raise ResolutionError(f"非法版本约束: {spec!r}")
        op = m.group(1) or "=="
        if op == "=":
            op = "=="
        return cls(op, Version(m.group(2)))

    def satisfies(self, v: Version) -> bool:
        op, target = self.op, self.version
        if op == "==":
            return v == target
        if op == "!=":
            return v != target
        if op == ">=":
            return v >= target
        if op == "<=":
            return v <= target
        if op == ">":
            return v > target
        if op == "<":
            return v < target
        if op == "^":
            if v < target:
                return False
            if target.major > 0:
                return v.major == target.major
            if target.minor > 0:
                return v.major == 0 and v.minor == target.minor
            return v.major == 0 and v.minor == 0 and v.patch == target.patch
        if op == "~":
            return v >= target and v.minor == target.minor and v.major == target.major
        raise ResolutionError(f"未知操作符: {op}")

    def __repr__(self):
        return f"{self.op}{self.version.text}"


def parse_constraints(specs) -> list[Constraint]:
    if specs is None:
        return []
    if isinstance(specs, str):
        specs = [specs]
    if not isinstance(specs, list):
        raise ResolutionError("版本约束必须是字符串或字符串数组")
    return [Constraint.parse(s) for s in specs]


@dataclass(frozen=True)
class Requirement:
    capability: str
    constraints: tuple[Constraint, ...] = ()
    optional: bool = False
    priority: int = 0
    from_pkg: Optional[str] = None
    root: bool = False

    def accepts(self, v: Version) -> bool:
        return all(c.satisfies(v) for c in self.constraints)


@dataclass(frozen=True)
class Provide:
    capability: str
    version: Version
    priority: int

    def __repr__(self):
        return f"{self.capability}@{self.version.text}(p{self.priority})"


@dataclass
class Package:
    name: str
    version: Version
    provides: list[Provide]
    requires: list[Requirement]
    conflicts: frozenset[str]
    alternatives: tuple[str, ...]
    os: tuple[str, ...]
    arch: tuple[str, ...]
    priority: int

    @property
    def pkg_id(self) -> str:
        return f"{self.name}@{self.version.text}"

    @property
    def provide_caps(self) -> frozenset[str]:
        return frozenset(p.capability for p in self.provides)

    def provide(self, capability: str) -> Optional[Provide]:
        for p in self.provides:
            if p.capability == capability:
                return p
        return None


@dataclass
class Target:
    os: tuple[str, ...] = ()
    arch: tuple[str, ...] = ()

    @staticmethod
    def _norm(value) -> tuple[str, ...]:
        if value is None:
            return ()
        if isinstance(value, str):
            return (value,)
        if isinstance(value, list):
            return tuple(str(v) for v in value)
        raise ResolutionError("target.os / target.arch 必须是字符串或数组")

    @classmethod
    def from_raw(cls, raw: dict) -> "Target":
        raw = raw or {}
        return cls(cls._norm(raw.get("os")), cls._norm(raw.get("arch")))

    def compatible(self, pkg: Package) -> bool:
        if pkg.os and not any(v in self.os for v in pkg.os):
            return False
        if pkg.arch and not any(v in self.arch for v in pkg.arch):
            return False
        return True


def _as_str_tuple(value, field_name: str) -> tuple[str, ...]:
    if value is None:
        return ()
    if isinstance(value, str):
        value = [value]
    if not isinstance(value, list) or not all(isinstance(v, str) for v in value):
        raise ResolutionError(f"{field_name} 必须是字符串或字符串数组")
    return tuple(value)


def parse_requirements(items, from_pkg: Optional[str] = None) -> list[Requirement]:
    if items is None:
        return []
    if not isinstance(items, list):
        raise ResolutionError("requires 必须是数组")
    result: list[Requirement] = []
    for item in items:
        if isinstance(item, str):
            cap, specs = item, None
        elif isinstance(item, dict):
            cap = item.get("capability")
            specs = item.get("version")
            if not isinstance(cap, str) or not cap:
                raise ResolutionError("require.capability 必须是非空字符串")
        else:
            raise ResolutionError("require 条目必须是字符串或对象")
        result.append(Requirement(
            capability=cap,
            constraints=tuple(parse_constraints(specs)),
            optional=bool(item.get("optional", False)) if isinstance(item, dict) else False,
            priority=int(item.get("priority", 0)) if isinstance(item, dict) else 0,
            from_pkg=from_pkg,
        ))
    return result


def _parse_provides(items, pkg_name: str, default_priority: int,
                    default_version: Version) -> list[Provide]:
    if not isinstance(items, list) or not items:
        raise ResolutionError(f"包 {pkg_name!r} 的 provides 必须是非空数组")
    provides: list[Provide] = []
    seen: set[str] = set()
    for item in items:
        if isinstance(item, str):
            cap, ver_text, prio = item, None, default_priority
            if cap == "":
                raise ResolutionError(f"包 {pkg_name!r} 存在非法 provides 条目")
        elif isinstance(item, dict):
            cap = item.get("capability")
            ver_text = item.get("version")
            prio = int(item.get("priority", default_priority))
        else:
            raise ResolutionError("provide 条目必须是字符串或对象")
        if not isinstance(cap, str) or not cap:
            raise ResolutionError(f"包 {pkg_name!r} 存在非法 provides 条目")
        if cap in seen:
            raise ResolutionError(f"包 {pkg_name!r} 重复提供能力 {cap!r}")
        seen.add(cap)
        provides.append(Provide(cap, Version(ver_text) if ver_text else default_version, prio))
    return provides


def parse_package(raw: dict) -> Package:
    name = raw.get("name")
    ver_text = raw.get("version")
    if not isinstance(name, str) or not name:
        raise ResolutionError("包缺少 name")
    if not isinstance(ver_text, str) or not ver_text:
        raise ResolutionError(f"包 {name!r} 缺少 version")
    version = Version(ver_text)
    priority = int(raw.get("priority", 0))
    provides = _parse_provides(raw.get("provides"), name, priority, version)
    conflicts = frozenset(_as_str_tuple(raw.get("conflicts"), f"{name}.conflicts"))
    alternatives = _as_str_tuple(raw.get("alternatives"), f"{name}.alternatives")
    requires = parse_requirements(raw.get("requires"), from_pkg=name)
    return Package(
        name=name,
        version=version,
        provides=provides,
        requires=requires,
        conflicts=conflicts,
        alternatives=alternatives,
        os=_as_str_tuple(raw.get("os"), f"{name}.os"),
        arch=_as_str_tuple(raw.get("arch"), f"{name}.arch"),
        priority=priority,
    )


def _read_json(path: str):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except FileNotFoundError:
        raise ResolutionError(f"文件不存在: {path}")
    except json.JSONDecodeError as exc:
        raise ResolutionError(f"JSON 解析失败 {path}: {exc}") from exc


class Catalog:
    def __init__(self, packages: list[Package]):
        self.packages = packages
        self.by_id: dict[str, Package] = {}
        self.by_name: dict[str, list[Package]] = {}
        self.provides: dict[str, list[tuple[Package, Provide]]] = {}
        names = set()
        for pkg in packages:
            if pkg.pkg_id in self.by_id:
                raise ResolutionError(f"重复的包: {pkg.pkg_id}")
            self.by_id[pkg.pkg_id] = pkg
            self.by_name.setdefault(pkg.name, []).append(pkg)
            names.add(pkg.name)
            for prov in pkg.provides:
                self.provides.setdefault(prov.capability, []).append((pkg, prov))
        for pkg in packages:
            unknown = [a for a in pkg.alternatives if a not in names]
            if unknown:
                raise ResolutionError(f"包 {pkg.pkg_id} 的 alternatives 引用了未知包: {', '.join(unknown)}")
        for entries in self.by_name.values():
            entries.sort(key=lambda p: p.version, reverse=True)
        for entries in self.provides.values():
            entries.sort(key=lambda e: (e[1].priority, e[1].version, e[0].name),
                         reverse=True)

    @classmethod
    def load(cls, path: str) -> "Catalog":
        data = _read_json(path)
        if not isinstance(data, dict) or not isinstance(data.get("packages"), list):
            raise ResolutionError(f"包目录文件 {path} 需要顶层 {{\"packages\": [...]}}")
        return cls([parse_package(p) for p in data["packages"]])


@dataclass
class BuildInput:
    requires: list[Requirement]
    target: Target
    allow_prereleases: bool = False

    @classmethod
    def load(cls, path: str) -> "BuildInput":
        data = _read_json(path)
        if not isinstance(data, dict):
            raise ResolutionError(f"构建输入 {path} 顶层必须是对象")
        reqs = parse_requirements(data.get("requires", []))
        for req in reqs:
            object.__setattr__(req, "root", True)
        return cls(
            requires=reqs,
            target=Target.from_raw(data.get("target", {})),
            allow_prereleases=bool(data.get("allow_prereleases", False)),
        )


def _pkg_sort_key(pkg: Package, prov: Provide):
    return (prov.priority, prov.version, pkg.priority, pkg.version)


def _cmp_pkg_prov(a, b):
    ka = _pkg_sort_key(a[0], a[1])
    kb = _pkg_sort_key(b[0], b[1])
    if ka > kb:
        return -1
    if ka < kb:
        return 1
    ida, idb = a[0].pkg_id, b[0].pkg_id
    return -1 if ida < idb else (1 if ida > idb else 0)


def candidate_chain(catalog: Catalog, capability: str, target: Target,
                    allow_prereleases: bool) -> list[Package]:
    """按“显式优先、版本优先、名称决胜”返回稳定候选序列；替代链成环可终止。"""
    direct: dict[str, tuple[Package, Provide]] = {}
    for pkg, prov in catalog.provides.get(capability, []):
        if not target.compatible(pkg):
            continue
        if not allow_prereleases and prov.version.is_prerelease:
            continue
        if pkg.pkg_id not in direct:
            direct[pkg.pkg_id] = (pkg, prov)

    edges: dict[str, set[str]] = {}
    for pkg in catalog.packages:
        if pkg.pkg_id not in direct or not target.compatible(pkg):
            continue
        for alt_name in pkg.alternatives:
            for alt_pkg in catalog.by_name.get(alt_name, []):
                if alt_pkg.pkg_id in direct:
                    edges.setdefault(pkg.pkg_id, set()).add(alt_pkg.pkg_id)

    has_incoming = {dst for dsts in edges.values() for dst in dsts}
    seeds = sorted(
        (v for pid, v in direct.items() if pid not in has_incoming),
        key=cmp_to_key(_cmp_pkg_prov),
    )
    remaining = set(direct)
    ordered: list[Package] = []
    queue = [item[0].pkg_id for item in seeds]
    while True:
        while queue:
            pid = queue.pop(0)
            if pid not in remaining:
                continue
            remaining.discard(pid)
            ordered.append(direct[pid][0])
            succ = sorted(
                (direct[s] for s in edges.get(pid, ()) if s in remaining),
                key=cmp_to_key(_cmp_pkg_prov),
            )
            for sp, _ in succ:
                if sp.pkg_id not in queue:
                    queue.append(sp.pkg_id)
        if not remaining:
            break
        leftovers = sorted(
            (direct[pid] for pid in remaining), key=cmp_to_key(_cmp_pkg_prov)
        )
        queue = [leftovers[0][0].pkg_id]
    return ordered


STABLE_RULES = [
    "同一能力在最终方案中只能由一个包提供（single provider）。",
    "候选仅保留满足目标 os/arch、且在 allow_prereleases=false 时非预发布的包。",
    "同一能力内先比较 provide.priority（大者优先），再比较能力版本（semver 高者优先）。",
    "上一项相同时比较包级 priority，再比较包版本。",
    "仍相同时按 name@version 字典序决胜，保证跨进程结果稳定。",
    "alternatives 是显式替代偏好：无入边的直接提供者优先；成环时取剩余集合中排序最高者破环，绝不无限递归。",
    "顶级要求按 (priority 降序, capability 升序) 处理；选择包时其所有传递要求一并加入。",
    "首选包若违反版本约束、单提供者或 conflicts 互斥，则回溯尝试候选链中的下一个实现。",
    "无可选实现时回退到可选要求的空实现；所有候选失败时给出包含原因的错误。",
]


def _aggregate_reqs(reqs: list[Requirement]) -> dict[str, list[Requirement]]:
    merged: dict[str, list[Requirement]] = {}
    for req in reqs:
        merged.setdefault(req.capability, []).append(req)
    return merged


def _acceptable(pkg: Package, capability: str, constraints, target: Target,
                selected: dict[str, Package]) -> Optional[str]:
    if not target.compatible(pkg):
        return "与目标 os/arch 不兼容"
    prov = pkg.provide(capability)
    for req in constraints:
        if not req.accepts(prov.version):
            specs = ", ".join(str(c) for c in req.constraints) or "*"
            return f"不满足版本约束 {specs}（实际 {prov.version.text}）"
    owner = selected.get(capability)
    if owner is not None and owner.pkg_id != pkg.pkg_id:
        return f"能力已由 {owner.pkg_id} 提供（单提供者）"
    for cap, other in selected.items():
        if other.pkg_id != pkg.pkg_id and cap in pkg.provide_caps:
            return f"其同时提供的能力 {cap} 已由 {other.pkg_id} 占用（单提供者）"
    for other in selected.values():
        if other.pkg_id == pkg.pkg_id:
            continue
        if pkg.name in other.conflicts or other.name in pkg.conflicts:
            return f"与已选包 {other.pkg_id} 互斥"
    return None


def _solve(open_map: dict[str, list[Requirement]], catalog: Catalog,
           target: Target, allow_prereleases: bool,
           selected: dict[str, Package], decisions: list[dict],
           depth: int) -> Optional[dict[str, Package]]:
    if depth > 10000:
        raise ResolutionError("求解深度超限（疑似替代/依赖异常）")
    if not open_map:
        return dict(selected)

    capability = min(open_map, key=lambda c: (
        -max(r.priority for r in open_map[c]), c))
    reqs = open_map.pop(capability)
    constraints = [r for r in reqs if r.constraints]

    if capability in selected:
        owner = selected[capability]
        prov = owner.provide(capability)
        bad = [r for r in constraints if not r.accepts(prov.version)]
        if not bad:
            return _solve(open_map, catalog, target, allow_prereleases,
                          selected, decisions, depth + 1)
        open_map[capability] = reqs
        return None

    if all(r.optional for r in reqs):
        decisions.append({"capability": capability, "action": "skip-optional"})
        answer = _solve(open_map, catalog, target, allow_prereleases,
                        selected, decisions, depth + 1)
        if answer is not None:
            return answer
        decisions.pop()
        open_map[capability] = reqs
        return None

    chain = candidate_chain(catalog, capability, target, allow_prereleases)
    if not chain:
        decisions.append({"capability": capability, "action": "reject",
                          "package": None,
                          "reason": "没有匹配目标 os/arch 或预发布策略的候选实现"})
        if all(r.optional for r in reqs):
            decisions.pop()
        open_map[capability] = reqs
        return None

    for pkg in chain:
        overlap = sorted(pkg.provide_caps & set(open_map))
        reason = _acceptable(pkg, capability, constraints, target, selected)
        if reason is None:
            for cap in overlap:
                prov = pkg.provide(cap)
                for req in open_map[cap]:
                    if not req.accepts(prov.version):
                        specs = ", ".join(str(c) for c in req.constraints) or "*"
                        reason = f"其提供的 {cap} 不满足版本约束 {specs}（实际 {prov.version.text}）"
                        break
                if reason:
                    break
        if reason:
            decisions.append({"capability": capability, "action": "reject",
                              "package": pkg.pkg_id, "reason": reason})
            continue
        newly_satisfied = sorted(pkg.provide_caps & set(open_map))
        snapshot_open = {c: list(v) for c, v in open_map.items()}
        snapshot_selected = dict(selected)
        for cap in newly_satisfied:
            open_map.pop(cap, None)
            selected[cap] = pkg
        if capability not in newly_satisfied:
            selected[capability] = pkg
        for req in pkg.requires:
            open_map.setdefault(req.capability, []).append(req)
        decisions.append({"capability": capability, "action": "select",
                          "package": pkg.pkg_id,
                          "satisfies": [capability] + [c for c in newly_satisfied if c != capability]})
        answer = _solve(open_map, catalog, target, allow_prereleases,
                        selected, decisions, depth + 1)
        if answer is not None:
            return answer
        decisions.append({"capability": capability, "action": "backtrack",
                          "package": pkg.pkg_id})
        open_map.clear()
        open_map.update(snapshot_open)
        selected.clear()
        selected.update(snapshot_selected)

    open_map[capability] = reqs
    return None


def resolve(build: BuildInput, catalog: Catalog) -> dict:
    required = list(build.requires)
    for req in required:
        if req.capability not in catalog.provides:
            raise ResolutionError(f"能力 {req.capability!r} 没有任何包提供")
    open_map = _aggregate_reqs(required)
    decisions: list[dict] = []
    selected = _solve(open_map, catalog, build.target,
                      build.allow_prereleases, {}, decisions, 0)
    if selected is None:
        caps = sorted({d.get("capability") for d in decisions
                       if d.get("action") == "reject"})
        raise ResolutionError(
            f"无法满足能力要求: {', '.join(sorted(set(open_map)))}；"
            f"被拒能力: {', '.join(c for c in caps if c)}")

    root_caps = {r.capability for r in build.requires}
    mapping = {}
    for cap in sorted(selected):
        pkg = selected[cap]
        mapping[cap] = {
            "package": pkg.name,
            "version": pkg.version.text,
            "root_requested": cap in root_caps,
        }
    chosen_ids = sorted({p.pkg_id for p in selected.values()})
    return {
        "target": {"os": list(build.target.os), "arch": list(build.target.arch)},
        "allow_prereleases": build.allow_prereleases,
        "mapping": mapping,
        "selected_packages": chosen_ids,
        "decisions": decisions,
        "stable_rules": STABLE_RULES,
    }


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description="声明式能力 -> 实现解析器（不联网）")
    parser.add_argument("--packages", required=True, help="本地包目录 JSON")
    parser.add_argument("--build", required=True, help="本地构建输入 JSON")
    parser.add_argument("--output", help="将结果写入该文件，否则打印到 stdout")
    args = parser.parse_args(argv)
    try:
        catalog = Catalog.load(args.packages)
        build = BuildInput.load(args.build)
        result = resolve(build, catalog)
    except ResolutionError as exc:
        print(f"错误: {exc}", file=sys.stderr)
        return 2
    text = json.dumps(result, ensure_ascii=False, indent=2)
    if args.output:
        with open(args.output, "w", encoding="utf-8") as fh:
            fh.write(text + "\n")
    else:
        print(text)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
