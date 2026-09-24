"""连接算子：点-区间 与 区间-区间（半开，半开）。

提供两种实现，结果一致、确定性排序（按左/右全局行号）：

1. ``interval_interval_join`` / ``point_interval_join``
   基于 :class:`~ival.index.IntervalIndex` 的索引连接，
   复杂度 O(n log n + m log n + k)，避免 n*m 全笛卡尔扫描；
2. ``sweep_interval_join`` / ``sweep_point_join``
   扫描线实现，O((n+m) log(n+m) + k)，同一坐标上事件顺序
   “先结束、再开始、最后点”精确表达半开边界。

重叠长度：``max(0, min(hi1,hi2) - max(lo1,lo2))``。
int 端点全程整数运算，结果为精确 int；float 端点为 float。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from .index import Interval, IntervalIndex, QueryHit
from .table import Table
from .values import FLOAT64, INT64, is_nan, is_null


@dataclass(frozen=True)
class Match:
    left_id: Any
    right_id: Any
    left_row: int
    right_row: int
    overlap_len: Any  # 点连接恒为 0；区间连接为正长度（int 精确）


@dataclass
class SideStats:
    side: str
    total_rows: int = 0
    used: int = 0
    null_endpoint_rows: int = 0
    nan_endpoint_rows: int = 0
    empty_interval_rows: int = 0  # lo >= hi，含零长与反向
    null_id_rows: int = 0
    nan_id_rows: int = 0
    skipped_rows: list[int] = field(default_factory=list)


@dataclass
class JoinResult:
    matches: list[Match]
    left_stats: SideStats
    right_stats: SideStats
    naive_pairs: int          # 全笛卡尔扫描需要比较的对数
    examined_pairs: int       # 本算法实际检查/产出的候选对数
    method: str


def _numeric_endpoint_type(table: Table, lo_col: str, hi_col: str) -> str:
    tlo = table.schema.type_of(lo_col)
    thi = table.schema.type_of(hi_col)
    if tlo not in (INT64, FLOAT64) or thi not in (INT64, FLOAT64):
        raise TypeError(f"端点列必须是 int64 或 float64，收到 {tlo}/{thi}")
    if tlo != thi:
        raise TypeError(f"两端点列类型必须一致，收到 {tlo} 与 {thi}（不做隐式提升）")
    return tlo


def extract_intervals(
    table: Table, id_col: str, lo_col: str, hi_col: str, side: str
) -> tuple[list[Interval[int]], SideStats]:
    """从批次表抽取合法 ``[lo, hi)`` 区间。

    null/NaN 端点行、lo >= hi 行被跳过并计入统计；id 允许重复，
    id 为 null/NaN 不影响参与连接，只计数。
    """
    _numeric_endpoint_type(table, lo_col, hi_col)
    stats = SideStats(side=side, total_rows=table.num_rows)
    lo_vals = table.column_values(lo_col)
    hi_vals = table.column_values(hi_col)
    ids = table.column_values(id_col)

    out: list[Interval[int]] = []
    for row in range(table.num_rows):
        lo, hi, sid = lo_vals[row], hi_vals[row], ids[row]
        if is_null(lo) or is_null(hi):
            stats.null_endpoint_rows += 1
            stats.skipped_rows.append(row)
            continue
        if is_nan(lo) or is_nan(hi):
            stats.nan_endpoint_rows += 1
            stats.skipped_rows.append(row)
            continue
        if lo >= hi:
            stats.empty_interval_rows += 1
            stats.skipped_rows.append(row)
            continue
        if is_null(sid):
            stats.null_id_rows += 1
        elif is_nan(sid):
            stats.nan_id_rows += 1
        out.append(Interval(lo, hi, row, sid))
        stats.used += 1
    return out, stats


def extract_points(
    table: Table, id_col: str, point_col: str, side: str
) -> tuple[list[tuple[Any, int, Any]], SideStats]:
    """抽取点：返回 ``[(point, row, source_id), ...]``。"""
    t = table.schema.type_of(point_col)
    if t not in (INT64, FLOAT64):
        raise TypeError(f"点列必须是 int64 或 float64，收到 {t}")
    stats = SideStats(side=side, total_rows=table.num_rows)
    pts = table.column_values(point_col)
    ids = table.column_values(id_col)

    out: list[tuple[Any, int, Any]] = []
    for row in range(table.num_rows):
        p, sid = pts[row], ids[row]
        if is_null(p):
            stats.null_endpoint_rows += 1
            stats.skipped_rows.append(row)
            continue
        if is_nan(p):
            stats.nan_endpoint_rows += 1
            stats.skipped_rows.append(row)
            continue
        if is_null(sid):
            stats.null_id_rows += 1
        elif is_nan(sid):
            stats.nan_id_rows += 1
        out.append((p, row, sid))
        stats.used += 1
    return out, stats


def _require_same_domain(left: Table, right: Table, lc: str, rc: str) -> None:
    if left.schema.type_of(lc) != right.schema.type_of(rc):
        raise TypeError("两侧数值域类型不同（int64 vs float64），拒绝隐式混合比较")


def overlap_len(a_lo, a_hi, b_lo, b_hi):
    """半开区间正长度交集；不相交返回 0（int 保持 int）。"""
    lo = a_lo if a_lo > b_lo else b_lo
    hi = a_hi if a_hi < b_hi else b_hi
    return hi - lo if hi > lo else 0


def _emit_sorted(matches: list[Match]) -> list[Match]:
    return sorted(matches, key=lambda m: (m.left_row, m.right_row))


# ---------- 索引连接 ----------

def interval_interval_join(
    left: Table, lid: str, llo: str, lhi: str,
    right: Table, rid: str, rlo: str, rhi: str,
) -> JoinResult:
    """区间-区间连接（右表建居中区间树索引）。"""
    _require_same_domain(left, right, llo, rlo)
    livals, ls = extract_intervals(left, lid, llo, lhi, "left")
    rivals, rs = extract_intervals(right, rid, rlo, rhi, "right")
    idx = IntervalIndex(rivals)

    matches: list[Match] = []
    examined = 0
    for iv in livals:
        hits: list[QueryHit] = list(idx.overlaps(iv.lo, iv.hi))
        examined += len(hits)  # 索引只产出真重叠候选，不做笛卡尔枚举
        for h in hits:
            matches.append(Match(
                iv.source_id, h.source_id, iv.payload, h.payload,
                overlap_len(iv.lo, iv.hi, h.lo, h.hi),
            ))
    return JoinResult(_emit_sorted(matches), ls, rs,
                      ls.total_rows * rs.total_rows, examined, "interval-tree-index")


def point_interval_join(
    points: Table, pid: str, pcol: str,
    intervals: Table, iid: str, ilo: str, ihi: str,
) -> JoinResult:
    """点-区间连接（区间表建索引）；点命中时 overlap_len 恒为 0。"""
    _require_same_domain(points, intervals, pcol, ilo)
    pts, ps = extract_points(points, pid, pcol, "left")
    ivs, is_ = extract_intervals(intervals, iid, ilo, ihi, "right")
    idx = IntervalIndex(ivs)

    zero = 0 if points.schema.type_of(pcol) == INT64 else 0.0
    matches: list[Match] = []
    examined = 0
    for p, row, sid in pts:
        hits = list(idx.stab(p))
        examined += len(hits)
        for h in hits:
            matches.append(Match(sid, h.source_id, row, h.payload, zero))
    return JoinResult(_emit_sorted(matches), ps, is_,
                      ps.total_rows * is_.total_rows, examined, "interval-tree-index")


# ---------- 扫描线连接（等价实现，事件序：结束 < 开始 < 点） ----------

def sweep_interval_join(
    left: Table, lid: str, llo: str, lhi: str,
    right: Table, rid: str, rlo: str, rhi: str,
) -> JoinResult:
    _require_same_domain(left, right, llo, rlo)
    livals, ls = extract_intervals(left, lid, llo, lhi, "left")
    rivals, rs = extract_intervals(right, rid, rlo, rhi, "right")

    # (x, kind) kind: 0=end, 1=start；同坐标先 end 再 start =>
    # [0,5) 与 [5,8) 在 5 处不相交。
    events: list[tuple[Any, int, int, int]] = []
    for iv in livals:
        events.append((iv.lo, 1, 0, iv.payload))
        events.append((iv.hi, 0, 0, iv.payload))
    for iv in rivals:
        events.append((iv.lo, 1, 1, iv.payload))
        events.append((iv.hi, 0, 1, iv.payload))
    events.sort(key=lambda e: (e[0], e[1]))

    l_by_row = {iv.payload: iv for iv in livals}
    r_by_row = {iv.payload: iv for iv in rivals}
    active: list[set[int]] = [set(), set()]
    matches: list[Match] = []
    examined = 0
    for x, kind, side, row in events:
        if kind == 0:  # 结束：与仍存活的对侧区间配对，随后移除
            opp = active[1 - side]
            if side == 0:
                iv, others = l_by_row[row], r_by_row
            else:
                iv, others = r_by_row[row], l_by_row
            for orow in list(opp):
                o = others[orow]
                examined += 1
                if side == 0:
                    m = Match(iv.source_id, o.source_id, row, orow,
                              overlap_len(iv.lo, iv.hi, o.lo, o.hi))
                else:
                    m = Match(o.source_id, iv.source_id, orow, row,
                              overlap_len(o.lo, o.hi, iv.lo, iv.hi))
                matches.append(m)
            active[side].discard(row)
        else:
            active[side].add(row)
    return JoinResult(_emit_sorted(matches), ls, rs,
                      ls.total_rows * rs.total_rows, examined, "sweep-line")


def sweep_point_join(
    points: Table, pid: str, pcol: str,
    intervals: Table, iid: str, ilo: str, ihi: str,
) -> JoinResult:
    _require_same_domain(points, intervals, pcol, ilo)
    pts, ps = extract_points(points, pid, pcol, "left")
    ivs, is_ = extract_intervals(intervals, iid, ilo, ihi, "right")

    events: list[tuple[Any, int, int]] = []
    for iv in ivs:
        events.append((iv.lo, 1, iv.payload))  # start
        events.append((iv.hi, 0, iv.payload))  # end
    # kind: 0=end < 1=start < 2=point：[0,5) 在点 5 已移除，[5,8) 在点 5 已加入。
    point_events = [(p, 2, row) for p, row, _ in pts]
    events.extend(point_events)
    events.sort(key=lambda e: (e[0], e[1]))

    iv_by_row = {iv.payload: iv for iv in ivs}
    pt_by_row = {row: (p, sid) for p, row, sid in pts}
    active: set[int] = set()
    zero = 0 if points.schema.type_of(pcol) == INT64 else 0.0
    matches: list[Match] = []
    examined = 0
    for x, kind, row in events:
        if kind == 0:
            active.discard(row)
        elif kind == 1:
            active.add(row)
        else:
            _, sid = pt_by_row[row]
            for irow in list(active):
                examined += 1
                matches.append(Match(sid, iv_by_row[irow].source_id,
                                     row, irow, zero))
    return JoinResult(_emit_sorted(matches), ps, is_,
                      ps.total_rows * is_.total_rows, examined, "sweep-line")
