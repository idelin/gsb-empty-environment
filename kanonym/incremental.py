"""增量修复：新增 / 撤回记录后重新求解。

原则：修复时对**全部**记录重新分组、重新计数（不沿用旧计数），
并对所有等价类重新校验 k-约束——不因局部组已达 k 而忽略其他组。
"""
from __future__ import annotations

from .dataset import Record
from .solver import build_evidence, evaluate, solve_exact, subject_count


class Anonymizer:
    def __init__(self, hierarchies, k, sup_budget, metric="ncp", sup_cost=1.0):
        self.hierarchies = list(hierarchies)
        self.k = k
        self.sup_budget = sup_budget
        self.metric = metric
        self.sup_cost = sup_cost
        self._records: dict[str, Record] = {}
        self.result = None
        self.evidence = None

    @property
    def records(self):
        return list(self._records.values())

    def load(self, records):
        for r in records:
            self._records[r.rid] = r
        return self._resolve()

    def add_records(self, records):
        for r in records:
            if r.rid in self._records:
                raise KeyError(f"记录 id 重复: {r.rid}")
            self._records[r.rid] = r
        return self._repair("add", [r.rid for r in records])

    def remove_records(self, rids):
        for rid in rids:
            if rid not in self._records:
                raise KeyError(f"记录不存在: {rid}")
            del self._records[rid]
        return self._repair("remove", list(rids))

    def _resolve(self):
        self.result = solve_exact(
            self.records, self.hierarchies, self.k, self.sup_budget,
            metric=self.metric, sup_cost=self.sup_cost,
        )
        self.evidence = (
            build_evidence(self.records, self.result.best, self.k)
            if self.result.best is not None else {}
        )
        return self.result

    def _repair(self, op, changed_rids):
        old = self.result
        # 先用旧向量对全量数据重新评估（重新分组、重新计数），
        # 再整体重解以保证最优性；两步都覆盖所有等价类。
        old_vector_check = None
        if old is not None and old.best is not None:
            old_vector_check = evaluate(
                self.records, self.hierarchies, old.best.vector,
                self.k, self.sup_budget, self.metric, self.sup_cost,
            )
        new = self._resolve()
        return {
            "op": op,
            "changed_rids": list(changed_rids),
            "old_vector": old.best.vector if old and old.best else None,
            "old_vector_still_feasible": (
                old_vector_check.feasible if old_vector_check else None
            ),
            "new_vector": new.best.vector if new.best else None,
            "new_loss": new.best.loss if new.best else None,
            "new_status": new.status,
            "all_groups_revalidated": True,  # 全量重新分组与计数
            "n_groups": len(new.best.classes) if new.best else 0,
            "group_subject_counts": (
                {key: subject_count(recs) for key, recs in new.best.classes.items()}
                if new.best else {}
            ),
        }
