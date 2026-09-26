"""有限域 CSP 求解器：轨迹式假设层 + GAC 传播 + 独立解检查器。

- 基础约束在建模后不可变（CSP.domains 用 frozenset 固化）。
- Solver.push()/pop() 支持任意嵌套的临时假设作用域。
- 每次域收缩都记录到 trail 并归属当前层；pop 精确回滚本层。
- 不调用任何 SAT/SMT 库，仅用标准库。
"""

from __future__ import annotations

from itertools import product


class Conflict(Exception):
    """传播或假设导致某变量域为空时抛出。"""


class Constraint:
    """n 元约束：给定作用域内变量的完整赋值，predicate 返回真假。"""

    def __init__(self, variables, predicate, name=None):
        self.variables = tuple(variables)
        self.predicate = predicate
        self.name = name or "c_" + "_".join(self.variables)

    def satisfied(self, assignment) -> bool:
        return bool(self.predicate(*(assignment[v] for v in self.variables)))

    def __repr__(self):
        return f"Constraint({self.name}, scope={self.variables})"


class CSP:
    """问题模型：有限域 + 约束集合。构建后视为只读。"""

    def __init__(self, domains, constraints):
        self.domains = {v: frozenset(d) for v, d in domains.items()}
        self.constraints = list(constraints)
        for con in self.constraints:
            for v in con.variables:
                if v not in self.domains:
                    raise ValueError(f"约束 {con.name} 引用了未知变量 {v!r}")


def check_solution(csp: CSP, assignment) -> bool:
    """独立检查器：不依赖 Solver 的任何内部状态，直接对照原始约束。"""
    for var, domain in csp.domains.items():
        if var not in assignment or assignment[var] not in domain:
            return False
    return all(con.satisfied(assignment) for con in csp.constraints)


class Solver:
    """带回溯搜索与假设层栈的求解器。

    层语义：
    - 层 0 为基础层（仅含基础约束传播的结果）。
    - 每次 push() 层号 +1，该层产生的一切域收缩都打上本层标记。
    - pop() 只回滚当前层的 trail 记录，深层已先弹出，故记录必在栈顶连续。
    - push 失败（冲突）时自动弹回本层，返回 False，外层状态完好。
    """

    def __init__(self, csp: CSP):
        self.csp = csp
        self.domains = {v: set(d) for v, d in csp.domains.items()}
        self.trail: list[tuple[str, object, int]] = []  # (变量, 被删值, 层号)
        self.layer = 0
        self.assumption_stack: list[dict] = []
        self._by_var = {v: [] for v in csp.domains}
        for con in csp.constraints:
            for v in con.variables:
                self._by_var[v].append(con)
        self._base_conflict = False
        try:
            self._propagate()
        except Conflict:
            self._base_conflict = True  # 基础层即不可满足，solve() 会返回 None

    # ---- 域操作与传播 -------------------------------------------------

    def _remove(self, var, value):
        if value in self.domains[var]:
            self.domains[var].discard(value)
            self.trail.append((var, value, self.layer))

    def _restrict_to(self, var, value):
        if value not in self.domains[var]:
            raise Conflict(f"假设 {var}={value!r} 与当前域冲突")
        for other in list(self.domains[var]):
            if other != value:
                self._remove(var, other)

    def _has_support(self, con, var, value) -> bool:
        others = [v for v in con.variables if v != var]
        for combo in product(*(self.domains[v] for v in others)):
            assignment = dict(zip(others, combo))
            assignment[var] = value
            if con.satisfied(assignment):
                return True
        return False

    def _propagate(self):
        queue = list(self.csp.constraints)
        while queue:
            con = queue.pop()
            for var in con.variables:
                for value in sorted(self.domains[var], key=repr):
                    if not self._has_support(con, var, value):
                        self._remove(var, value)
                        if not self.domains[var]:
                            raise Conflict(f"{var} 的域被掏空（约束 {con.name}）")
                        for other in self._by_var[var]:
                            if other is not con:
                                queue.append(other)

    # ---- 假设作用域 ----------------------------------------------------

    def push(self, assumptions=None) -> bool:
        """进入新一层并施加假设；冲突时自动回滚本层并返回 False。"""
        self.layer += 1
        self.assumption_stack.append(dict(assumptions or {}))
        try:
            for var, value in (assumptions or {}).items():
                self._restrict_to(var, value)
            self._propagate()
        except Conflict:
            self.pop()
            return False
        return True

    def pop(self):
        """退出当前层，精确撤销本层所有域收缩。"""
        if self.layer == 0:
            raise RuntimeError("基础层不可弹出")
        target = self.layer
        while self.trail and self.trail[-1][2] == target:
            var, value, _ = self.trail.pop()
            self.domains[var].add(value)
        self.assumption_stack.pop()
        self.layer -= 1

    # ---- 搜索 ----------------------------------------------------------

    def solve(self):
        """回溯搜索。每个分支决策本身也是一层假设，失败即弹层。"""
        if self._base_conflict or any(not d for d in self.domains.values()):
            return None
        if all(len(d) == 1 for d in self.domains.values()):
            assignment = {v: next(iter(d)) for v, d in self.domains.items()}
            if not check_solution(self.csp, assignment):
                raise RuntimeError(f"内部赋值未通过独立检查器: {assignment}")
            return assignment
        var = min(
            (v for v in self.domains if len(self.domains[v]) > 1),
            key=lambda v: len(self.domains[v]),
        )
        for value in sorted(self.domains[var], key=repr):
            if self.push({var: value}):
                result = self.solve()
                if result is not None:
                    return result
                self.pop()
        return None
