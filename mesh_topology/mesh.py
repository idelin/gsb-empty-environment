"""半边网格核心：从三角面列表建立半边关系，支持事务化拓扑编辑。

坐标仅接受 int 或 fractions.Fraction（精确有理数），
所有退化判定均为精确算术，不使用 epsilon。
"""
from __future__ import annotations

from contextlib import contextmanager
from dataclasses import dataclass
from fractions import Fraction


def edge_key(a: int, b: int) -> tuple[int, int]:
    """无向边的规范键。"""
    return (a, b) if a <= b else (b, a)


def _check_coord(coord) -> tuple:
    p = tuple(coord)
    if len(p) not in (2, 3):
        raise ValueError(f"坐标维度须为 2 或 3，得到 {len(p)}")
    for c in p:
        if isinstance(c, bool) or not isinstance(c, (int, Fraction)):
            raise TypeError(
                f"坐标分量须为 int 或 Fraction（精确有理数），得到 {type(c).__name__}"
            )
    return p


def exact_midpoint(p: tuple, q: tuple) -> tuple:
    """精确中点（结果可能为 Fraction）。"""
    return tuple((Fraction(a) + Fraction(b)) / 2 for a, b in zip(p, q))


def triangle_is_degenerate(p0: tuple, p1: tuple, p2: tuple) -> bool:
    """精确零面积判定：叉积精确为零（整数/有理数运算，无 epsilon）。"""
    u = [b - a for a, b in zip(p0, p1)]
    v = [b - a for a, b in zip(p0, p2)]
    if len(p0) == 2:
        return u[0] * v[1] - u[1] * v[0] == 0
    cross = (
        u[1] * v[2] - u[2] * v[1],
        u[2] * v[0] - u[0] * v[2],
        u[0] * v[1] - u[1] * v[0],
    )
    return all(c == 0 for c in cross)


@dataclass
class HalfEdge:
    """一条有向半边；twin 为反向半边的键，边界或歧义时为 None。"""

    a: int
    b: int
    face: int
    next: tuple[int, int] | None = None
    prev: tuple[int, int] | None = None
    twin: tuple[int, int] | None = None

    @property
    def key(self) -> tuple[int, int]:
        return (self.a, self.b)


def _rotate_to_edge(tri, a, b):
    """把三角形循环旋转为 (x, y, z)，使 (x, y) 是含 {a, b} 的有向边。"""
    for i in range(3):
        x, y, z = tri[i], tri[(i + 1) % 3], tri[(i + 2) % 3]
        if {x, y} == {a, b}:
            return x, y, z
    raise ValueError(f"面 {tri} 不含边 ({a},{b})")


