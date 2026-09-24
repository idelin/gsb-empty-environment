"""数据模型：约束、分段结果、查询分段、软撤销决策。"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Constraint:
    """一条 x_v - x_u <= c 约束，生效区间为左闭右开 [start, end)。

    cid:      约束（边）的字符串 ID；重边用不同 ID 区分。
    priority: 优先级，数值越小越先被软撤销。
    version:  同 ID 更正时递增的版本号。
    """

    cid: str
    u: str
    v: str
    c: int
    start: int
    end: int
    priority: int
    version: int = 1

    def __post_init__(self):
        if not self.start < self.end:
            raise ValueError(f"约束 {self.cid!r} 的区间为空: [{self.start}, {self.end})")


@dataclass(frozen=True)
class SegmentResult:
    """一个原子分段的求解结果。"""

    feasible: bool
    potentials: dict | None      # 可行时为 {顶点: 势值}
    cycle: tuple | None          # 不可行时为负环约束 ID 元组（环序）


@dataclass(frozen=True)
class QueryPiece:
    """时间窗查询结果中的一个分段（左闭右开）。"""

    start: int
    end: int
    feasible: bool
    potentials: dict | None = None
    cycle: tuple | None = None


@dataclass(frozen=True)
class Decision:
    """一次软撤销决策：为解决 interval 上的负环 cycle 而撤销 cid。"""

    action: str
    cid: str
    interval: tuple
    cycle: tuple

    def __str__(self):
        s, e = self.interval
        return (f"DECISION {self.action} {self.cid} on [{s}, {e}) "
                f"due to negative cycle {list(self.cycle)}")


def format_pieces(pieces) -> str:
    """把查询分段格式化为可打印文本（势值按键排序，保证输出确定）。"""
    lines = []
    for p in pieces:
        if p.feasible:
            pot = dict(sorted(p.potentials.items()))
            lines.append(f"  FEASIBLE [{p.start}, {p.end}) potentials={pot}")
        else:
            lines.append(f"  CONFLICT [{p.start}, {p.end}) cycle={list(p.cycle)}")
    return "\n".join(lines)
