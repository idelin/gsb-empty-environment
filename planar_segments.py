"""线段集合精确求交并分割为平面图（仅标准库，Python 3.11+）。

所有坐标统一为 fractions.Fraction，拓扑判断全部基于精确的有理数
符号判定（叉积符号），不使用任何 epsilon 容差。

退化情形定义（确定性行为）：
- 零长度段（两端点相同）：不参与求交与分割，不会进入任何除零分支；
  构建完成后单独分类为 vertex（落在已有顶点上）/ on_edge（落在某条
  子边内部）/ isolated（孤立点）。
- 平行不共线：无交。
- 共线重叠：重叠区间（可能退化为单点）的两个端点同时加入两条来源
  段的分割点集合；重叠部分的原子子边携带全部来源。
- 端点触碰 / T 型接触：交点同时加入两条段的分割点集合。
- 共享端点：全局点表按坐标去重，同一坐标只建一个顶点。
"""

from __future__ import annotations

import json
import sys
from dataclasses import dataclass, field
from fractions import Fraction

Point = tuple[Fraction, Fraction]


def to_fraction(value) -> Fraction:
    """接受 int / Fraction / "p/q" 或 "p" 形式的字符串；拒绝浮点等模糊输入。"""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError(f"不支持的坐标类型: {value!r}")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        text = value.strip()
        if "/" in text:
            num, den = text.split("/", 1)
            return Fraction(int(num), int(den))
        return Fraction(int(text))
    raise TypeError(f"不支持的坐标类型: {value!r}")


def to_point(pair) -> Point:
    return (to_fraction(pair[0]), to_fraction(pair[1]))


def cross(o: Point, a: Point, b: Point) -> Fraction:
    """向量 (a-o) x (b-o) 的精确叉积。"""
    return (a[0] - o[0]) * (b[1] - o[1]) - (a[1] - o[1]) * (b[0] - o[0])


def dominant_axis(p: Point, q: Point) -> int:
    """非退化段的主导轴：x 方向有变化取 0，否则取 1（段非竖直时用 x 排序）。"""
    return 0 if q[0] != p[0] else 1


def _lift(a: Point, b: Point, axis: int, v: Fraction) -> Point:
    """共线情形下，由主导轴坐标 v 精确还原直线上的点。"""
    t = (v - a[axis]) / (b[axis] - a[axis])
    other = 1 - axis
    w = a[other] + t * (b[other] - a[other])
    return (v, w) if axis == 0 else (w, v)


def segment_intersection(s1: tuple[Point, Point], s2: tuple[Point, Point]):
    """精确求两非退化段的交。

    返回 None（无交）/ ("point", p) / ("overlap", p, q)（p != q 的共线重叠区间）。
    """
    a, b = s1
    c, d = s2
    d1x, d1y = b[0] - a[0], b[1] - a[1]
    d2x, d2y = d[0] - c[0], d[1] - c[1]
    denom = d1x * d2y - d1y * d2x
    if denom != 0:
        # 非平行：解参数方程，参数均在 [0,1] 内才有交点。
        ex, ey = c[0] - a[0], c[1] - a[1]
        t = (ex * d2y - ey * d2x) / denom
        u = (ex * d1y - ey * d1x) / denom
        if Fraction(0) <= t <= 1 and Fraction(0) <= u <= 1:
            return ("point", (a[0] + t * d1x, a[1] + t * d1y))
        return None
    # 平行：先判共线。
    if cross(a, b, c) != 0:
        return None
    axis = dominant_axis(a, b)
    lo1, hi1 = sorted((a[axis], b[axis]))
    lo2, hi2 = sorted((c[axis], d[axis]))
    lo, hi = max(lo1, lo2), min(hi1, hi2)
    if lo > hi:
        return None
    if lo == hi:
        return ("point", _lift(a, b, axis, lo))
    return ("overlap", _lift(a, b, axis, lo), _lift(a, b, axis, hi))


def point_on_segment(p: Point, a: Point, b: Point) -> bool:
    """精确判断 p 是否落在非退化闭线段 ab 上。"""
    if cross(a, b, p) != 0:
        return False
    axis = dominant_axis(a, b)
    lo, hi = sorted((a[axis], b[axis]))
    return lo <= p[axis] <= hi


@dataclass(frozen=True)
class Edge:
    id: int
    u: int  # 顶点 id
    v: int  # 顶点 id
    sources: tuple[int, ...]  # 来源输入段下标，升序，可能多个


@dataclass(frozen=True)
class ZeroLengthPoint:
    source: int
    point: Point
    kind: str  # "vertex" | "on_edge" | "isolated"
    vertex: int | None = None
    edge: int | None = None


