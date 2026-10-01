"""二维生效范围：业务时间 x 系统版本时间的矩形冲突修复。

- 每条配置同时受业务时间区间 [t0, t1) 与系统版本区间 [v0, v1) 约束，
  即一个半开轴对齐矩形；查询为有限矩形 [qt0, qt1) x [qv0, qv1)。
- 生效规则顺序与一维一致：priority 高 -> version 高 -> id 小。
- 输出覆盖整个查询矩形的规范、不重叠矩形分区（含无覆盖区域，value=None）。
- 贪心合并只做“水平条带 + 垂直等宽合并”，是局部贪心，
  不保证全局最少矩形数；小网格可用 exact_min_partition 求精确最小覆盖。
- verify_partition 为独立验证器（不复用生成器代码）。
- 撤销配置时仅失效与其矩形相交的网格单元，其余缓存单元不动；
  audit() 以全量网格枚举逐单元、逐端点对照暴力重算结果。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Hashable


@dataclass(frozen=True)
class RectConfig:
    id: str
    priority: int
    version: int
    t0: float
    t1: float
    v0: float
    v1: float
    value: Hashable

    def __post_init__(self) -> None:
        if not (self.t0 < self.t1 and self.v0 < self.v1):
            raise ValueError(f"config {self.id!r}: 需要 t0 < t1 且 v0 < v1")

    def rank(self) -> tuple:
        return (-self.priority, -self.version, self.id)


@dataclass(frozen=True)
class Rect:
    x0: float
    x1: float
    y0: float
    y1: float
    value: Any  # None 表示无覆盖


@dataclass(frozen=True)
class AffectedRect:
    x0: float
    x1: float
    y0: float
    y1: float
    old_value: Any
    new_value: Any


@dataclass
class Grid:
    xs: list  # 业务时间网格线，长度 C+1
    ys: list  # 版本时间网格线，长度 R+1
    values: list  # values[j][i]，j 为版本维行，i 为业务时间维列

    @property
    def rows(self) -> int:
        return len(self.ys) - 1

    @property
    def cols(self) -> int:
        return len(self.xs) - 1


def winner_value(configs, x0, x1, y0, y1):
    """覆盖整个单元 [x0,x1) x [y0,y1) 的最优配置值；无覆盖返回 None。"""
    covering = [
        c
        for c in configs
        if c.t0 <= x0 and c.t1 >= x1 and c.v0 <= y0 and c.v1 >= y1
    ]
    if not covering:
        return None
    return min(covering, key=RectConfig.rank).value


def point_winner(configs, x, y):
    """半开语义下的单点暴力查询（ground truth）。"""
    covering = [c for c in configs if c.t0 <= x < c.t1 and c.v0 <= y < c.v1]
    if not covering:
        return None
    return min(covering, key=RectConfig.rank).value


def build_grid(configs, query) -> Grid:
    qt0, qt1, qv0, qv1 = query
    xs = {qt0, qt1}
    ys = {qv0, qv1}
    for c in configs:
        if c.t1 > qt0 and c.t0 < qt1 and c.v1 > qv0 and c.v0 < qv1:
            xs.add(max(c.t0, qt0))
            xs.add(min(c.t1, qt1))
            ys.add(max(c.v0, qv0))
            ys.add(min(c.v1, qv1))
    xs = sorted(xs)
    ys = sorted(ys)
    values = [
        [winner_value(configs, xs[i], xs[i + 1], ys[j], ys[j + 1]) for i in range(len(xs) - 1)]
        for j in range(len(ys) - 1)
    ]
    return Grid(xs, ys, values)


# ---- 贪心分区（不保证全局最少） ----

def greedy_cell_partition(values):
    """返回 (i0, i1, j0, j1, value) 单元矩形列表。

    局部贪心：行内水平条带 + 与上一行同 (i0, i1, value) 条带的垂直合并。
    结果确定但**不保证全局最少**；精确最小值见 exact_min_partition。
    """
    rows = len(values)
    cols = len(values[0])
    active = {}  # (i0, i1, value) -> [i0, i1, j0, j1, value]
    done = []
    for j in range(rows):
        strips = []
        i = 0
        while i < cols:
            v = values[j][i]
            i2 = i + 1
            while i2 < cols and values[j][i2] == v:
                i2 += 1
            strips.append((i, i2, v))
            i = i2
        new_active = {}
        for i0, i1, v in strips:
            key = (i0, i1, v)
            if key in active:
                s = active[key]
                s[3] = j + 1
                new_active[key] = s
            else:
                s = [i0, i1, j, j + 1, v]
                new_active[key] = s
                done.append(s)
        active = new_active
    return [tuple(s) for s in done]


def greedy_partition(grid: Grid) -> list[Rect]:
    return [
        Rect(grid.xs[i0], grid.xs[i1], grid.ys[j0], grid.ys[j1], v)
        for i0, i1, j0, j1, v in greedy_cell_partition(grid.values)
    ]


# ---- 小网格精确最小矩形覆盖 ----

class BudgetExceeded(Exception):
    """求解节点预算耗尽，精确解中断。"""


@dataclass(frozen=True)
class SolveResult:
    count: Any  # 精确解的矩形数；预算中断时为 None
    optimal: bool
    lower_bound: int  # 中断时仍有效的下界
    upper_bound: int  # 贪心给出的上界
    nodes: int  # 实际消耗的求解节点数
    cells: Any  # optimal 时为 (i0, i1, j0, j1, value) 列表，否则为 None


def exact_min_partition(values, budget: int = 100_000) -> SolveResult:
    """对 values[j][i] 网格求单色矩形精确划分（不重叠、全覆盖、数量最少）。

    状态压缩 DP + 记忆化；budget 限制展开的状态数，超出即抛 BudgetExceeded
    并返回带 [lower_bound, upper_bound] 区间的非最优结果。
    """
    rows = len(values)
    cols = len(values[0])
    full = (1 << (rows * cols)) - 1

    rects_by_cell: list[list] = [[] for _ in range(rows * cols)]
    max_area = 1
    for r0 in range(rows):
        for r1 in range(r0 + 1, rows + 1):
            for c0 in range(cols):
                for c1 in range(c0 + 1, cols + 1):
                    v = values[r0][c0]
                    mask = 0
                    mono = True
                    for r in range(r0, r1):
                        for c in range(c0, c1):
                            if values[r][c] != v:
                                mono = False
                                break
                            mask |= 1 << (r * cols + c)
                        if not mono:
                            break
                    if mono:
                        area = (r1 - r0) * (c1 - c0)
                        max_area = max(max_area, area)
                        entry = (mask, area, r0, r1, c0, c1)
                        for r in range(r0, r1):
                            for c in range(c0, c1):
                                rects_by_cell[r * cols + c].append(entry)
    for lst in rects_by_cell:
        lst.sort(key=lambda e: -e[1])

    total = rows * cols
    lower_bound = -(-total // max_area)  # 弱下界：总面积 / 最大单色矩形
    greedy_n = len(greedy_cell_partition(values))

    memo: dict[int, tuple] = {}
    nodes = 0

    def rec(mask: int):
        nonlocal nodes
        if mask == 0:
            return (0, None, None)
        hit = memo.get(mask)
        if hit is not None:
            return hit
        nodes += 1
        if nodes > budget:
            raise BudgetExceeded()
        lsb = mask & (-mask)
        idx = lsb.bit_length() - 1
        best = None
        for m, _area, r0, r1, c0, c1 in rects_by_cell[idx]:
            if m & mask == m:
                sub = rec(mask & ~m)
                cand = sub[0] + 1
                if best is None or cand < best[0]:
                    best = (cand, m, (r0, r1, c0, c1))
        memo[mask] = best
        return best

    try:
        rec(full)
    except BudgetExceeded:
        return SolveResult(None, False, lower_bound, greedy_n, nodes, None)

    cells = []
    mask = full
    while mask:
        _cnt, m, (r0, r1, c0, c1) = memo[mask]
        cells.append((c0, c1, r0, r1, values[r0][c0]))
        mask &= ~m
    count = len(cells)
    return SolveResult(count, True, count, count, nodes, cells)


def rects_from_cells(grid: Grid, cells) -> list[Rect]:
    return [
        Rect(grid.xs[i0], grid.xs[i1], grid.ys[j0], grid.ys[j1], v)
        for i0, i1, j0, j1, v in cells
    ]


# ---- 独立验证器 ----

def verify_partition(grid: Grid, rects) -> list[str]:
    """独立验证：矩形不重叠、各自单色、且恰好覆盖整个查询矩形一次。

    不调用任何分区生成代码，直接逐单元计数与查表。
    返回错误列表，空列表表示验证通过。
    """
    errors: list[str] = []
    xs, ys, values = grid.xs, grid.ys, grid.values
    xidx = {x: i for i, x in enumerate(xs)}
    yidx = {y: j for j, y in enumerate(ys)}
    cover = [[0] * grid.cols for _ in range(grid.rows)]
    for k, rc in enumerate(rects):
        if not (rc.x0 < rc.x1 and rc.y0 < rc.y1):
            errors.append(f"rect {k}: 退化矩形 {rc}")
            continue
        if rc.x0 not in xidx or rc.x1 not in xidx or rc.y0 not in yidx or rc.y1 not in yidx:
            errors.append(f"rect {k}: 边界未对齐网格线 {rc}")
            continue
        i0, i1 = xidx[rc.x0], xidx[rc.x1]
        j0, j1 = yidx[rc.y0], yidx[rc.y1]
        vals = set()
        for j in range(j0, j1):
            for i in range(i0, i1):
                vals.add(values[j][i])
                cover[j][i] += 1
        if vals != {rc.value}:
            errors.append(f"rect {k}: 非单色或值不符 grid={vals} rect={rc.value!r}")
    for j in range(grid.rows):
        for i in range(grid.cols):
            if cover[j][i] != 1:
                errors.append(f"cell (i={i}, j={j}) 覆盖次数为 {cover[j][i]}，应为 1")
    return errors


# ---- 增量二维时间线 ----

class RectTimeline:
    """固定查询矩形上的增量二维分区。"""

    def __init__(self, query) -> None:
        qt0, qt1, qv0, qv1 = query
        if not (qt0 < qt1 and qv0 < qv1):
            raise ValueError("查询矩形需要 qt0 < qt1 且 qv0 < qv1")
        self.query = tuple(query)
        self._configs: dict[str, RectConfig] = {}
        self._grid = Grid([qt0, qt1], [qv0, qv1], [[None]])

    @property
    def grid(self) -> Grid:
        return self._grid

    def add(self, cfg: RectConfig) -> None:
        if cfg.id in self._configs:
            raise ValueError(f"重复的配置 id: {cfg.id!r}")
        self._configs[cfg.id] = cfg
        self._grid = build_grid(list(self._configs.values()), self.query)

    def revoke(self, cfg_id: str) -> list[AffectedRect]:
        """撤销配置：仅重算与其矩形相交的网格单元，返回受影响区域。"""
        if cfg_id not in self._configs:
            raise KeyError(f"未知配置 id: {cfg_id!r}")
        cfg = self._configs.pop(cfg_id)
        g = self._grid
        cfgs = list(self._configs.values())
        changed = []
        for j in range(g.rows):
            for i in range(g.cols):
                x0, x1 = g.xs[i], g.xs[i + 1]
                y0, y1 = g.ys[j], g.ys[j + 1]
                if x1 <= cfg.t0 or x0 >= cfg.t1 or y1 <= cfg.v0 or y0 >= cfg.v1:
                    continue  # 与被撤销矩形不相交，缓存保持有效
                new_v = winner_value(cfgs, x0, x1, y0, y1)
                if new_v != g.values[j][i]:
                    changed.append((i, j, g.values[j][i], new_v))
                    g.values[j][i] = new_v
        if not changed:
            return []
        mark = [[None] * g.cols for _ in range(g.rows)]
        for i, j, ov, nv in changed:
            mark[j][i] = (ov, nv)
        out = []
        for i0, i1, j0, j1, v in greedy_cell_partition(mark):
            if v is None:
                continue
            out.append(AffectedRect(g.xs[i0], g.xs[i1], g.ys[j0], g.ys[j1], v[0], v[1]))
        return out

    def partition(self) -> list[Rect]:
        return greedy_partition(self._grid)

    def value_at(self, x, y):
        """基于缓存网格的点查询。"""
        qt0, qt1, qv0, qv1 = self.query
        if not (qt0 <= x < qt1 and qv0 <= y < qv1):
            raise ValueError("查询点超出查询矩形")
        i = _bisect_index(self._grid.xs, x)
        j = _bisect_index(self._grid.ys, y)
        return self._grid.values[j][i]

    def audit(self) -> list[str]:
        """全量网格枚举对照：逐单元中心与逐端点（半开语义）暴力重算。

        端点 (xs[i], ys[j]) 的生效值必须等于其右下单元的值
        （半开区间 [x, x+dx) x [y, y+dy) 的角点归属该单元）。
        """
        cfgs = list(self._configs.values())
        g = self._grid
        errors: list[str] = []
        for j in range(g.rows):
            for i in range(g.cols):
                cx = (g.xs[i] + g.xs[i + 1]) / 2
                cy = (g.ys[j] + g.ys[j + 1]) / 2
                want = point_winner(cfgs, cx, cy)
                if want != g.values[j][i]:
                    errors.append(
                        f"cell (i={i}, j={j}) 中心({cx},{cy}): 缓存={g.values[j][i]!r} 枚举={want!r}"
                    )
                want_ep = point_winner(cfgs, g.xs[i], g.ys[j])
                if want_ep != g.values[j][i]:
                    errors.append(
                        f"端点 ({g.xs[i]},{g.ys[j]}): 缓存={g.values[j][i]!r} 枚举={want_ep!r}"
                    )
        return errors


def _bisect_index(lines, x) -> int:
    lo, hi = 0, len(lines) - 1
    while lo < hi:
        mid = (lo + hi + 1) // 2
        if lines[mid] <= x:
            lo = mid
        else:
            hi = mid - 1
    return lo
