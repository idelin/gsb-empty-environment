"""增量修复：新增 / 撤回记录后的全局重新校验与修复。

原则
----
- 局部组达到 k 不代表全局可行：每次变更后，对**所有**等价类重新
  分组、重新计数（不沿用旧计数），并重新运行全局优化，保证返回的
  方案在整个数据集上满足 k 且损失最低。
"""
from __future__ import annotations

from .core import Record
from .hierarchy import Hierarchy
from .optimize import SearchResult, optimize


class IncrementalAnonymizer:
    """维护数据集与当前最优方案的增量包装器。"""

    def __init__(
        self,
        hierarchies: tuple[Hierarchy, ...],
        k: int,
        budget: int,
        count_mode: str = "record",
        suppression_penalty: float = 1.0,
    ) -> None:
        self.hierarchies = hierarchies
        self.k = k
        self.budget = budget
        self.count_mode = count_mode
        self.suppression_penalty = suppression_penalty
        self.records: dict[str, Record] = {}
        self.last_result: SearchResult | None = None

    def _repair(self) -> SearchResult:
        """全局修复：对所有记录重新分组、重新计数、重新优化。"""
        self.last_result = optimize(
            list(self.records.values()),
            self.hierarchies,
            self.k,
            self.budget,
            count_mode=self.count_mode,
            suppression_penalty=self.suppression_penalty,
        )
        return self.last_result

    def load(self, records: list[Record]) -> SearchResult:
        self.records = {r.rid: r for r in records}
        return self._repair()

    def add(self, record: Record) -> SearchResult:
        """新增记录后全局修复（不只检查受影响的类）。"""
        if record.rid in self.records:
            raise ValueError(f"记录 {record.rid!r} 已存在")
        self.records[record.rid] = record
        return self._repair()

    def remove(self, rid: str) -> SearchResult:
        """撤回记录后全局修复：撤回可能使其他组跌破 k，必须全部重查。"""
        if rid not in self.records:
            raise KeyError(f"记录 {rid!r} 不存在")
        del self.records[rid]
        return self._repair()
