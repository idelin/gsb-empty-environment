"""独立校验：长度守恒、需求逐项精确满足、库存不超限。

不依赖求解器内部状态，只根据切割清单、需求与库存重新核算。
"""

from __future__ import annotations

from collections import Counter
from fractions import Fraction

from .model import Demand, cut_metrics, to_fraction


def verify_plan(bars, demands, stocks, kerf=0):
    """返回错误列表；空列表表示计划通过全部校验。"""
    kerf = to_fraction(kerf)
    errors = []
    produced = Counter()
    usage = Counter()
    for idx, bar in enumerate(bars):
        tag = f"第{idx + 1}根(长度{bar.stock_length})"
        try:
            cuts, leftover = cut_metrics(bar.stock_length, bar.pieces, kerf)
        except ValueError as exc:
            errors.append(f"{tag}: 切割不可行（{exc}）")
            continue
        if cuts != bar.cuts:
            errors.append(f"{tag}: 切割次数记录 {bar.cuts} 与核算 {cuts} 不符")
        if leftover != bar.leftover:
            errors.append(f"{tag}: 余料记录 {bar.leftover} 与核算 {leftover} 不符")
        total = sum(bar.pieces, Fraction(0)) + kerf * cuts + leftover
        if total != bar.stock_length:
            errors.append(f"{tag}: 长度不守恒 {total} != {bar.stock_length}")
        for p in bar.pieces:
            produced[p] += 1
        usage[(bar.stock_length, bar.cost)] += 1

    wanted = Counter()
    for d in demands:
        d = d if isinstance(d, Demand) else Demand.of(*d)
        wanted[d.length] += d.quantity
    for length, qty in wanted.items():
        if produced.get(length, 0) != qty:
            errors.append(
                f"需求 {length}: 需要 {qty} 段，实际切出 {produced.get(length, 0)} 段"
            )
    for length in produced:
        if length not in wanted:
            errors.append(f"多切了非需求段 {length}")

    limited = Counter()
    unlimited = set()
    for s in stocks:
        key = (s.length, s.cost)
        if s.count is None:
            unlimited.add(key)
        else:
            limited[key] += s.count
    for key, used in usage.items():
        if key in unlimited:
            continue
        cap = limited.get(key, 0)
        if used > cap:
            errors.append(
                f"库存超用: 原料 {key[0]}(成本{key[1]}) 用 {used} 根，限量 {cap} 根"
            )
    return errors


def expected_remnants(bars, min_remnant=0):
    """按计划清单独立核算应回库的可复用余料。"""
    mr = to_fraction(min_remnant)
    return tuple(
        sorted(
            (b.leftover for b in bars if b.leftover > 0 and b.leftover >= mr),
            reverse=True,
        )
    )
