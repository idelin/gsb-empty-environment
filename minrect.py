"""最小面积定向包围矩形（精确算术，无 epsilon）。

设计要点
========
* 坐标只接受 int / Fraction（或二元组），内部统一为 Fraction，
  所有拓扑判断（凸包转向、卡壳推进、面积比较）都是精确有理运算，
  不使用任何浮点 epsilon。
* 凸包：Andrew 单调链，精确叉积，共线中间点被剔除。
* 旋转卡壳：对 CCW 凸包的每条边维护三个单调推进的支撑索引
  （最大叉积、最大/最小点积），每条边产出一个候选矩形。
* 候选面积以 Fraction 精确比较；等面积时按规范决胜规则选择，
  结果与输入顺序无关（见 README）。

退化定义
========
* 空点集：抛出 ValueError。
* 单点：面积 0，四角均为该点，support_edge 为 None，方向取 (1, 0)。
* 两点 / 全共线：面积 0 的退化矩形，四角为两端点各重复两次。
"""

from __future__ import annotations

from dataclasses import dataclass, replace
from fractions import Fraction
from typing import Iterable

Point = tuple[Fraction, Fraction]


def as_point(p) -> Point:
    """把 (int|Fraction, int|Fraction) 规范化为精确 Point。"""
    x, y = p
    fx = x if isinstance(x, Fraction) else Fraction(x)
    fy = y if isinstance(y, Fraction) else Fraction(y)
    return (fx, fy)


def _sub(a: Point, b: Point) -> Point:
    return (a[0] - b[0], a[1] - b[1])


def _dot(a: Point, b: Point) -> Fraction:
    return a[0] * b[0] + a[1] * b[1]


def _cross(a: Point, b: Point) -> Fraction:
    return a[0] * b[1] - a[1] * b[0]


# ---------------------------------------------------------------- 凸包

def convex_hull(points: Iterable) -> list[Point]:
    """Andrew 单调链凸包。

    返回 CCW 顶点列表（不重复起点），边上的共线中间点被剔除；
    全共线时返回两个端点；单点返回单点。全部使用精确叉积。
    """
    pts = sorted(set(as_point(p) for p in points))
    if len(pts) <= 1:
        return pts

    def build(seq):
        chain: list[Point] = []
        for p in seq:
            while len(chain) >= 2 and _cross(
                _sub(chain[-1], chain[-2]), _sub(p, chain[-1])
            ) <= 0:
                chain.pop()
            chain.append(p)
        return chain

    lower = build(pts)
    upper = build(list(reversed(pts)))
    return lower[:-1] + upper[:-1]


# ---------------------------------------------------------------- 证书

@dataclass(frozen=True)
class RectangleCertificate:
    """最小面积定向包围矩形证书。

    area            精确面积（Fraction，恒 >= 0）
    support_edge    所贴合的凸包边 (a, b)；单点时为 None
    direction       支撑边方向向量 e（未归一化，保持有理）
    corners         矩形四角，规范 CCW 序（字典序最小角起始）
    proj_along      所有点向 e 投影的 (min, max)（未归一化，精确）
    proj_across     所有点向法向 n=(-ey, ex) 投影的 (min, max)
    extremes        四个极值点 (min_along, max_along, min_across, max_across)
    extreme_handles 由 PointCloud 填充：每个极值点对应的输入句柄组
    """

    area: Fraction
    support_edge: tuple[Point, Point] | None
    direction: Point
    corners: tuple[Point, Point, Point, Point]
    proj_along: tuple[Fraction, Fraction]
    proj_across: tuple[Fraction, Fraction]
    extremes: tuple[Point, Point, Point, Point]
    extreme_handles: tuple[tuple[int, ...], ...] = ()


def _canonical_corners(corners: list[Point]) -> tuple[Point, ...]:
    """CCW 四角旋转到字典序最小角起始，得到与起点无关的规范形。"""
    i = min(range(4), key=lambda t: corners[t])
    return tuple(corners[t % 4] for t in range(i, i + 4))