@dataclass
class PlanarGraph:
    vertices: list[Point]
    edges: list[Edge]
    zero_length: list[ZeroLengthPoint] = field(default_factory=list)


def build_planar_graph(raw_segments) -> PlanarGraph:
    """输入 [[(x1,y1),(x2,y2)], ...]，输出按交点分割后的平面图。

    每条原子子边记录全部来源段下标；同一坐标只建一个顶点。
    """
    normal: list[tuple[int, Point, Point]] = []
    degenerate: list[tuple[int, Point]] = []
    for i, seg in enumerate(raw_segments):
        p, q = to_point(seg[0]), to_point(seg[1])
        if p == q:
            degenerate.append((i, p))  # 零长度段：单独收集，不进除零分支
        else:
            normal.append((i, p, q))

    # 1) 两两精确求交，收集每段的分割点。
    split_points: dict[int, set[Point]] = {i: {p, q} for i, p, q in normal}
    for x in range(len(normal)):
        for y in range(x + 1, len(normal)):
            i, a, b = normal[x]
            j, c, d = normal[y]
            hit = segment_intersection((a, b), (c, d))
            if hit is None:
                continue
            if hit[0] == "point":
                split_points[i].add(hit[1])
                split_points[j].add(hit[1])
            else:  # 共线重叠：重叠区间两端点加入双方分割集合
                split_points[i].update((hit[1], hit[2]))
                split_points[j].update((hit[1], hit[2]))

    # 2) 沿主导轴排序分割点，切出原子子边；全局点表去重。
    point_id: dict[Point, int] = {}
    vertices: list[Point] = []

    def pid(p: Point) -> int:
        if p not in point_id:
            point_id[p] = len(vertices)
            vertices.append(p)
        return point_id[p]

    edge_sources: dict[tuple[int, int], set[int]] = {}
    for i, a, b in normal:
        axis = dominant_axis(a, b)
        pts = sorted(split_points[i], key=lambda p: (p[axis], p[1 - axis]))
        for u, v in zip(pts, pts[1:]):
            if u == v:
                continue
            ui, vi = pid(u), pid(v)
            key = (ui, vi) if ui < vi else (vi, ui)
            edge_sources.setdefault(key, set()).add(i)

    edges = [
        Edge(id=k, u=u, v=v, sources=tuple(sorted(srcs)))
        for k, ((u, v), srcs) in enumerate(sorted(edge_sources.items()))
    ]

    # 3) 零长度段分类（不修改图结构）。
    zero_length: list[ZeroLengthPoint] = []
    for src, p in degenerate:
        if p in point_id:
            zero_length.append(ZeroLengthPoint(src, p, "vertex", vertex=point_id[p]))
            continue
        on = next((e for e in edges
                   if point_on_segment(p, vertices[e.u], vertices[e.v])), None)
        if on is not None:
            zero_length.append(ZeroLengthPoint(src, p, "on_edge", edge=on.id))
        else:
            zero_length.append(ZeroLengthPoint(src, p, "isolated"))

    return PlanarGraph(vertices=vertices, edges=edges, zero_length=zero_length)


def _fmt_num(v: Fraction):
    return int(v) if v.denominator == 1 else f"{v.numerator}/{v.denominator}"


def graph_to_dict(graph: PlanarGraph) -> dict:
    return {
        "vertices": [
            {"id": i, "x": _fmt_num(p[0]), "y": _fmt_num(p[1])}
            for i, p in enumerate(graph.vertices)
        ],
        "edges": [
            {"id": e.id, "u": e.u, "v": e.v, "sources": list(e.sources)}
            for e in graph.edges
        ],
        "zero_length": [
            {
                "source": z.source,
                "point": [_fmt_num(z.point[0]), _fmt_num(z.point[1])],
                "kind": z.kind,
                **({"vertex": z.vertex} if z.vertex is not None else {}),
                **({"edge": z.edge} if z.edge is not None else {}),
            }
            for z in graph.zero_length
        ],
    }


def main(argv: list[str]) -> int:
    """用法: python planar_segments.py input.json

    输入 JSON: [[[x1,y1],[x2,y2]], ...]，坐标为整数或 "p/q" 字符串。
    """
    if len(argv) != 2:
        print(main.__doc__, file=sys.stderr)
        return 2
    with open(argv[1], "r", encoding="utf-8") as fh:
        raw = json.load(fh)
    graph = build_planar_graph(raw)
    print(json.dumps(graph_to_dict(graph), ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
