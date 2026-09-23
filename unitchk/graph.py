"""版本化转换图：增删单位定义/换算边时维护版本与快照。

- 边是精确仿射映射 x -> a*x + b（比例单位 b = 0，比例与偏移分开）。
- 每次增删操作版本号 +1 并记录日志与快照，可查询历史版本。
- 新增边若与已有路径构成矛盾环（复合映射 ≠ 恒等），拒绝并给出环证据。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .core import UnitKind


@dataclass(frozen=True)
class AffineMap:
    """精确仿射映射 x -> a*x + b（有理数精确计算）。"""
    a: Fraction = Fraction(1)
    b: Fraction = Fraction(0)

    def __init__(self, a=1, b=0):
        object.__setattr__(self, "a", Fraction(a))
        object.__setattr__(self, "b", Fraction(b))

    def then(self, other: "AffineMap") -> "AffineMap":
        """先 self 后 other：other ∘ self。"""
        return AffineMap(other.a * self.a, other.a * self.b + other.b)

    def inverse(self) -> "AffineMap":
        return AffineMap(1 / self.a, -self.b / self.a)

    def is_identity(self) -> bool:
        return self.a == 1 and self.b == 0

    def __call__(self, x):
        return self.a * x + self.b


@dataclass(frozen=True)
class Edge:
    src: str
    dst: str
    amap: AffineMap
    label: str = ""


@dataclass
class Contradiction:
    """矛盾环证据：沿环复合得到的映射不是恒等映射。"""
    cycle: list          # Edge 列表，首尾相接成环
    composed: AffineMap  # 环上复合映射（≠ 恒等）
    detail: str


def conversion_between(u, v) -> AffineMap:
    """由两个单位定义推导换算映射：x_v = (u.scale*x + u.offset - v.offset)/v.scale。"""
    return AffineMap(u.scale / v.scale, (u.offset - v.offset) / v.scale)


class ConversionGraph:
    def __init__(self, registry=None):
        self.registry = registry
        self._adj: dict[str, list[Edge]] = {}
        self.version = 0
        self.journal: list[tuple[int, str]] = [(0, "init")]
        self._snapshots: dict[int, dict] = {0: {}}

    # ---- 版本维护 ----
    def _bump(self, op: str) -> None:
        self.version += 1
        self.journal.append((self.version, op))
        self._snapshots[self.version] = {
            k: tuple(v) for k, v in self._adj.items()}

    def _check_units(self, src: str, dst: str, amap: AffineMap) -> None:
        if self.registry is None:
            return
        u1 = self.registry.units.get(src)
        u2 = self.registry.units.get(dst)
        if u1 is None or u2 is None:
            raise KeyError(f"单位未在注册表中定义: {src} 或 {dst}")
        if u1.dim != u2.dim:
            raise ValueError(
                f"量纲不同，不能建立换算: {src}{u1.dim} vs {dst}{u2.dim}")
        if (u1.kind is UnitKind.ABSOLUTE) != (u2.kind is UnitKind.ABSOLUTE):
            raise ValueError(
                f"比例与偏移单位分开：{src}({u1.kind.value}) 与 "
                f"{dst}({u2.kind.value}) 之间不能建立换算")
        if u1.kind is UnitKind.RATIO and amap.b != 0:
            raise ValueError("比例单位之间的换算不允许带偏移")

    # ---- 增删操作（每次操作版本 +1）----
    def add_unit(self, name: str) -> None:
        if name in self._adj:
            raise ValueError(f"单位已存在: {name}")
        self._adj[name] = []
        self._bump(f"add-unit {name}")

    def remove_unit(self, name: str) -> None:
        if name not in self._adj:
            raise KeyError(f"单位不存在: {name}")
        del self._adj[name]
        for edges in self._adj.values():
            edges[:] = [e for e in edges if e.dst != name]
        self._bump(f"remove-unit {name}")

    def add_conversion(self, src: str, dst: str, amap: AffineMap, label: str = ""):
        """新增换算边。若构成矛盾环则拒绝并返回 Contradiction 证据。"""
        if src not in self._adj or dst not in self._adj:
            raise KeyError("请先 add_unit 再建立换算")
        self._check_units(src, dst, amap)
        new_edge = Edge(src, dst, amap, label)
        if src == dst:
            if not amap.is_identity():
                return Contradiction([new_edge], amap,
                                     f"自环 {src} 的复合映射 {amap} 不是恒等")
            return new_edge
        found = self._bfs(dst, src, self._adj)
        if found is not None:
            path, m = found
            composed = amap.then(m)  # src --新边--> dst --已有路径--> src
            if not composed.is_identity():
                cycle = [new_edge] + path
                return Contradiction(cycle, composed,
                                     f"环上复合映射 {composed} 不是恒等映射，"
                                     f"新边 {src}->{dst} 与已有路径矛盾")
            return new_edge  # 与已有换算一致的重复声明，幂等
        self._adj[src].append(new_edge)
        self._adj[dst].append(Edge(dst, src, amap.inverse(), label + " (逆)"))
        self._bump(f"add-conversion {src}->{dst}")
        return new_edge

    def remove_conversion(self, src: str, dst: str) -> None:
        before = sum(len(v) for v in self._adj.values())
        self._adj[src] = [e for e in self._adj.get(src, []) if e.dst != dst]
        self._adj[dst] = [e for e in self._adj.get(dst, []) if e.dst != src]
        after = sum(len(v) for v in self._adj.values())
        if before == after:
            raise KeyError(f"换算边不存在: {src}->{dst}")
        self._bump(f"remove-conversion {src}->{dst}")

    # ---- 查询 ----
    @staticmethod
    def _bfs(src, dst, adj):
        if src == dst:
            return [], AffineMap()
        seen = {src: None}
        queue = [src]
        while queue:
            cur = queue.pop(0)
            for e in adj.get(cur, ()):
                if e.dst in seen:
                    continue
                seen[e.dst] = (cur, e)
                if e.dst == dst:
                    path = []
                    node = dst
                    while seen[node] is not None:
                        prev, edge = seen[node]
                        path.append(edge)
                        node = prev
                    path.reverse()
                    total = AffineMap()
                    for edge in path:
                        total = total.then(edge.amap)
                    return path, total
                queue.append(e.dst)
        return None

    def find(self, src: str, dst: str, version=None):
        """查询换算映射与路径；version 指定时查询历史快照。"""
        adj = self._adj if version is None else self._snapshots.get(version)
        if adj is None:
            raise KeyError(f"无此版本: {version}")
        if src not in adj or dst not in adj:
            return None
        return self._bfs(src, dst, adj)