def _make_certificate(
    edge: tuple[Point, Point],
    extremes: tuple[Point, Point, Point, Point],
) -> RectangleCertificate:
    """由支撑边与四个极值点构造候选证书（全部精确有理运算）。

    四角 = alpha*e + beta*n，其中 alpha = dot(e,p)/|e|^2、
    beta = cross(e,p)/|e|^2，因此角点坐标恒为有理数。
    """
    a, b = edge
    left, right, bottom, top = extremes
    e = _sub(b, a)
    n = (-e[1], e[0])
    len2 = _dot(e, e)
    umin, umax = _dot(e, left), _dot(e, right)
    vmin, vmax = _cross(e, bottom), _cross(e, top)
    area = (umax - umin) * (vmax - vmin) / len2

    def corner(u: Fraction, v: Fraction) -> Point:
        au = u / len2
        av = v / len2
        return (au * e[0] + av * n[0], au * e[1] + av * n[1])

    corners = _canonical_corners([
        corner(umin, vmin),
        corner(umax, vmin),
        corner(umax, vmax),
        corner(umin, vmax),
    ])
    return RectangleCertificate(
        area=area,
        support_edge=(a, b),
        direction=e,
        corners=corners,
        proj_along=(umin, umax),
        proj_across=(vmin, vmax),
        extremes=(left, right, bottom, top),
    )


# ---------------------------------------------------------------- 旋转卡壳

def _edge_candidates(hull: list[Point]) -> Iterable[RectangleCertificate]:
    """旋转卡壳主过程：对每条凸包边产出一个候选矩形。

    j/k/l 三个支撑索引随边方向 CCW 旋转而单调推进，总复杂度 O(n)。
    所有推进比较均为精确叉积 / 点积。
    """
    n = len(hull)
    if n == 2:
        a, b = hull
        # dot(e, b) - dot(e, a) = |e|^2 > 0，故左端点为 a、右端点为 b
        yield _make_certificate((a, b), (a, b, a, a))
        return
    # 对首条边全量扫描初始化三个支撑索引；之后随边方向 CCW
    # 旋转单调推进（支撑点只沿凸包前移），总复杂度 O(n)。
    e0 = _sub(hull[1], hull[0])
    j = max(range(n), key=lambda t: _cross(e0, hull[t]))
    k = max(range(n), key=lambda t: _dot(e0, hull[t]))
    l = min(range(n), key=lambda t: _dot(e0, hull[t]))
    for i in range(n):
        a = hull[i]
        b = hull[(i + 1) % n]
        e = _sub(b, a)
        while _cross(e, hull[(j + 1) % n]) > _cross(e, hull[j]):
            j = (j + 1) % n
        while _dot(e, hull[(k + 1) % n]) > _dot(e, hull[k]):
            k = (k + 1) % n
        while _dot(e, hull[(l + 1) % n]) < _dot(e, hull[l]):
            l = (l + 1) % n
        # 凸包内部在边的左侧，min cross 恒在支撑边上取得
        yield _make_certificate((a, b), (hull[l], hull[k], a, hull[j]))


def min_area_rectangle(points: Iterable) -> RectangleCertificate:
    """求最小面积定向包围矩形，返回证书。

    等面积决胜规则：先比精确面积，再比规范四角元组的字典序，
    取最小者；结果与输入点的顺序及重复无关。
    """
    pts = [as_point(p) for p in points]
    if not pts:
        raise ValueError("空点集没有包围矩形")
    hull = convex_hull(pts)
    if len(hull) == 1:
        p = hull[0]
        return RectangleCertificate(
            area=Fraction(0),
            support_edge=None,
            direction=(Fraction(1), Fraction(0)),
            corners=(p, p, p, p),
            proj_along=(p[0], p[0]),
            proj_across=(p[1], p[1]),
            extremes=(p, p, p, p),
        )
    best: RectangleCertificate | None = None
    for cert in _edge_candidates(hull):
        if best is None or (cert.area, cert.corners) < (best.area, best.corners):
            best = cert
    return best


# ---------------------------------------------------------------- 核验

def shoelace2(corners) -> Fraction:
    """多边形有向面积两倍的绝对值（精确）。"""
    s = Fraction(0)
    m = len(corners)
    for i in range(m):
        a = corners[i]
        b = corners[(i + 1) % m]
        s += _cross(a, b)
    return abs(s)


