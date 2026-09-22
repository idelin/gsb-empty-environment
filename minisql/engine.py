"""本地执行器：只扫描分析器保留的分区，按三值逻辑过滤。

多重集合语义：行以 list 存放，重复行各自独立出现；
OR 跨分区时结果就是各分区命中行的并集（拼接，不去重）。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Mapping

from .analyzer import Decision, analyze
from .logic import Tri
from .partition import PartitionedTable
from .predicate import Expr, normalize


@dataclass
class QueryResult:
    rows: list[dict]
    decisions: list[Decision]

    @property
    def scanned(self) -> list[str]:
        return [d.partition for d in self.decisions if d.kept]

    @property
    def skipped(self) -> list[str]:
        return [d.partition for d in self.decisions if not d.kept]


def select(table: PartitionedTable, where: Expr | None = None,
           params: Mapping[str, Any] | None = None,
           decisions: list[Decision] | None = None) -> QueryResult:
    """执行查询。可传入预编译阶段（参数未绑定）生成的 decisions 计划，
    模拟“一次分析、多次绑定执行”的预编译语句流程。"""
    if decisions is None:
        decisions = analyze(where, table, params)
    pred = normalize(where) if where is not None else None
    rows: list[dict] = []
    for part, dec in zip(table.partitions, decisions):
        if not dec.kept:
            continue
        for row in part.rows:
            if pred is None or pred.eval(row, params) is Tri.TRUE:
                rows.append(row)
    return QueryResult(rows, decisions)


def select_full_scan(table: PartitionedTable, where: Expr | None = None,
                     params: Mapping[str, Any] | None = None) -> list[dict]:
    """不做任何裁剪的对照扫描，用于验证裁剪正确性。"""
    pred = normalize(where) if where is not None else None
    return [row for row in table.rows
            if pred is None or pred.eval(row, params) is Tri.TRUE]
