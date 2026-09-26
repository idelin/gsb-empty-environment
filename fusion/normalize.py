"""归一化：只使用快照里的明确统计（min/max）。

缺失字段不在此处理（它根本不在快照里，由 fusion 的公开缺失规则处理），
因此不会被错误地填成 0 参与归一化。

全同分（含只有 1 个候选）时极差为 0，为避免除零，公开规则约定全部归为
1.0："该字段内无法区分优劣"，而不是 0（0 会把唯一/并列候选错误惩罚掉）。
"""

from __future__ import annotations

EPSILON = 1e-12


def normalize_series(values: list[int]) -> list[float]:
    if not values:
        return []
    lo = min(values)
    hi = max(values)
    if hi - lo <= EPSILON:
        return [1.0 for _ in values]
    span = float(hi - lo)
    return [float(v - lo) / span for v in values]


def normalize_value(value: float, lo: float, hi: float) -> float:
    if hi - lo <= EPSILON:
        return 1.0
    return (value - lo) / (hi - lo)
