"""本地 JSON 输入的数据模型与校验。

packages.json:
{
  "packages": {
    "web": {"versions": ["1.4.0", "1.5.0-rc.1", "1.5.0"]}
  }
}

build.json:
{
  "nodes": [
    {
      "id": "web-1.4.0", "package": "web", "version": "1.4.0",
      "digest": "sha256:...", "rebuild_cost": 100,
      "deps": [{"node": "core-2.0.0", "constraint": "^2.0.0"}]
    }
  ],
  "failed": ["app-3.0.0"],
  "cache": [{"node": "app-3.0.0", "digest": "sha256:...", "restore_cost": 20}],
  "targets": ["app-3.0.0"]
}
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path

from .version import Version, parse_constraints, satisfies_all, VersionError


class ModelError(ValueError):
    """输入数据不合法。"""


@dataclass(frozen=True)
class Dependency:
    node_id: str
    constraint_text: str = ""

    @property
    def constraints(self):
        return parse_constraints(self.constraint_text)


@dataclass
class Node:
    id: str
    package: str
    version: Version
    digest: str
    rebuild_cost: int
    deps: tuple[Dependency, ...] = ()
    restore_cost: int | None = None  # 仅当 cache 摘要匹配时填充


@dataclass
class Problem:
    nodes: dict[str, Node]
    failed: frozenset[str]
    cache: dict[str, tuple[str, int]]  # node_id -> (digest, restore_cost)
    targets: tuple[str, ...]

    # ---------------------------------------------------------- 加载
    @staticmethod
    def from_raw(packages_raw: dict, build_raw: dict) -> "Problem":
        package_versions = _load_packages(packages_raw)
        nodes = _load_nodes(build_raw, package_versions)
        failed = frozenset(_require_string_list(build_raw, "failed", default=()))
        cache = _load_cache(build_raw)
        targets = tuple(_require_string_list(build_raw, "targets", default=()))

        unknown_failed = failed - nodes.keys()
        if unknown_failed:
            raise ModelError(f"failed 引用了不存在的节点：{sorted(unknown_failed)}")
        unknown_cache = cache.keys() - nodes.keys()
        if unknown_cache:
            raise ModelError(f"cache 引用了不存在的节点：{sorted(unknown_cache)}")
        unknown_targets = set(targets) - nodes.keys()
        if unknown_targets:
            raise ModelError(f"targets 引用了不存在的节点：{sorted(unknown_targets)}")
        if not targets:
            raise ModelError("targets 不能为空")
        if len(set(targets)) != len(targets):
            raise ModelError("targets 中存在重复节点")

        # 摘要匹配：匹配才可恢复；不匹配视为不可恢复（直接丢弃缓存项）
        for node in nodes.values():
            entry = cache.get(node.id)
            if entry is not None and entry[0] == node.digest:
                node.restore_cost = entry[1]

        _assert_dag(nodes)
        return Problem(nodes=nodes, failed=failed, cache=cache, targets=targets)

    @staticmethod
    def from_files(packages_path: str | Path, build_path: str) -> "Problem":
        packages_raw = _read_json(packages_path)
        build_raw = _read_json(build_path)
        return Problem.from_raw(packages_raw, build_raw)


# ---------------------------------------------------------------- 辅助


def _read_json(path: str | Path) -> dict:
    try:
        with open(path, "r", encoding="utf-8") as handle:
            data = json.load(handle)
    except FileNotFoundError as exc:
        raise ModelError(f"找不到文件：{path}") from exc
    except json.JSONDecodeError as exc:
        raise ModelError(f"JSON 解析失败（{path}）：{exc}") from exc
    if not isinstance(data, dict):
        raise ModelError(f"顶层必须是 JSON 对象：{path}")
    return data


def _load_packages(raw: dict) -> dict[str, dict[Version, str]]:
    section = raw.get("packages", {})
    if not isinstance(section, dict):
        raise ModelError("packages 必须是对象")
    result: dict[str, dict[Version, str]] = {}
    for name, spec in section.items():
        if not isinstance(spec, dict) or not isinstance(spec.get("versions"), list):
            raise ModelError(f"包 {name} 需要 {'versions': [...]} 列表")
        versions: dict[Version, str] = {}
        for text in spec["versions"]:
            if not isinstance(text, str):
                raise ModelError(f"包 {name} 的版本必须是字符串：{text!r}")
            version = Version.parse(text)
            if version in versions:
                raise ModelError(f"包 {name} 存在重复版本 {text}")
            versions[version] = text
        if not versions:
            raise ModelError(f"包 {name} 至少需要一个版本")
        result[name] = versions
    if not result:
        raise ModelError("packages 不能为空（不联网，所有包必须来自本地 JSON）")
    return result


def _load_nodes(raw: dict, package_versions) -> dict[str, Node]:
    section = raw.get("nodes", [])
    if not isinstance(section, list) or not section:
        raise ModelError("nodes 必须是非空列表")
    nodes: dict[str, Node] = {}
    for index, item in enumerate(section):
        if not isinstance(item, dict):
            raise ModelError(f"nodes[{index}] 必须是对象")
        node_id = _need_str(item, "id", index)
        if node_id in nodes:
            raise ModelError(f"节点 id 重复：{node_id}")
        package = _need_str(item, "package", index, node_id)
        if package not in package_versions:
            raise ModelError(f"节点 {node_id} 的包 {package} 不在本地 packages.json 中")
        version_text = _need_str(item, "version", index, node_id)
        try:
            version = Version.parse(version_text)
        except VersionError as exc:
            raise ModelError(f"节点 {node_id} 版本非法：{exc}") from exc
        if version not in package_versions[package]:
            raise ModelError(
                f"节点 {node_id} 使用了本地索引中不存在的版本 "
                f"{package}@{version_text}（不联网查包）"
            )
        digest = _need_str(item, "digest", index, node_id)
        rebuild_cost = _need_nonneg_int(item, "rebuild_cost", index, node_id)
        deps = tuple(
            _load_dep(dep_raw, index, dep_idx, nodes, package_versions)
            for dep_idx, dep_raw in enumerate(item.get("deps", []))
        )
        dep_ids = [d.node_id for d in deps]
        if len(set(dep_ids)) != len(dep_ids):
            raise ModelError(f"节点 {node_id} 的 deps 存在重复")
        nodes[node_id] = Node(
            id=node_id,
            package=package,
            version=version,
            digest=digest,
            rebuild_cost=rebuild_cost,
            deps=deps,
        )
    return nodes


def _load_dep(dep_raw, node_index, dep_index, nodes_so_far, package_versions) -> Dependency:
    if not isinstance(dep_raw, dict):
        raise ModelError(f"nodes[{node_index}].deps[{dep_index}] 必须是对象")
    dep_id = _need_str(dep_raw, "node", node_index)
    if dep_id not in nodes_so_far:
        raise ModelError(f"依赖节点未在前面定义：{dep_id}（节点按拓扑顺序给出）")
    dep_node = nodes_so_far[dep_id]
    constraint_text = dep_raw.get("constraint", "")
    if constraint_text is not None and not isinstance(constraint_text, str):
        raise ModelError(f"依赖 {dep_id} 的 constraint 必须是字符串")
    constraints = parse_constraints(constraint_text or "")
    try:
        if not satisfies_all(dep_node.version, constraints):
            raise ModelError(
                f"依赖 {dep_id}（{dep_node.package}@{dep_node.version}）"
                f"不满足约束 {constraint_text!r}"
            )
    except VersionError as exc:
        raise ModelError(f"依赖 {dep_id} 的约束非法：{exc}") from exc
    return Dependency(node_id=dep_id, constraint_text=constraint_text or "")


def _load_cache(raw: dict) -> dict[str, tuple[str, int]]:
    section = raw.get("cache", [])
    if not isinstance(section, list):
        raise ModelError("cache 必须是列表")
    result: dict[str, tuple[str, int]] = {}
    for index, item in enumerate(section):
        if not isinstance(item, dict):
            raise ModelError(f"cache[{index}] 必须是对象")
        node_id = _need_str(item, "node", index)
        digest = _need_str(item, "digest", index, node_id)
        restore_cost = item.get("restore_cost", 0)
        if not isinstance(restore_cost, int) or isinstance(restore_cost, bool) or restore_cost < 0:
            raise ModelError(f"cache[{index}].restore_cost 必须是非负整数")
        if node_id in result:
            raise ModelError(f"cache 中节点重复：{node_id}")
        result[node_id] = (digest, restore_cost)
    return result


def _require_string_list(raw, key, default=()):
    value = raw.get(key, list(default))
    if not isinstance(value, list) or not all(isinstance(v, str) for v in value):
        raise ModelError(f"{key} 必须是字符串列表")
    return value


def _need_str(item, key, index, node_id=None):
    value = item.get(key)
    if not isinstance(value, str) or not value:
        where = f"节点 {node_id}" if node_id else f"nodes[{index}]"
        raise ModelError(f"{where} 的 {key} 必须是非空字符串")
    return value


def _need_nonneg_int(item, key, index, node_id):
    value = item.get(key)
    if not isinstance(value, int) or isinstance(value, bool) or value < 0:
        raise ModelError(f"节点 {node_id} 的 {key} 必须是非负整数")
    return value


def _assert_dag(nodes: dict[str, Node]) -> None:
    # Kahn 算法检测环（依赖图）
    indegree = {nid: 0 for nid in nodes}
    dependents: dict[str, list[str]] = {nid: [] for nid in nodes}
    for nid, node in nodes.items():
        for dep in node.deps:
            indegree[nid] += 1
            dependents[dep.node_id].append(nid)
    queue = [nid for nid, deg in indegree.items() if deg == 0]
    seen = 0
    while queue:
        current = queue.pop()
        seen += 1
        for dependent in dependents[current]:
            indegree[dependent] -= 1
            if indegree[dependent] == 0:
                queue.append(dependent)
    if seen != len(nodes):
        cyclic = sorted(nid for nid, deg in indegree.items() if deg > 0)
        raise ModelError(f"依赖图中存在环，涉及节点：{cyclic}")
