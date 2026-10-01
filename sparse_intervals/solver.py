"""矩形分区工具。

- greedy_partition:        行条带 + 纵向合并的局部贪心（不保证全局最少）。
- min_rect_partition:      小网格精确最小矩形覆盖（Exact Cover 搜索 + 预算中断）。
- components_lower_bound:  连通分量数，任意分区的矩形数下界。
- verify_grid_partition:   网格级独立验证（每格恰好覆盖一次且矩形内同值）。
- verify_partition_continuous: 连续坐标独立验证（仅用 value_at 采样，不依赖引擎内部网格）。

矩形用单元坐标 (x0, y0, x1, y1, value) 表示，x1/y1 为开边界。
"""

from __future__ import annotations

from typing import Any, Callable, Hashable, Iterable


class BudgetExceeded(Exception):
    """精确求解器搜索预算耗尽。"""


# ---------------------------------------------------------------- 贪心

def greedy_partition(grid: list) -> list:
    """行条带合并 + 相邻行同跨度同值条带纵向合并。

    这是局部贪心：结果通常接近最少，但不保证全局最少，
    调用方不得据此宣称最优。
    """
    H = len(grid)
    W = len(grid[0]) if H else 0
    out: list[tuple] = []
    open_rects: dict[tuple, int] = {}  # (x0, x1, value) -> 起始行 y0
    for y in range(H):
        runs = []
        x = 0
        while x < W:
            v = grid[y][x]
            x0 = x
            while x < W and grid[y][x] == v:
                x += 1
            runs.append((x0, x, v))
        new_open: dict[tuple, int] = {}
        for (x0, x1, v) in runs:
            key = (x0, x1, v)
            if key in open_rects:
                new_open[key] = open_rects.pop(key)
            else:
                new_open[key] = y
        for (x0, x1, v), y0 in open_rects.items():
            out.append((x0, y0, x1, y, v))
        open_rects = new_open
    for (x0, x1, v), y0 in open_rects.items():
        out.append((x0, y0, x1, H, v))
    return out


# ---------------------------------------------------------------- 下界

def components_lower_bound(grid: list) -> int:
    """同值连通分量数：任何单色矩形分区的矩形数都 >= 该值。"""
    H = len(grid)
    W = len(grid[0]) if H else 0
    seen = [[False] * W for _ in range(H)]
    count = 0
    for y0 in range(H):
        for x0 in range(W):
            if seen[y0][x0]:
                continue
            count += 1
            v = grid[y0][x0]
            stack = [(x0, y0)]
            seen[y0][x0] = True
            while stack:
                x, y = stack.pop()
                for nx, ny in ((x+1, y), (x-1, y), (x, y+1), (x, y-1)):
                    if (0 <= nx < W and 0 <= ny < H
                            and not seen[ny][nx] and grid[ny][nx] == v):
                        seen[ny][nx] = True
                        stack.append((nx, ny))
    return count


# ---------------------------------------------------------------- 精确求解

def _all_monochromatic_rects(grid: list, W: int, H: int):
    """枚举全部单色矩形及其单元位掩码。"""
    rects = []
    masks = []
    for y0 in range(H):
        for x0 in range(W):
            v = grid[y0][x0]
            for y1 in range(y0 + 1, H + 1):
                x1 = x0 + 1
                while x1 <= W:
                    # 增量检查：新增列 x1-1 必须全为 v
                    if any(grid[y][x1 - 1] != v for y in range(y0, y1)):
                        break
                    mask = 0
                    for y in range(y0, y1):
                        base = y * W
                        for x in range(x0, x1):
                            mask |= 1 << (base + x)
                    rects.append((x0, y0, x1, y1))
                    masks.append(mask)
                    x1 += 1
    return rects, masks