class Mesh:
    """三角网格：面列表为真源，半边/邻接结构为增量维护的派生量。"""

    def __init__(self, coords=(), faces=()):
        self.coords: list[tuple] = []
        self.faces: list[tuple | None] = []
        self._free_slots: list[int] = []
        # 增量维护的派生结构
        self._dir_edge_faces: dict[tuple[int, int], list[int]] = {}
        self._edge_faces: dict[tuple[int, int], set[int]] = {}
        self._vertex_faces: dict[int, set[int]] = {}
        self._derived = None  # (half_edges, boundary_loops, ambiguous_vertices)
        for c in coords:
            self.add_vertex(c)
        for f in faces:
            self.insert_face(*f)

    # ---------------- 顶点 ----------------
    def add_vertex(self, coord) -> int:
        p = _check_coord(coord)
        if self.coords and len(p) != len(self.coords[0]):
            raise ValueError("坐标维度不一致")
        self.coords.append(p)
        return len(self.coords) - 1

    def _check_vid(self, v: int) -> None:
        if not (0 <= v < len(self.coords)):
            raise IndexError(f"顶点 {v} 不存在")

    def remove_isolated_vertex(self, vid: int) -> None:
        """删除孤立顶点并重排索引（仅允许孤立顶点，属拓扑安全的补丁操作）。"""
        self._check_vid(vid)
        if self._vertex_faces.get(vid):
            raise ValueError(f"顶点 {vid} 不是孤立顶点，拒绝删除")
        self.coords.pop(vid)
        self.faces = [
            None if t is None else tuple(v - 1 if v > vid else v for v in t)
            for t in self.faces
        ]
        self._rebuild_maps()

    # ---------------- 面 ----------------
    def live_faces(self) -> dict[int, tuple]:
        return {i: t for i, t in enumerate(self.faces) if t is not None}

    def _face(self, fid: int) -> tuple:
        if not (0 <= fid < len(self.faces)) or self.faces[fid] is None:
            raise KeyError(f"面 {fid} 不存在")
        return self.faces[fid]

    def insert_face(self, a: int, b: int, c: int) -> int:
        for v in (a, b, c):
            self._check_vid(v)
        tri = (a, b, c)
        if self._free_slots:
            fid = self._free_slots.pop()
            self.faces[fid] = tri
        else:
            fid = len(self.faces)
            self.faces.append(tri)
        self._add_to_maps(fid, tri)
        self._derived = None
        return fid

    def delete_face(self, fid: int) -> None:
        tri = self._face(fid)
        self._remove_from_maps(fid, tri)
        self.faces[fid] = None
        self._free_slots.append(fid)
        self._derived = None

    def flip_face(self, fid: int) -> None:
        tri = self._face(fid)
        self._replace_face(fid, (tri[0], tri[2], tri[1]))

    def split_edge(self, a: int, b: int, coord=None) -> int:
        """拆分边 (a,b)：每个邻接面 (x,y,z) 变为 (x,m,z) 与 (m,y,z)。

        返回新顶点 id。coord 缺省时取精确中点（可能为 Fraction）。
        """
        self._check_vid(a)
        self._check_vid(b)
        if a == b:
            raise ValueError("不能拆分零长边（a == b）")
        fids = sorted(self._edge_faces.get(edge_key(a, b), ()))
        if not fids:
            raise KeyError(f"边 {edge_key(a, b)} 不存在于任何面")
        m = self.add_vertex(
            coord if coord is not None else exact_midpoint(self.coords[a], self.coords[b])
        )
        for fid in fids:
            x, y, z = _rotate_to_edge(self.faces[fid], a, b)
            self._replace_face(fid, (x, m, z))
            self.insert_face(m, y, z)
        return m

    # ---------------- 事务 ----------------
    @contextmanager
    def transaction(self):
        """事务：块内任意异常都会回滚，原拓扑与旧快照完整保留。"""
        snap_coords = list(self.coords)
        snap_faces = list(self.faces)
        snap_free = list(self._free_slots)
        try:
            yield self
        except Exception:
            self.coords = snap_coords
            self.faces = snap_faces
            self._free_slots = snap_free
            self._rebuild_maps()
            raise

    # ---------------- 派生结构维护 ----------------
    def _replace_face(self, fid: int, new_tri) -> None:
        old = self._face(fid)
        self._remove_from_maps(fid, old)
        self.faces[fid] = tuple(new_tri)
        self._add_to_maps(fid, self.faces[fid])
        self._derived = None

    def _add_to_maps(self, fid, tri) -> None:
        for i in range(3):
            x, y = tri[i], tri[(i + 1) % 3]
            self._dir_edge_faces.setdefault((x, y), []).append(fid)
            self._edge_faces.setdefault(edge_key(x, y), set()).add(fid)
            self._vertex_faces.setdefault(x, set()).add(fid)
            self._vertex_faces.setdefault(y, set()).add(fid)

    def _remove_from_maps(self, fid, tri) -> None:
        for i in range(3):
            x, y = tri[i], tri[(i + 1) % 3]
            lst = self._dir_edge_faces.get((x, y))
            if lst and fid in lst:
                lst.remove(fid)
                if not lst:
                    del self._dir_edge_faces[(x, y)]
            s = self._edge_faces.get(edge_key(x, y))
            if s is not None:
                s.discard(fid)
                if not s:
                    del self._edge_faces[edge_key(x, y)]
            for v in (x, y):
                sv = self._vertex_faces.get(v)
                if sv is not None:
                    sv.discard(fid)
                    if not sv:
                        del self._vertex_faces[v]

    def _rebuild_maps(self) -> None:
        self._dir_edge_faces = {}
        self._edge_faces = {}
        self._vertex_faces = {}
        for fid, tri in enumerate(self.faces):
            if tri is not None:
                self._add_to_maps(fid, tri)
        self._derived = None

    def check_consistency(self) -> list[str]:
        """把增量维护的映射与从面列表全量重建的结果对照，返回差异列表。"""
        save = (self._dir_edge_faces, self._edge_faces, self._vertex_faces)
        self._rebuild_maps()
        rebuilt = (self._dir_edge_faces, self._edge_faces, self._vertex_faces)
        self._dir_edge_faces, self._edge_faces, self._vertex_faces = save
        errors = []
        norm_dir = {k: sorted(v) for k, v in save[0].items()}
        norm_dir_r = {k: sorted(v) for k, v in rebuilt[0].items()}
        if norm_dir != norm_dir_r:
            errors.append(f"有向边映射不一致: {norm_dir} != {norm_dir_r}")
        norm_e = {k: sorted(v) for k, v in save[1].items()}
        norm_e_r = {k: sorted(v) for k, v in rebuilt[1].items()}
        if norm_e != norm_e_r:
            errors.append(f"无向边映射不一致: {norm_e} != {norm_e_r}")
        norm_v = {k: sorted(v) for k, v in save[2].items()}
        norm_v_r = {k: sorted(v) for k, v in rebuilt[2].items()}
        if norm_v != norm_v_r:
            errors.append(f"顶点-面映射不一致: {norm_v} != {norm_v_r}")
        return errors

    # ---------------- 半边与边界环 ----------------
    def _ensure_derived(self) -> None:
        if self._derived is not None:
            return
        half_edges: dict[tuple[int, int], HalfEdge] = {}
        counts: dict[tuple[int, int], int] = {}
        for fid, tri in enumerate(self.faces):
            if tri is None:
                continue
            for i in range(3):
                a, b = tri[i], tri[(i + 1) % 3]
                counts[(a, b)] = counts.get((a, b), 0) + 1
                half_edges[(a, b)] = HalfEdge(
                    a, b, fid,
                    next=(b, tri[(i + 2) % 3]),
                    prev=(tri[(i + 2) % 3], a),
                )
        # 双向引用：两侧各唯一时才互认 twin，否则视为歧义（非流形/重复面）
        for (a, b), he in half_edges.items():
            if counts.get((b, a), 0) == 1 and counts[(a, b)] == 1:
                he.twin = (b, a)
        boundary = [he for (a, b), he in half_edges.items() if counts.get((b, a), 0) == 0]
        loops, ambiguous = self._trace_boundary_loops(boundary)
        self._derived = (half_edges, loops, ambiguous)

    @staticmethod
    def _trace_boundary_loops(boundary):
        out: dict[int, list[HalfEdge]] = {}
        for he in boundary:
            out.setdefault(he.a, []).append(he)
        ambiguous = sorted(v for v, lst in out.items() if len(lst) > 1)
        loops, visited = [], set()
        for he in boundary:
            if he.key in visited:
                continue
            loop, cur = [he.a], he
            while cur.key not in visited:
                visited.add(cur.key)
                loop.append(cur.b)
                cands = [c for c in out.get(cur.b, []) if c.key not in visited]
                if not cands:
                    break
                cur = cands[0]
            loops.append(loop)
        return loops, ambiguous

    def half_edges(self) -> dict[tuple[int, int], HalfEdge]:
        self._ensure_derived()
        return self._derived[0]

    def boundary_loops(self):
        """返回 (边界环列表, 歧义边界顶点列表)。环为顶点序列，闭合环首尾相同。"""
        self._ensure_derived()
        return self._derived[1], list(self._derived[2])

    # ---------------- 只读访问 ----------------
    def undirected_edge_faces(self) -> dict[tuple[int, int], tuple[int, ...]]:
        return {k: tuple(sorted(v)) for k, v in sorted(self._edge_faces.items())}

    def vertex_faces(self) -> dict[int, tuple[int, ...]]:
        return {k: tuple(sorted(v)) for k, v in sorted(self._vertex_faces.items())}

    def directed_edge_faces(self) -> dict[tuple[int, int], tuple[int, ...]]:
        return {k: tuple(sorted(v)) for k, v in sorted(self._dir_edge_faces.items())}

    def diagnose(self):
        from .diagnostics import analyze

        return analyze(self)
