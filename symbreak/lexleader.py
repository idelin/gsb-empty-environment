"""字典序代表（lex-leader）约束生成。

原则：只为"已证明"的对称性生成约束。
  * declare_exchangeable：声明一组变量完全可交换（全对称群 S_n）。
    先验证全部相邻对换都是自同构（它们生成 S_n），通过后才追加
    链式字典序约束 x1 <= x2 <= ... <= xn（S_n 的标准 lex-leader）。
  * lex_leader_constraint：为单个已验证的置换生成 x <=lex p(x)。
    未通过验证的置换一律拒绝，绝不强行排序。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional

from .csp import Constraint, Problem
from .group import group_closure
from .permutation import Permutation
from .verify import Counterexample, verify_permutation


@dataclass
class ExchangeableResult:
    ok: bool
    group_vars: tuple[str, ...]
    constraints: list[Constraint] = field(default_factory=list)
    group: list[Permutation] = field(default_factory=list)
    counterexample: Optional[Counterexample] = None
    reason: str = ""


def _check_ordered(domain: tuple, var: str) -> None:
    try:
        if not all(domain[i] <= domain[i + 1] for i in range(len(domain) - 1)):
            raise ValueError(
                f"变量 {var} 的域 {domain} 不是升序，无法施加字典序约束"
            )
    except TypeError as exc:
        raise ValueError(f"变量 {var} 的域值不可比较，无法施加字典序约束") from exc


def declare_exchangeable(
    problem: Problem,
    group_vars: list[str],
    verify_budget: int = 100_000,
    closure_budget: int = 100_000,
) -> ExchangeableResult:
    """声明并验证一组变量完全可交换，成功后向 problem 追加链式字典序约束。

    拒绝情形（ok=False，不修改 problem）：
      * 组内含未知变量或特殊角色变量；
      * 组内变量域不完全相同；
      * 某相邻对换不是约束/目标的自同构（附最小反例）。
    """
    names = set(problem.names)
    group = tuple(group_vars)
    if len(set(group)) != len(group):
        raise ValueError("可交换组内变量重复")
    unknown = [v for v in group if v not in names]
    if unknown:
        return ExchangeableResult(False, group, reason=f"未知变量: {unknown}")

    special = [v.name for v in problem.variables if v.special and v.name in group]
    if special:
        return ExchangeableResult(
            False, group, reason=f"特殊角色变量不得参与交换: {special}"
        )

    domains = problem.domains
    base = domains[group[0]]
    for v in group:
        if domains[v] != base:
            return ExchangeableResult(
                False,
                group,
                reason=(
                    f"变量 {v} 的域 {domains[v]} 与 {group[0]} 的域 "
                    f"{base} 不同，拒绝对称声明"
                ),
            )
    if len(group) > 1:
        _check_ordered(base, group[0])

    # 验证全部相邻对换（生成 S_n）；任何一个失败即拒绝整组声明
    transpositions = [
        Permutation.transposition(problem.names, group[i], group[i + 1])
        for i in range(len(group) - 1)
    ]
    for t in transpositions:
        res = verify_permutation(problem, t, budget=verify_budget)
        if not res.ok:
            return ExchangeableResult(
                False, group, counterexample=res.counterexample,
                reason=f"对换 {t} 不是自同构",
            )

    constraints = []
    for i in range(len(group) - 1):
        a, b = group[i], group[i + 1]
        constraints.append(
            Constraint(
                name=f"lex:{a}<={b}",
                scope=(a, b),
                predicate=lambda x, y: x <= y,
            )
        )
    problem.lex_constraints.extend(constraints)
    closure = group_closure(transpositions, problem.names, budget=closure_budget)
    return ExchangeableResult(True, group, constraints=constraints, group=closure)


def lex_leader_constraint(
    problem: Problem,
    perm: Permutation,
    verify_budget: int = 100_000,
) -> Constraint:
    """为单个置换生成 lex-leader 约束 x <=lex p(x)（按 problem.names 顺序）。

    perm 必须先通过自同构验证，否则抛出 ValueError —— 只使用已证对称。
    返回的约束由调用方决定是否加入 problem.lex_constraints。
    """
    res = verify_permutation(problem, perm, budget=verify_budget)
    if not res.ok:
        raise ValueError(
            f"置换未通过自同构验证，拒绝生成字典序约束: {res.counterexample}"
        )
    order = list(problem.names)
    inv = perm.inverse()
    inv_pos = [order.index(inv.image(n)) for n in order]

    def predicate(*values: object) -> bool:
        original = list(values)
        permuted = [values[i] for i in inv_pos]  # (p·x)[n] = x[p^{-1}(n)]
        return original <= permuted

    return Constraint(
        name=f"lex-leader:{perm!r}",
        scope=tuple(order),
        predicate=predicate,
    )