def min_rect_partition(grid: list, budget: int = 200_000):
    """小网格精确最小矩形覆盖。

    以 Exact Cover（Algorithm X 风格 + 分支限界）搜索矩形数最少的
    单色矩形分区。返回 (rects, status, nodes)：
      - status == "optimal":          已证明全局最少；
      - status == "budget_exceeded":  达到 budget 个搜索节点被中断，
                                      返回的是当前最优上界，不保证最少。
    """
    H = len(grid)
    W = len(grid[0]) if H else 0
    n = W * H
    if n == 0:
        return [], "optimal", 0

    rects, masks = _all_monochromatic_rects(grid, W, H)
    areas = [(r[2] - r[0]) * (r[3] - r[1]) for r in rects]
    maxarea = max(areas)

    cell_to_rects: list[list[int]] = [[] for _ in range(n)]
    for idx, m in enumerate(masks):
        mm = m
        while mm:
            lsb = mm & -mm
            cell_to_rects[lsb.bit_length() - 1].append(idx)
            mm ^= lsb

    # 用贪心结果作为初始上界。
    best = [(r[0], r[1], r[2], r[3]) for r in greedy_partition(grid)]
    best_count = len(best)
    chosen: list[int] = []
    nodes = 0
    full = (1 << n) - 1

    def search(uncovered: int) -> None:
        nonlocal nodes, best, best_count
        nodes += 1
        if nodes > budget:
            raise BudgetExceeded
        if uncovered == 0:
            best_count = len(chosen)
            best = [rects[i] for i in chosen]
            return
        # 下界剪枝：剩余单元数 / 最大矩形面积。
        remaining = uncovered.bit_count()
        if len(chosen) + -(-remaining // maxarea) >= best_count:
            return
        # 选可用候选最少的未覆盖单元（失败驱动）。
        cand = None
        mm = uncovered
        while mm:
            lsb = mm & -mm
            c = lsb.bit_length() - 1
            mm ^= lsb
            avail = [i for i in cell_to_rects[c] if masks[i] & ~uncovered == 0]
            if cand is None or len(avail) < len(cand):
                cand = avail
                if len(avail) <= 1:
                    break
        if not cand:
            return  # 不会发生（1x1 矩形总是候选），防御性返回
        cand.sort(key=lambda i: -areas[i])  # 大矩形优先，加速收敛
        for i in cand:
            chosen.append(i)
            search(uncovered & ~masks[i])
            chosen.pop()

    status = "optimal"
    try:
        search(full)
    except BudgetExceeded:
        status = "budget_exceeded"

    out = [(x0, y0, x1, y1, grid[y0][x0]) for (x0, y0, x1, y1) in best]
    return out, status, nodes


# ---------------------------------------------------------------- 验证器

def verify_grid_partition(grid: list, rects: Iterable[tuple]) -> list:
    """网格级独立验证：每个单元恰好被一个矩形覆盖，且每个矩形内同值。"""
    errors: list[str] = []
    H = len(grid)
    W = len(grid[0]) if H else 0
    cover = [[0] * W for _ in range(H)]
    for rect in rects:
        x0, y0, x1, y1 = rect[0], rect[1], rect[2], rect[3]
        if not (0 <= x0 < x1 <= W and 0 <= y0 < y1 <= H):
            errors.append(f"rect out of bounds: {rect}")
            continue
        v = grid[y0][x0]
        mono = True
        for y in range(y0, y1):
            for x in range(x0, x1):
                if grid[y][x] != v:
                    mono = False
                cover[y][x] += 1
        if not mono:
            errors.append(f"rect not monochromatic: {rect}")
    for y in range(H):
        for x in range(W):
            if cover[y][x] != 1:
                errors.append(f"cell ({x},{y}) covered {cover[y][x]} times")
    return errors


def verify_partition_continuous(
    value_at: Callable[[float, float], Hashable],
    query: tuple,
    rects: Iterable[dict],
) -> list:
    """连续坐标独立验证器。

    只依赖外部传入的 value_at(t, v) 与查询矩形，不依赖引擎内部网格：
      1. 每个矩形都在查询矩形内；
      2. 查询矩形内每个基本子格恰好被一个矩形覆盖（不重不漏）；
      3. 每个矩形内生效值恒定且等于其声明值。
    """
    t0, t1, v0, v1 = query
    rects = list(rects)
    errors: list[str] = []
    xs = sorted({t0, t1} | {r["t0"] for r in rects} | {r["t1"] for r in rects})
    ys = sorted({v0, v1} | {r["v0"] for r in rects} | {r["v1"] for r in rects})
    for r in rects:
        if not (t0 <= r["t0"] < r["t1"] <= t1 and v0 <= r["v0"] < r["v1"] <= v1):
            errors.append(f"rect out of query bounds: {r}")
    for j in range(len(ys) - 1):
        for i in range(len(xs) - 1):
            a, b = xs[i], xs[i + 1]
            c, d = ys[j], ys[j + 1]
            cover = [r for r in rects
                     if r["t0"] <= a and r["t1"] >= b
                     and r["v0"] <= c and r["v1"] >= d]
            if len(cover) != 1:
                errors.append(
                    f"cell t=[{a},{b}) v=[{c},{d}) covered {len(cover)} times")
                continue
            expect = value_at((a + b) / 2, (c + d) / 2)
            if cover[0]["value"] != expect:
                errors.append(
                    f"cell t=[{a},{b}) v=[{c},{d}) value "
                    f"{cover[0]['value']!r} != expected {expect!r}")
    return errors
