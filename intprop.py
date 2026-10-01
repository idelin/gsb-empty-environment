"""整数系数线性不等式的区间界传播求解器（纯标准库，Python 3.11+）。

特性：
- 所有算术使用 Python 大整数，无溢出；
- 负系数除法取整方向正确（floor/ceil 严格按数学定义）；
- 等式拆成 <= 与 >= 两个方向传播；
- 多约束共享变量，队列驱动传播至固定点，并记录每次剪枝的解释；
- 支持 checkpoint/undo，撤回约束后域可恢复；
- 模同余必要条件（gcd）可发现纯区间传播漏掉的无解，但不声称完整求解；
- 声称 SATISFIABLE 时用独立检查器核对原始约束。
"""

from __future__ import annotations

from collections import deque
from math import gcd


def floor_div(numer: int, denom: int) -> int:
    """精确的向下取整除法，适用于任意符号与大整数。"""
    if denom == 0:
        raise ZeroDivisionError("floor_div by zero")
    return numer // denom  # Python 的 // 本身就是 floor 语义


def ceil_div(numer: int, denom: int) -> int:
    """精确的向上取整除法，适用于任意符号与大整数。"""
    if denom == 0:
        raise ZeroDivisionError("ceil_div by zero")
    return -((-numer) // denom)


class Constraint:
    """内部一律规范为 sum(coeff * var) <= rhs。"""

    __slots__ = ("terms", "rhs", "g")

    def __init__(self, terms: tuple[tuple[int, str], ...], rhs: int):
        self.terms = terms
        self.rhs = rhs
        g = 0
        for coeff, _ in terms:
            g = gcd(g, abs(coeff))
        self.g = g  # 系数的最大公约数（用于模同余必要条件）

    def __repr__(self) -> str:
        body = " + ".join(f"({a})*{v}" for a, v in self.terms) or "0"
        return f"{body} <= {self.rhs}"


class Solver:
    """有限域整数线性约束的界传播器。

    状态：每个变量的区间域 [lo, hi]、约束列表、监视表、传播队列、
    回滚 trail，以及每个界收紧的解释（由哪条约束推出）。
    """

    def __init__(self, use_congruence: bool = True):
        self.use_congruence = use_congruence
        self.lo: dict[str, int] = {}
        self.hi: dict[str, int] = {}
        self.why_lo: dict[str, tuple[int | str, str]] = {}
        self.why_hi: dict[str, tuple[int | str, str]] = {}
        self.constraints: list[Constraint] = []
        self.original: list[tuple[tuple[tuple[int, str], ...], int, str]] = []
        self.watch: dict[str, set[int]] = {}
        self.queue: deque[int] = deque()
        self.queued: set[int] = set()
        self.trail: list[tuple] = []
        self.conflict: str | None = None

    # ------------------------------------------------------------------ 变量
    def add_variable(self, name: str, lo: int, hi: int) -> None:
        if name in self.lo:
            raise ValueError(f"variable {name!r} already declared")
        lo, hi = int(lo), int(hi)
        if lo > hi:
            raise ValueError(f"empty initial domain for {name!r}")
        self.lo[name] = lo
        self.hi[name] = hi
        self.why_lo[name] = ("init", f"initial domain lower bound {lo}")
        self.why_hi[name] = ("init", f"initial domain upper bound {hi}")
        self.watch[name] = set()

    # ------------------------------------------------------------------ 约束
    def add_constraint(self, terms, rhs: int, sense: str = "<=") -> None:
        """terms 为 [(coeff, var), ...]；sense ∈ {"<=", ">=", "=="}。

        等式拆成两个方向；零系数项被丢弃（不影响任何域）；
        常量矛盾（如 0 <= -1）立即失败。
        """
        if self.conflict is not None:
            return
        rhs = int(rhs)
        raw = tuple((int(a), v) for a, v in terms)
        for _, v in raw:
            if v not in self.lo:
                raise ValueError(f"unknown variable {v!r}")
        self.original.append((raw, rhs, sense))
        clean = [(a, v) for a, v in raw if a != 0]

        if sense == "==":
            # 模同余必要条件：sum a_i x_i 必为 gcd(a_i) 的倍数。
            g = 0
            for a, _ in clean:
                g = gcd(g, abs(a))
            if g == 0:
                if rhs != 0:
                    self.conflict = f"constant contradiction: 0 == {rhs}"
                return
            if self.use_congruence and rhs % g != 0:
                self.conflict = (
                    f"congruence: lhs is always a multiple of {g}, "
                    f"but rhs {rhs} is not"
                )
                return
            self._add_le(clean, rhs)
            if self.conflict is None:
                self._add_le([(-a, v) for a, v in clean], -rhs)
        elif sense == "<=":
            self._add_le(clean, rhs)
        elif sense == ">=":
            self._add_le([(-a, v) for a, v in clean], -rhs)
        else:
            raise ValueError(f"unsupported sense {sense!r}")

    def _add_le(self, terms: list[tuple[int, str]], rhs: int) -> None:
        if not terms:
            if rhs < 0:  # 0 <= rhs 为假
                self.conflict = f"constant contradiction: 0 <= {rhs} is false"
            return
        idx = len(self.constraints)
        con = Constraint(tuple(terms), rhs)
        self.constraints.append(con)
        self.trail.append(("con", idx))
        for _, v in con.terms:
            self.watch[v].add(idx)
        self._enqueue(idx)
        self.propagate()

    # ------------------------------------------------------------------ 传播
    def _enqueue(self, idx: int) -> None:
        if idx not in self.queued:
            self.queued.add(idx)
            self.queue.append(idx)

    def propagate(self) -> None:
        """队列驱动，传播至固定点或冲突。"""
        while self.queue and self.conflict is None:
            idx = self.queue.popleft()
            self.queued.discard(idx)
            self._propagate_one(idx)

    def _propagate_one(self, idx: int) -> None:
        con = self.constraints[idx]
        bound = con.rhs
        if self.use_congruence and con.g > 1:
            # lhs 恒为 g 的倍数：sum <= rhs 等价于 sum <= g*floor(rhs/g)。
            bound = con.g * (con.rhs // con.g)

        min_sum = 0
        for a, v in con.terms:
            min_sum += a * self.lo[v] if a > 0 else a * self.hi[v]
        if min_sum > bound:
            self.conflict = (
                f"constraint {idx} ({con}): minimal lhs {min_sum} > rhs {bound}"
            )
            return

        for a, v in con.terms:
            own_min = a * self.lo[v] if a > 0 else a * self.hi[v]
            rest_min = min_sum - own_min
            # a*x <= bound - rest_min
            if a > 0:
                new_hi = floor_div(bound - rest_min, a)
                if new_hi < self.hi[v]:
                    self._set_hi(v, new_hi, idx)
            else:
                new_lo = ceil_div(bound - rest_min, a)
                if new_lo > self.lo[v]:
                    self._set_lo(v, new_lo, idx)
            if self.conflict is not None:
                return

    def _set_lo(self, v: str, val: int, idx: int) -> None:
        if val > self.hi[v]:
            self.conflict = (
                f"empty domain for {v!r}: lower bound {val} from constraint "
                f"{idx} exceeds upper bound {self.hi[v]}"
            )
            return
        self._trail_domain(v)
        self.lo[v] = val
        self.why_lo[v] = (idx, f"{v} >= {val} by constraint {idx}: {self.constraints[idx]}")
        for w in self.watch[v]:
            self._enqueue(w)

    def _set_hi(self, v: str, val: int, idx: int) -> None:
        if val < self.lo[v]:
            self.conflict = (
                f"empty domain for {v!r}: upper bound {val} from constraint "
                f"{idx} below lower bound {self.lo[v]}"
            )
            return
        self._trail_domain(v)
        self.hi[v] = val
        self.why_hi[v] = (idx, f"{v} <= {val} by constraint {idx}: {self.constraints[idx]}")
        for w in self.watch[v]:
            self._enqueue(w)

    def _trail_domain(self, v: str) -> None:
        self.trail.append(
            ("dom", v, self.lo[v], self.hi[v], self.why_lo[v], self.why_hi[v])
        )

    # ------------------------------------------------------------------ 回滚
    def checkpoint(self) -> int:
        """返回一个标记，之后可用 undo(mark) 恢复到当前状态。"""
        return len(self.trail)

    def undo(self, mark: int) -> None:
        """撤回 mark 之后添加的约束与所有域收紧，恢复域与解释。"""
        while len(self.trail) > mark:
            entry = self.trail.pop()
            if entry[0] == "dom":
                _, v, lo, hi, why_lo, why_hi = entry
                self.lo[v] = lo
                self.hi[v] = hi
                self.why_lo[v] = why_lo
                self.why_hi[v] = why_hi
            else:  # ("con", idx)
                idx = entry[1]
                con = self.constraints.pop()
                assert idx == len(self.constraints)
                for _, v in con.terms:
                    self.watch[v].discard(idx)
                self.original.pop()
        self.queue.clear()
        self.queued.clear()
        self.conflict = None

    # ------------------------------------------------------------------ 求解
    def solve(self) -> str:
        """返回 INFEASIBLE / SATISFIABLE / UNKNOWN。

        本求解器不完备：只有所有变量都收敛为单点且通过独立检查器时
        才声称 SATISFIABLE；否则只声称 UNKNOWN，绝不声称已完整求解。
        """
        self.propagate()
        if self.conflict is not None:
            return "INFEASIBLE"
        if all(self.lo[v] == self.hi[v] for v in self.lo):
            assignment = {v: self.lo[v] for v in self.lo}
            if not check_assignment(self.original, assignment):
                raise AssertionError("independent checker rejected assignment")
            return "SATISFIABLE"
        return "UNKNOWN"

    def assignment(self) -> dict[str, int]:
        return {v: self.lo[v] for v in self.lo}

    def explain(self, v: str) -> str:
        return f"lo: {self.why_lo[v][1]} | hi: {self.why_hi[v][1]}"


def check_assignment(original, assignment: dict[str, int]) -> bool:
    """独立检查器：直接在原始约束（含原始 sense）上验证赋值。"""
    for terms, rhs, sense in original:
        total = 0
        for a, v in terms:
            total += a * assignment[v]
        if sense == "<=" and not total <= rhs:
            return False
        if sense == ">=" and not total >= rhs:
            return False
        if sense == "==" and not total == rhs:
            return False
    return True


def enumerate_solutions(original, domains: dict[str, tuple[int, int]]):
    """精确整数参考枚举：穷举有限域，返回所有满足原始约束的赋值。"""
    names = sorted(domains)
    solutions: list[dict[str, int]] = []

    def rec(i: int, assn: dict[str, int]) -> None:
        if i == len(names):
            if check_assignment(original, assn):
                solutions.append(dict(assn))
            return
        v = names[i]
        lo, hi = domains[v]
        for val in range(lo, hi + 1):
            assn[v] = val
            rec(i + 1, assn)

    rec(0, {})
    return solutions
