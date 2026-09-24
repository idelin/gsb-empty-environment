"""线段集合的精确平面分割。

- 坐标支持 int / fractions.Fraction / 可被 Fraction 解析的字符串，全部转为
  Fraction 做精确运算，拓扑判断只用精确符号（cross == 0 / < 0 / > 0），
  不存在 epsilon。
- 零长度线段（退化输入）单独收集为孤立点，绝不进入线段求交的分式计算，
  因此不存在除零分支。
- 输出平面图：顶点按精确坐标去重（共享端点只建一个点），子边按无序端点
  对去重并保留全部来源线段标签。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Iterable, Iterator

Number = int | Fraction | str
Point = tuple[Fraction, Fraction]


def to_point(x: Number, y: Number) -> Point:
    return (Fraction(x), Fraction(y))


def _cross(o: Point, a: Point, b: Point) -> Fraction:
    """(a - o) x (b - o) 的精确值。"""
    return (a[0] - o[0]) * (b[1] - o[1]) - (a[1] - o[1]) * (b[0] - o[0])


def _axis(p: Point, q: Point) -> int:
    """选投影主轴：x 不同用 0，否则用 1（调用方保证 p != q）。"""
    return 0 if p[0] != q[0] else 1


def _on_segment(p: Point, a: Point, b: Point) -> bool:
    """精确判断 p 是否在闭线段 ab 上（a != b）。"""
    if _cross(a, b, p) != 0:
        return False
    ax = _axis(a, b)
    lo, hi = sorted((a[ax], b[ax]))
    return lo <= p[ax] <= hi


@dataclass(frozen=True)
class Segment:
    p: Point
    q: Point
    label: str = ""

    @staticmethod
    def of(x1: Number, y1: Number, x2: Number, y2: Number, label: str = "") -> "Segment":
        return Segment(to_point(x1, y1), to_point(x2, y2), label)

    @property
    def degenerate(self) -> bool:
        return self.p == self.q


@dataclass(frozen=True)
class Edge:
    """平面图子边；v1 < v2 为顶点下标，sources 为来源线段标签（有序去重）。"""
    v1: int
    v2: int
    sources: tuple[str, ...]


@dataclass(frozen=True)
class IsolatedPoint:
    """零长度输入线段转化来的孤立点。"""
    at: Point
    label: str
    on_segments: tuple[str, ...]  # 精确经过该点的非退化线段标签


@dataclass
class PlanarGraph:
    vertices: list[Point] = field(default_factory=list)
    edges: list[Edge] = field(default_factory=list)
    isolated_points: list[IsolatedPoint] = field(default_factory=list)


def _line_intersection(a: Point, b: Point, c: Point, d: Point) -> Point:
    """非平行线段 ab 与 cd 的精确交点（调用方保证方向不平行）。"""
    d1x, d1y = b[0] - a[0], b[1] - a[1]
    d2x, d2y = d[0] - c[0], d[1] - c[1]
    denom = d1x * d2y - d1y * d2x
    # denom != 0 由调用方保证（方向不平行），不会除零。
    t = ((c[0] - a[0]) * d2y - (c[1] - a[1]) * d2x) / denom
    return (a[0] + t * d1x, a[1] + t * d1y)


def _collinear_overlap(a: Point, b: Point, c: Point, d: Point) -> tuple[Point, Point] | None:
    """共线线段的重叠部分：返回 (lo, hi)；退化为单点时 lo == hi；无重叠返回 None。"""
    ax = _axis(a, b)
    lo1, hi1 = sorted((a[ax], b[ax]))
    lo2, hi2 = sorted((c[ax], d[ax]))
    lo, hi = max(lo1, lo2), min(hi1, hi2)
    if lo > hi:
        return None
    ends = {a[ax]: a, b[ax]: b, c[ax]: c, d[ax]: d}
    return (ends[lo], ends[hi])


def _pair_split_points(s1: Segment, s2: Segment) -> Iterator[tuple[int, Point]]:
    """产出 (segment_index, split_point)：s1/s2 相互作用落在各自上的分割点。"""
    a, b, c, d = s1.p, s1.q, s2.p, s2.q
    o1, o2 = _cross(a, b, c), _cross(a, b, d)
    if o1 == 0 and o2 == 0:
        overlap = _collinear_overlap(a, b, c, d)
        if overlap is None:
            return
        lo, hi = overlap
        yield 0, lo
        yield 0, hi
        yield 1, lo
        yield 1, hi
        return
    o3, o4 = _cross(c, d, a), _cross(c, d, b)
    if o1 * o2 <= 0 and o3 * o4 <= 0:
        pt = _line_intersection(a, b, c, d)
        yield 0, pt
        yield 1, pt


def build_planar_graph(segments: Iterable[Segment]) -> PlanarGraph:
    segs = list(segments)
    normal = [s for s in segs if not s.degenerate]
    zero_len = [s for s in segs if s.degenerate]

    split: list[set[Point]] = [{s.p, s.q} for s in normal]
    for i in range(len(normal)):
        for j in range(i + 1, len(normal)):
            for idx, pt in _pair_split_points(normal[i], normal[j]):
                split[i if idx == 0 else j].add(pt)

    graph = PlanarGraph()
    vid: dict[Point, int] = {}
    edge_sources: dict[tuple[int, int], set[str]] = {}

    def vertex_id(pt: Point) -> int:
        if pt not in vid:
            vid[pt] = len(graph.vertices)
            graph.vertices.append(pt)
        return vid[pt]

    for seg, pts in zip(normal, split):
        ax = _axis(seg.p, seg.q)
        ordered = sorted(pts, key=lambda p: p[ax])
        for u, v in zip(ordered, ordered[1:]):
            if u == v:
                continue
            iu, iv = vertex_id(u), vertex_id(v)
            key = (iu, iv) if iu < iv else (iv, iu)
            edge_sources.setdefault(key, set()).add(seg.label)

    graph.edges = [
        Edge(k[0], k[1], tuple(sorted(srcs)))
        for k, srcs in sorted(edge_sources.items())
    ]

    for s in zero_len:
        on = tuple(sorted(seg.label for seg in normal if _on_segment(s.p, seg.p, seg.q)))
        graph.isolated_points.append(IsolatedPoint(s.p, s.label, on))
    return graph