def verify_certificate(points: Iterable, cert: RectangleCertificate) -> bool:
    """以逐凸包边枚举为参考，独立核验证书的最优性与一致性。

    检查项：
    1. 面积等于逐边枚举的最小精确面积（最优性）；
    2. 投影范围、四角、鞋带面积与证书面积自洽；
    3. 四个极值点是真实输入点且达到投影范围；
    4. 支撑边确为凸包边；
    5. 所有输入点的投影都落在证书范围内（可验证投影范围）。
    """
    pts = [as_point(p) for p in points]
    if not pts:
        return False
    hull = convex_hull(pts)

    best_area: Fraction | None = Fraction(0) if len(hull) == 1 else None
    for i in range(len(hull)):
        a = hull[i]
        b = hull[(i + 1) % len(hull)]
        e = _sub(b, a)
        if e == (Fraction(0), Fraction(0)):
            continue
        us = [_dot(e, p) for p in hull]
        vs = [_cross(e, p) for p in hull]
        area = (max(us) - min(us)) * (max(vs) - min(vs)) / _dot(e, e)
        if best_area is None or area < best_area:
            best_area = area
    if cert.area != best_area:
        return False

    e = cert.direction
    len2 = _dot(e, e)
    if len2 <= 0:
        return False
    umin, umax = cert.proj_along
    vmin, vmax = cert.proj_across
    if not (umin <= umax and vmin <= vmax):
        return False
    if cert.area != (umax - umin) * (vmax - vmin) / len2:
        return False

    n = (-e[1], e[0])

    def corner(u: Fraction, v: Fraction) -> Point:
        au = u / len2
        av = v / len2
        return (au * e[0] + av * n[0], au * e[1] + av * n[1])

    expected = _canonical_corners([
        corner(umin, vmin),
        corner(umax, vmin),
        corner(umax, vmax),
        corner(umin, vmax),
    ])
    if tuple(cert.corners) != expected:
        return False
    if shoelace2(cert.corners) != 2 * cert.area:
        return False

    pset = set(pts)
    left, right, bottom, top = cert.extremes
    if any(p not in pset for p in cert.extremes):
        return False
    if not (
        _dot(e, left) == umin
        and _dot(e, right) == umax
        and _cross(e, bottom) == vmin
        and _cross(e, top) == vmax
    ):
        return False

    if cert.support_edge is not None:
        m = len(hull)
        edges = {(hull[i], hull[(i + 1) % m]) for i in range(m)}
        if tuple(cert.support_edge) not in edges:
            return False

    for p in pts:
        u = _dot(e, p)
        v = _cross(e, p)
        if not (umin <= u <= umax and vmin <= v <= vmax):
            return False
    return True


# ---------------------------------------------------------------- 动态点集

class PointCloud:
    """支持批量加入 / 批量撤回的动态点集。

    每个点加入时获得唯一句柄（身份）；坐标重复的点拥有各自独立的
    句柄，撤回按句柄进行，几何计算自动去重。
    """

    def __init__(self, points: Iterable = ()):
        self._entries: list[Point | None] = []
        self.add_batch(points)

    def add(self, point) -> int:
        self._entries.append(as_point(point))
        return len(self._entries) - 1

    def add_batch(self, points: Iterable) -> list[int]:
        return [self.add(p) for p in points]

    def remove(self, handle: int) -> None:
        if not 0 <= handle < len(self._entries) or self._entries[handle] is None:
            raise KeyError(f"无效句柄: {handle}")
        self._entries[handle] = None

    def remove_batch(self, handles: Iterable[int]) -> None:
        for h in handles:
            self.remove(h)

    def points(self) -> list[Point]:
        return [p for p in self._entries if p is not None]

    def min_area_rectangle(self) -> RectangleCertificate:
        """对当前活跃点求最小包围矩形，并在证书中回填极值点句柄。"""
        cert = min_area_rectangle(self.points())
        index: dict[Point, list[int]] = {}
        for h, p in enumerate(self._entries):
            if p is not None:
                index.setdefault(p, []).append(h)
        handles = tuple(tuple(sorted(index[p])) for p in cert.extremes)
        return replace(cert, extreme_handles=handles)
