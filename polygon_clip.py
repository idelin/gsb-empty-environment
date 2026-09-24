"""凸裁剪窗裁剪简单多边形（Python 3.11，仅标准库，精确有理数运算）。

规则定义
========
- 坐标只接受 int / Fraction / 可被 Fraction 解析的 str，拒绝 float，
  全部运算使用 fractions.Fraction 精确完成，拓扑判断不依赖任何 epsilon。
- 多边形与裁剪窗都视为闭集（包含边界），裁剪结果同样是闭集：
  恰好贴在裁剪边界上的边、线、点都不会被丢弃。
- 被裁多边形必须是简单多边形：非相邻边不得有任何交点（含触碰），
  相邻边不得重叠或折返（尖刺），面积不得为零，否则抛出 ValueError。
- 裁剪窗必须是面积非零的凸多边形（允许相邻边共线），内部统一转为逆时针，
  否则抛出 ValueError。
- 输出为若干 Region（可能多个连通区域）：
  * 非退化区域统一为逆时针、起点为最小顶点的规范形；
  * 退化区域（面积为零，即裁剪结果只剩一条线段或一个点）degenerate=True。
- Region.edge_sources[i] 是边 vertices[i] -> vertices[(i+1) % n] 的来源：
  ("subject", i) 表示来自被裁多边形第 i 条边，
  ("clip", j)    表示来自裁剪窗第 j 条边（均按输入时的顶点编号，
  第 i 条边指 vertices[i] -> vertices[(i+1) % n]）。
- 幂等性：把输出的非退化区域用同一窗口再次裁剪，得到完全相同的几何
  （顶点规范形一致；来源信息按新的输入重新标记）。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Tuple, Union

__all__ = ["Region", "ClipResult", "clip_polygon", "polygon_area"]

Number = Union[int, Fraction, str]
Point = Tuple[Fraction, Fraction]
EdgeSource = Tuple[str, int]  # ("subject", i) 或 ("clip", j)


# ---------------------------------------------------------------------------
# 坐标解析
# ---------------------------------------------------------------------------

def _to_fraction(value) -> Fraction:
    if isinstance(value, bool):
        raise TypeError("boolean 不是合法坐标")
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        try:
            return Fraction(value)
        except ValueError as exc:
            raise ValueError(f"无法解析为有理数: {value!r}") from exc
    raise TypeError(f"不支持的坐标类型: {type(value).__name__}（仅接受 int/Fraction/str）")


def _to_point(p) -> Point:
    try:
        x, y = p
    except (TypeError, ValueError) as exc:
        raise ValueError(f"非法点: {p!r}") from exc
    return (_to_fraction(x), _to_fraction(y))


# ---------------------------------------------------------------------------
# 精确几何原语
# ---------------------------------------------------------------------------

def _cross(o: Point, a: Point, b: Point) -> Fraction:
    return (a[0] - o[0]) * (b[1] - o[1]) - (a[1] - o[1]) * (b[0] - o[0])


def _on_segment(a: Point, b: Point, p: Point) -> bool:
    return (
        _cross(a, b, p) == 0
        and min(a[0], b[0]) <= p[0] <= max(a[0], b[0])
        and min(a[1], b[1]) <= p[1] <= max(a[1], b[1])
    )


def _segments_intersect(a: Point, b: Point, c: Point, d: Point) -> bool:
    d1 = _cross(c, d, a)
    d2 = _cross(c, d, b)
    d3 = _cross(a, b, c)
    d4 = _cross(a, b, d)
    if ((d1 > 0 and d2 < 0) or (d1 < 0 and d2 > 0)) and (
        (d3 > 0 and d4 < 0) or (d3 < 0 and d4 > 0)
    ):
        return True
    return (
        (d1 == 0 and _on_segment(c, d, a))
        or (d2 == 0 and _on_segment(c, d, b))
        or (d3 == 0 and _on_segment(a, b, c))
        or (d4 == 0 and _on_segment(a, b, d))
    )


def _line_intersection(s: Point, e: Point, a: Point, b: Point) -> Point:
    fs = _cross(a, b, s)
    fe = _cross(a, b, e)
    t = fs / (fs - fe)
    return (s[0] + (e[0] - s[0]) * t, s[1] + (e[1] - s[1]) * t)


def _param(a: Point, b: Point, p: Point) -> Fraction:
    if a[0] != b[0]:
        return (p[0] - a[0]) / (b[0] - a[0])
    return (p[1] - a[1]) / (b[1] - a[1])


def _signed_area2(verts) -> Fraction:
    s = Fraction(0)
    n = len(verts)
    for i in range(n):
        x1, y1 = verts[i]
        x2, y2 = verts[(i + 1) % n]
        s += x1 * y2 - x2 * y1
    return s


def polygon_area(points) -> Fraction:
    """任意顶点序列的精确（绝对）面积。"""
    pts = [_to_point(p) for p in points]
    if len(pts) < 3:
        return Fraction(0)
    return abs(_signed_area2(pts)) / 2


# ---------------------------------------------------------------------------
# 输入校验
# ---------------------------------------------------------------------------

def _validate_simple_polygon(points, *, name: str):
    pts = [_to_point(p) for p in points]
    n = len(pts)
    if n < 3:
        raise ValueError(f"{name} 至少需要 3 个顶点，得到 {n} 个")
    if len(set(pts)) != n:
        raise ValueError(f"{name} 存在重复顶点")
    for k in range(n):
        prev, s, nxt = pts[k - 1], pts[k], pts[(k + 1) % n]
        if _cross(prev, s, nxt) == 0:
            dot = (s[0] - prev[0]) * (nxt[0] - s[0]) + (s[1] - prev[1]) * (nxt[1] - s[1])
            if dot <= 0:
                raise ValueError(f"{name} 在顶点 {k} 处相邻边重叠或折返（尖刺）")
    for i in range(n):
        a, b = pts[i], pts[(i + 1) % n]
        for j in range(i + 1, n):
            if j == i + 1 or (i == 0 and j == n - 1):
                continue
            c, d = pts[j], pts[(j + 1) % n]
            if _segments_intersect(a, b, c, d):
                raise ValueError(f"{name} 自交：边 {i} 与边 {j} 相交或相触")
    if _signed_area2(pts) == 0:
        raise ValueError(f"{name} 面积为零")
    return pts


def _validate_window(points):
    pts = _validate_simple_polygon(points, name="裁剪窗")
    n = len(pts)
    sign = 0
    for k in range(n):
        c = _cross(pts[k - 1], pts[k], pts[(k + 1) % n])
        if c != 0:
            s = 1 if c > 0 else -1
            if sign == 0:
                sign = s
            elif s != sign:
                raise ValueError("裁剪窗不是凸多边形")
    return pts, sign


# ---------------------------------------------------------------------------
# Sutherland-Hodgman（闭集语义：cross >= 0 即在左闭半平面内）
# ---------------------------------------------------------------------------

def _clip_halfplane(walk, a: Point, b: Point, clip_edge_index: int):
    """用有向边 a->b 的左闭半平面裁剪 walk。

    walk 元素为 [point, intag]，intag 表示终止于该顶点的边的来源。
    """
    out = []
    m = len(walk)
    for i in range(m):
        s, stag = walk[i - 1]
        e, etag = walk[i]
        s_in = _cross(a, b, s) >= 0
        e_in = _cross(a, b, e) >= 0
        if e_in:
            if not s_in:
                out.append([_line_intersection(s, e, a, b), ("clip", clip_edge_index)])
            out.append([e, etag])
        elif s_in:
            out.append([_line_intersection(s, e, a, b), etag])
    deduped = []
    for item in out:
        if deduped and deduped[-1][0] == item[0]:
            continue
        deduped.append(item)
    while len(deduped) > 1 and deduped[0][0] == deduped[-1][0]:
        deduped.pop()
    return deduped


# ---------------------------------------------------------------------------
# 弱简单闭走廓 -> 若干简单环
# ---------------------------------------------------------------------------

def _refine_walk(walk):
    """在“顶点落在非邻接边内部”处剖分边，使重合只表现为整边重合。"""
    m = len(walk)
    if m < 2:
        return walk
    pts = [p for p, _ in walk]
    splits = [[] for _ in range(m)]
    for i in range(m):
        a = pts[i]
        b = pts[(i + 1) % m]
        if a == b:
            continue
        for k in range(m):
            if k == i or k == (i + 1) % m:
                continue
            p = pts[k]
            if _on_segment(a, b, p):
                splits[i].append((_param(a, b, p), p))
    if not any(splits):
        return walk
    refined = []
    for i in range(m):
        refined.append([pts[i], walk[i][1]])
        edge_tag = walk[(i + 1) % m][1]
        for _, p in sorted(splits[i]):
            refined.append([p, edge_tag])
    return refined


def _split_loops(walk):
    """在重复顶点处把闭走廓拆成顶点互不相同的环。"""
    loops = []
    rest = [[p, t] for p, t in walk]
    while rest:
        seen = {}
        cut = None
        for idx, (p, _) in enumerate(rest):
            if p in seen:
                cut = (seen[p], idx)
                break
            seen[p] = idx
        if cut is None:
            loops.append(rest)
            break
        j, i = cut
        loop = rest[j:i]
        loop[0] = [loop[0][0], rest[i][1]]
        loops.append(loop)
        junction_tag = rest[j][1]
        rest = rest[:j] + rest[i:]
        if rest:
            rest[j] = [rest[j][0], junction_tag]
    return loops


# ---------------------------------------------------------------------------
# 规范化与输出
# ---------------------------------------------------------------------------

def _reverse(verts, outtags):
    n = len(verts)
    rv = [verts[(-i) % n] for i in range(n)]
    rt = [outtags[(-i - 1) % n] for i in range(n)]
    return rv, rt


def _min_rotation(verts, outtags):
    n = len(verts)
    best = None
    for k in range(n):
        cand = (
            tuple(verts[k:] + verts[:k]),
            tuple(outtags[k:] + outtags[:k]),
        )
        if best is None or cand < best:
            best = cand
    return best


@dataclass(frozen=True)
class Region:
    """一个连通裁剪结果；degenerate=True 表示仅为线段或点（面积 0）。"""

    vertices: Tuple[Point, ...]
    edge_sources: Tuple[EdgeSource, ...]
    degenerate: bool

    @property
    def signed_area(self) -> Fraction:
        return _signed_area2(self.vertices) / 2

    @property
    def area(self) -> Fraction:
        return abs(self.signed_area)


@dataclass(frozen=True)
class ClipResult:
    regions: Tuple[Region, ...]
    window: Tuple[Point, ...]  # 规范化后的逆时针裁剪窗

    @property
    def solid(self) -> Tuple[Region, ...]:
        return tuple(r for r in self.regions if not r.degenerate)

    @property
    def degenerate(self) -> Tuple[Region, ...]:
        return tuple(r for r in self.regions if r.degenerate)

    @property
    def total_area(self) -> Fraction:
        return sum((r.area for r in self.regions), Fraction(0))


def _make_region(loop, edge_map):
    verts = [p for p, _ in loop]
    n = len(verts)
    intags = []
    for _, (kind, idx) in loop:
        if kind == "clip":
            intags.append(("clip", edge_map[idx]))
        else:
            intags.append(("subject", idx))
    area2 = _signed_area2(verts)
    degenerate = area2 == 0
    if degenerate and n >= 2 and all(kind != "subject" for kind, _ in intags):
        return None
    outtags = [intags[(i + 1) % n] for i in range(n)]
    if degenerate:
        fwd = _min_rotation(verts, outtags)
        rev = _min_rotation(*_reverse(verts, outtags))
        cv, ct = min(fwd, rev)
    else:
        if area2 < 0:
            verts, outtags = _reverse(verts, outtags)
        cv, ct = _min_rotation(verts, outtags)
    return Region(vertices=tuple(cv), edge_sources=tuple(ct), degenerate=degenerate)


def clip_polygon(subject, window) -> ClipResult:
    """用凸裁剪窗裁剪简单多边形，返回 ClipResult。

    subject：简单多边形顶点序列（顺/逆时针均可）。
    window：凸多边形顶点序列（内部统一为逆时针）。
    非法输入（自交、尖刺、零面积、非凸窗口等）抛出 ValueError。
    """
    subj = _validate_simple_polygon(subject, name="被裁多边形")
    win, sign = _validate_window(window)
    n = len(win)
    if sign < 0:
        ccw = [win[(-i) % n] for i in range(n)]
        edge_map = [(n - 1 - j) % n for j in range(n)]
    else:
        ccw = list(win)
        edge_map = list(range(n))

    walk = [[p, ("subject", (i - 1) % len(subj))] for i, p in enumerate(subj)]
    for j in range(n):
        window_a = ccw[j]
        window_b = ccw[(j + 1) % n]
        walk = _clip_halfplane(walk, window_a, window_b, j)
        if not walk:
            break

    regions = []
    if walk:
        for loop in _split_loops(_refine_walk(walk)):
            region = _make_region(loop, edge_map)
            if region is not None:
                regions.append(region)
    regions.sort(key=lambda r: (r.vertices, r.edge_sources))
    return ClipResult(regions=tuple(regions), window=tuple(ccw))
