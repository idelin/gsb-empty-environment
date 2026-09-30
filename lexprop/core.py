"""有限域字典序约束的支持过滤传播器（纯标准库，不调用 SAT/SMT）。

语义（与 Python 列表比较一致，长度规则明确）：
  - 非严格 lex_leq(x, y)：元组 x <= y（字典序，短前缀更小）。
  - 严格   lex_less(x, y)：元组 x < y。
  - 相同向量只满足非严格；空向量 [] <= [] 成立，[] < [] 不成立；
    [] 是任何向量的前缀，故 [] < [..] 成立（长度规则一致）。

传播基于“支持”而非仅比较当前上下界：保留值 v 当且仅当存在一个满足
原约束的完整赋值使该变量取 v。首差位置 k 的情形分析配合并查集，
统一处理不同长度、共享变量与严格性。
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Dict, Iterable, List, Optional, Sequence, Set, Tuple


class Var:
    """有限域变量，按对象身份区分（同名对象仍是同一变量）。"""
    __slots__ = ("name",)

    def __init__(self, name: str):
        self.name = name

    def __repr__(self) -> str:  # pragma: no cover - 仅为可读性
        return self.name


class BudgetExhausted(Exception):
    """传播预算耗尽：尚未达到传播固定点。"""


class Budget:
    def __init__(self, steps: int):
        self.remaining = steps

    def consume(self, n: int = 1) -> None:
        if self.remaining < n:
            raise BudgetExhausted("预算不足，尚未达到传播固定点")
        self.remaining -= n


class DomainWipeout(Exception):
    def __init__(self, var: Optional[Var]):
        super().__init__(f"变量 {var} 的域被删空" if var else "约束不可行")
        self.var = var


@dataclass(frozen=True)
class _PrefixInfo:
    # ok[k]：前 k 位强制相等（x_j == y_j, j < k）是否一致可行
    ok: Tuple[bool, ...]
    # boundary[k] = (same, dom_x, dom_y)：在前 k 位相等的前提下，
    # 位置 k 两端所属等式分量的（交集后）域；same 表示两端被强制相等
    boundary: Tuple[Tuple[bool, frozenset, frozenset], ...]


@dataclass
class Proof:
    """一条删值证明：引用原约束，给出每个首差位置 k 不可行的原因。"""
    constraint: str
    var: str
    value: Any
    reasons: Tuple[Tuple[int, tuple], ...]   # (k, reason)
    domains_snapshot: Tuple[Tuple[str, frozenset], ...]

    _REASON_TEXT = {
        "prefix_inconsistent": "前缀强制相等不可行（等式分量的域交为空）",
        "forced_equal": "两端变量被前缀等式/共享强制相等，不能作为首差",
        "no_difference": "无法形成 x_k < y_k",
        "length_rule": "公共前缀全等后按长度规则不满足",
    }

    def render(self) -> str:
        lines = [
            f"删值证明：约束 {self.constraint} 中，变量 {self.var} 取值 "
            f"{self.value!r} 无支持，理由如下："
        ]
        snap = ", ".join(f"{n}={sorted(d)}" for n, d in self.domains_snapshot)
        lines.append(f"  当时域：{snap}")
        for k, reason in self.reasons:
            kind = reason[0]
            text = self._REASON_TEXT.get(kind, kind)
            detail = ""
            if kind == "no_difference":
                detail = f"（本侧最小 {reason[2]} >= 对侧最大 {reason[3]}）"
            elif kind == "length_rule":
                detail = f"（n={reason[1]}, m={reason[2]}, strict={reason[3]}）"
            lines.append(f"  首差位置 k={k}: {text}{detail}")
        return "\n".join(lines)

    def verify(self, constraint: "LexConstraint") -> bool:
        """独立重算：在快照域下该值确实无任何首差位置的支持。"""
        var = constraint.var_by_name(self.var)
        domains = {v: set(d) for v, d in
                   ((constraint.var_by_name(n), d) for n, d in self.domains_snapshot)}
        override = {var: frozenset([self.value])}
        for k, reason in self.reasons:
            viable, again = constraint.case_reason(k, domains, override)
            if viable or again != reason:
                return False
        return True


class LexConstraint:
    """lex_leq / lex_less 约束，支持不同长度与共享变量。"""

    def __init__(self, xs: Sequence[Var], ys: Sequence[Var],
                 strict: bool = False, name: Optional[str] = None):
        self.xs: List[Var] = list(xs)
        self.ys: List[Var] = list(ys)
        self.strict = bool(strict)
        self.name = name or f"lex{len(self.xs)}x{len(self.ys)}"
        seen: Set[int] = set()
        self._vars: List[Var] = []
        for v in self.xs + self.ys:
            if id(v) not in seen:
                seen.add(id(v))
                self._vars.append(v)
        # 前缀支持缓存：域签名 -> _PrefixInfo；由 Solver 的 push/pop 恢复
        self._cache: Dict[tuple, _PrefixInfo] = {}

    # ---------- 基本访问 ----------

    @property
    def vars(self) -> List[Var]:
        return list(self._vars)

    def var_by_name(self, name: str) -> Var:
        for v in self._vars:
            v: Var
            if v.name == name:
                return v
        raise KeyError(name)

    def expr(self) -> str:
        op = "lex_less" if self.strict else "lex_leq"
        lhs = ",".join(v.name for v in self.xs)
        rhs = ",".join(v.name for v in self.ys)
        return f"{op}([{lhs}], [{rhs}])"

    # ---------- 前缀支持缓存 ----------

    @staticmethod
    def _eff(domains: Dict[Var, Set[Any]], override: Optional[Dict[Var, frozenset]],
             v: Var) -> Iterable[Any]:
        if override is not None and v in override:
            return override[v]
        return domains[v]

    def _signature(self, domains: Dict[Var, Set[Any]],
                   override: Optional[Dict[Var, frozenset]]) -> tuple:
        return tuple(frozenset(self._eff(domains, override, v)) for v in self._vars)

    def prefix_info(self, domains: Dict[Var, Set[Any]],
                    override: Optional[Dict[Var, frozenset]] = None) -> _PrefixInfo:
        """计算（并缓存）前缀相等可行性与各位置的等式分量域。"""
        sig = self._signature(domains, override)
        hit = self._cache.get(sig)
        if hit is not None:
            return hit
        L = min(len(self.xs), len(self.ys))
        parent = {v: v for v in self._vars}
        comp = {v: frozenset(self._eff(domains, override, v)) for v in self._vars}

        def find(a: Var) -> Var:
            root = a
            while parent[root] is not root:
                root = parent[root]
            while parent[a] is not root:
                parent[a], a = root, parent[a]
            return root

        consistent = all(comp[v] for v in self._vars)
        ok = [consistent]
        boundary = []
        for k in range(L):
            rx, ry = find(self.xs[k]), find(self.ys[k])
            boundary.append((rx is ry, comp[rx], comp[ry]))
            if rx is not ry:
                merged = comp[rx] & comp[ry]
                parent[ry] = rx
                comp[rx] = merged
                if not merged:
                    consistent = False
            ok.append(consistent)
        info = _PrefixInfo(tuple(ok), tuple(boundary))
        self._cache[sig] = info
        return info

    # ---------- 首差位置情形分析 ----------

    def case_reason(self, k: int, domains: Dict[Var, Set[Any]],
                    override: Optional[Dict[Var, frozenset]] = None
                    ) -> Tuple[bool, tuple]:
        """首差位置 k 是否可行；不可行时给出机器可复核的原因。"""
        n, m = len(self.xs), len(self.ys)
        L = min(n, m)
        info = self.prefix_info(domains, override)
        if not info.ok[k]:
            return False, ("prefix_inconsistent", k)
        if k == L:
            if n < m:
                return True, ("proper_prefix",)
            if n == m and not self.strict:
                return True, ("equal_vectors_nonstrict",)
            return False, ("length_rule", n, m, self.strict)
        same, dx, dy = info.boundary[k]
        if same:
            return False, ("forced_equal", k)
        if min(dx) < max(dy):
            return True, ("difference", k, min(dx), max(dy))
        return False, ("no_difference", k, min(dx), max(dy))

    def is_feasible(self, domains: Dict[Var, Set[Any]],
                    override: Optional[Dict[Var, frozenset]] = None) -> bool:
        L = min(len(self.xs), len(self.ys))
        return any(self.case_reason(k, domains, override)[0]
                   for k in range(L + 1))

    def has_support(self, domains: Dict[Var, Set[Any]], var: Var, value: Any) -> bool:
        if value not in domains[var]:
            return False
        return self.is_feasible(domains, {var: frozenset([value])})

    # ---------- 支持过滤传播 ----------

    def propagate(self, domains: Dict[Var, Set[Any]], budget: Budget,
                  proofs: List[Proof]) -> bool:
        """删除所有无支持的值；返回是否有域发生变化。"""
        changed = False
        L = min(len(self.xs), len(self.ys))
        for var in self._vars:
            for value in sorted(domains[var]):
                budget.consume()
                if self.has_support(domains, var, value):
                    continue
                override = {var: frozenset([value])}
                reasons = tuple(
                    (k, self.case_reason(k, domains, override)[1])
                    for k in range(L + 1))
                snapshot = tuple((v.name, frozenset(domains[v]))
                                 for v in self._vars)
                proofs.append(Proof(self.expr(), var.name, value,
                                    reasons, snapshot))
                domains[var] = set(domains[var]) - {value}
                changed = True
                if not domains[var]:
                    raise DomainWipeout(var)
        return changed

    # ---------- 前缀相等 / 首差位置报告 ----------

    def prefix_stats(self, domains: Dict[Var, Set[Any]]) -> Tuple[int, Optional[int]]:
        """返回 (alpha, beta)：已强制相等的前缀长度与首个可能首差位置。"""
        L = min(len(self.xs), len(self.ys))
        alpha = 0
        while (alpha < L and len(domains[self.xs[alpha]]) == 1
               and domains[self.xs[alpha]] == domains[self.ys[alpha]]):
            alpha += 1
        beta = None
        for i in range(alpha, L):
            if min(domains[self.xs[i]]) < max(domains[self.ys[i]]):
                beta = i
                break
        return alpha, beta
