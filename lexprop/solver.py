"""求解器：传播到固定点 + 回溯搜索，带 push/pop 轨迹与预算。"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional, Set

from .checker import check_assignment
from .core import (Budget, BudgetExhausted, DomainWipeout, LexConstraint,
                   Proof, Var)

MSG_NO_FIXPOINT = "预算不足，尚未达到传播固定点"


@dataclass
class Result:
    status: str                      # "SAT" | "UNSAT" | "UNKNOWN"
    solution: Optional[Dict[Var, Any]] = None
    message: str = ""
    fixpoint_reached: bool = True
    proofs: List[Proof] = field(default_factory=list)


class Solver:
    def __init__(self, domains: Dict[Var, Set[Any]],
                 constraints: List[LexConstraint],
                 budget_steps: int = 100_000):
        self.domains: Dict[Var, Set[Any]] = {v: set(d) for v, d in domains.items()}
        self.constraints = list(constraints)
        self.budget = Budget(budget_steps)
        self.proofs: List[Proof] = []
        self.solution: Optional[Dict[Var, Any]] = None
        self._marks: List[tuple] = []

    # ---------- push/pop：恢复域与前缀支持缓存 ----------

    def push(self) -> None:
        snap_domains = {v: set(d) for v, d in self.domains.items()}
        snap_caches = [dict(c._cache) for c in self.constraints]
        self._marks.append((snap_domains, snap_caches, len(self.proofs)))

    def pop(self) -> None:
        snap_domains, snap_caches, proof_len = self._marks.pop()
        self.domains = snap_domains
        for c, cache in zip(self.constraints, snap_caches):
            c._cache = cache
        del self.proofs[proof_len:]

    # ---------- 传播 ----------

    def propagate(self) -> None:
        """迭代到固定点；预算耗尽抛 BudgetExhausted，域空抛 DomainWipeout。"""
        while True:
            changed = False
            for c in self.constraints:
                if not c.vars and not c.is_feasible(self.domains):
                    raise DomainWipeout(None)   # 例如 lex_less([], [])
                if c.propagate(self.domains, self.budget, self.proofs):
                    changed = True
            if not changed:
                return

    # ---------- 搜索 ----------

    def solve(self) -> Result:
        status = self._search()
        if status == "SAT":
            return Result("SAT", self.solution, "有解（已通过独立检查器验证）",
                          True, self.proofs)
        if status == "FAIL":
            return Result("UNSAT", None, "约束不可满足", True, self.proofs)
        return Result("UNKNOWN", None, MSG_NO_FIXPOINT, False, self.proofs)

    def _search(self) -> str:
        try:
            self.propagate()
        except BudgetExhausted:
            return "UNKNOWN"
        except DomainWipeout:
            return "FAIL"
        var = next((v for v in self.domains if len(self.domains[v]) > 1), None)
        if var is None:
            sol = {v: next(iter(d)) for v, d in self.domains.items()}
            for c in self.constraints:  # 声称有解前用独立检查器验原约束
                if not check_assignment(c.xs, c.ys, c.strict, sol):
                    raise AssertionError("内部错误：候选解未通过独立检查器")
            self.solution = sol
            return "SAT"
        for value in sorted(self.domains[var]):
            self.push()
            self.domains[var] = {value}
            status = self._search()
            self.pop()
            if status != "FAIL":
                return status
        return "FAIL"
