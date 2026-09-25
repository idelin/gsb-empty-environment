"""拓扑诊断：边界、非流形边、方向冲突、重复面、退化面、孤立顶点、蝴蝶结顶点。"""
from __future__ import annotations

from dataclasses import dataclass, field

from .mesh import Mesh, triangle_is_degenerate


@dataclass
class Diagnostics:
    boundary_edges: list[tuple[int, int]] = field(default_factory=list)
    boundary_loops: list[list[int]] = field(default_factory=list)
    ambiguous_boundary_vertices: list[int] = field(default_factory=list)
    # ((a,b), (fid,...))：一条边连接超过两个面
    nonmanifold_edges: list[tuple[tuple[int, int], tuple[int, ...]]] = field(default_factory=list)
    # ((a,b), f1, f2)：两个相邻面在该边上方向相同
    orientation_conflicts: list[tuple[tuple[int, int], int, int]] = field(default_factory=list)
    duplicate_face_groups: list[tuple[int, ...]] = field(default_factory=list)
    degenerate_faces: list[tuple[int, str]] = field(default_factory=list)
    isolated_vertices: list[int] = field(default_factory=list)
    # (vid, (组件1, 组件2, ...))：顶点扇不连通 => 蝴蝶结（非流形）顶点
    bowtie_vertices: list[tuple[int, tuple[tuple[int, ...], ...]]] = field(default_factory=list)

    def is_clean(self) -> bool:
        return not (
            self.boundary_edges
            or self.nonmanifold_edges
            or self.orientation_conflicts
            or self.duplicate_face_groups
            or self.degenerate_faces
            or self.isolated_vertices
            or self.bowtie_vertices
        )

    def summary(self) -> str:
        lines = ["拓扑诊断结果:"]
        lines.append(f"  边界边: {len(self.boundary_edges)} 条, 边界环: {len(self.boundary_loops)} 个")
        for loop in self.boundary_loops:
            lines.append(f"    环: {loop}")
        if self.ambiguous_boundary_vertices:
            lines.append(f"  歧义边界顶点（环走向不唯一）: {self.ambiguous_boundary_vertices}")
        lines.append(f"  非流形边（>2 面共边）: {len(self.nonmanifold_edges)} 条")
        for (a, b), fids in self.nonmanifold_edges:
            lines.append(f"    边 ({a},{b}) 连接 {len(fids)} 个面: {list(fids)}")
        lines.append(f"  面方向冲突: {len(self.orientation_conflicts)} 处")
        for (a, b), f1, f2 in self.orientation_conflicts:
            lines.append(f"    边 ({a},{b}): 面 {f1} 与面 {f2} 同向")
        lines.append(f"  重复面组: {len(self.duplicate_face_groups)} 组")
        for g in self.duplicate_face_groups:
            lines.append(f"    面 {list(g)} 顶点集相同")
        lines.append(f"  退化三角形: {len(self.degenerate_faces)} 个")
        for fid, reason in self.degenerate_faces:
            lines.append(f"    面 {fid}: {reason}")
        lines.append(f"  孤立顶点: {len(self.isolated_vertices)} 个 {self.isolated_vertices}")
        lines.append(f"  蝴蝶结（非流形）顶点: {len(self.bowtie_vertices)} 个")
        for vid, comps in self.bowtie_vertices:
            lines.append(f"    顶点 {vid}: 面扇分裂为 {len(comps)} 个组件 {[list(c) for c in comps]}")
        return "\n".join(lines)


def _dir_edges(tri) -> set[tuple[int, int]]:
    return {(tri[i], tri[(i + 1) % 3]) for i in range(3)}


def vertex_fan_components(mesh: Mesh, vid: int) -> tuple[tuple[int, ...], ...]:
    """顶点 vid 的面扇连通组件（经含 vid 的共享边相连）。"""
    faces = sorted(mesh.vertex_faces().get(vid, ()))
    parent = {f: f for f in faces}

    def find(x):
        while parent[x] != x:
            parent[x] = parent[parent[x]]
            x = parent[x]
        return x

    for key, fids in mesh.undirected_edge_faces().items():
        if vid in key and len(fids) > 1:
            fl = list(fids)
            for g in fl[1:]:
                ra, rb = find(fl[0]), find(g)
                if ra != rb:
                    parent[ra] = rb
    comps: dict[int, list[int]] = {}
    for f in faces:
        comps.setdefault(find(f), []).append(f)
    return tuple(tuple(c) for c in sorted(comps.values(), key=lambda c: c[0]))


def analyze(mesh: Mesh) -> Diagnostics:
    diag = Diagnostics()
    live = mesh.live_faces()

    # 退化三角形：索引重复（组合退化）与零面积（精确判定）分别标注
    for fid, tri in sorted(live.items()):
        if len(set(tri)) < 3:
            diag.degenerate_faces.append((fid, f"顶点索引重复 {tri}"))
        elif triangle_is_degenerate(*(mesh.coords[v] for v in tri)):
            diag.degenerate_faces.append((fid, "零面积（精确叉积为零）"))

    # 重复面：顶点集相同即视为重复，不计入正常相邻
    groups: dict[tuple[int, ...], list[int]] = {}
    for fid, tri in live.items():
        groups.setdefault(tuple(sorted(tri)), []).append(fid)
    diag.duplicate_face_groups = sorted(
        tuple(sorted(g)) for g in groups.values() if len(g) > 1
    )

    # 非流形边与方向冲突
    dir_sets = {fid: _dir_edges(tri) for fid, tri in live.items()}
    for (a, b), fids in mesh.undirected_edge_faces().items():
        if len(fids) > 2:
            diag.nonmanifold_edges.append(((a, b), fids))
        elif len(fids) == 2:
            f1, f2 = fids
            if ((a, b) in dir_sets[f1]) == ((a, b) in dir_sets[f2]):
                diag.orientation_conflicts.append(((a, b), f1, f2))

    # 边界边与边界环
    directed = mesh.directed_edge_faces()
    diag.boundary_edges = sorted((a, b) for (a, b) in directed if (b, a) not in directed)
    diag.boundary_loops, diag.ambiguous_boundary_vertices = mesh.boundary_loops()

    # 孤立顶点
    vf = mesh.vertex_faces()
    diag.isolated_vertices = [v for v in range(len(mesh.coords)) if v not in vf]

    # 蝴蝶结顶点：面扇组件数 > 1
    for vid in vf:
        comps = vertex_fan_components(mesh, vid)
        if len(comps) > 1:
            diag.bowtie_vertices.append((vid, comps))

    return diag
