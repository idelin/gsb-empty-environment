"""对称性验证：用户给定的置换必须先证明是约束与目标的自同构。

验证内容（任一不满足即拒绝，并给出反例）：
  1. 特殊角色变量必须是不动点；
  2. 每个变量与其像的域必须完全相同；
  3. 每条原始约束在置换前后真值一致（枚举相关变量的全部赋值）；
  4. 目标函数（若存在）在置换前后取值一致（枚举全部完整赋值）。

约束反例按"最小"顺序枚举：先比较非默认取值（非 domain[0]）的个数，
再比较各变量在域中的下标元组，因此第一个被发现的反例即最小反例。
"""
from __future__ import annotations

import itertools
from dataclasses import dataclass
from typing import Any, Iterator, Optional

from .csp import Problem
from .permutation import Permutation


class BudgetExceeded(Exception):
    """验证或闭包构造超出预算。"""


@dataclass(frozen=True)
class Counterexample:
    """置换被拒绝的原因。

    kind: "special" | "domain" | "constraint" | "objective"
    assignment: 触发不保持现象的（部分）赋值；domain/special 类为 None。
    """

    kind: str
    detail: str
    constraint: Optional[str] = None
    assignment: Optional[dict[str, Any]] = None


@dataclass(frozen=True)
class Verification:
    ok: bool
    counterexample: Optional[Counterexample] = None


def _ordered_assignments(
    domains: dict[str, tuple[Any, ...]], variables: list[str]
) -> Iterator[dict[str, Any]]:
    """按最小性顺序枚举 variables 的全部赋值。

    排序键 =(非默认取值个数, 各变量域下标元组)，与域值本身是否可比较无关。
    """
    pools = [domains[v] for v in variables]
    combos = itertools.product(*[range(len(p)) for p in pools])
    indexed = sorted(
        combos,
        key=lambda idx: (sum(1 for i in idx if i != 0), idx),
    )
    for idx in indexed:
        yield {v: pools[i][idx[i]] for i, v in enumerate(variables)}


def verify_permutation(
    problem: Problem, perm: Permutation, budget: int = 100_000
) -> Verification:
    """验证 perm 是否为 problem（约束 + 目标）的自同构。

    budget 限制赋值检查的总次数，超出抛出 BudgetExceeded。
    """
    if set(perm.names) != set(problem.names):
        raise ValueError("置换的变量集合与问题不一致")

    # 1. 特殊角色变量不得被交换
    for v in problem.variables:
        if v.special and perm.image(v.name) != v.name:
            return Verification(
                False,
                Counterexample(
                    kind="special",
                    detail=(
                        f"特殊角色变量 {v.name} 被置换为 "
                        f"{perm.image(v.name)}，特殊角色不得被交换"
                    ),
                ),
            )

    # 2. 域保持
    domains = problem.domains
    for n in problem.names:
        if domains[n] != domains[perm.image(n)]:
            return Verification(
                False,
                Counterexample(
                    kind="domain",
                    detail=(
                        f"变量 {n} 的域 {domains[n]} 与其像 "
                        f"{perm.image(n)} 的域 {domains[perm.image(n)]} 不同"
                    ),
                ),
            )

    checks = 0

    def spend() -> None:
        nonlocal checks
        checks += 1
        if checks > budget:
            raise BudgetExceeded(f"自同构验证超过预算 {budget} 次赋值检查")

    # 3. 每条约束保持：只需枚举 scope ∪ perm^{-1}(scope) 上的赋值
    inv = perm.inverse()
    for c in problem.constraints:
        scope = set(c.scope)
        relevant = sorted(scope | {inv.image(s) for s in scope})
        for partial in _ordered_assignments(domains, relevant):
            spend()
            if c.satisfied(partial) != c.satisfied(perm.apply(partial)):
                return Verification(
                    False,
                    Counterexample(
                        kind="constraint",
                        constraint=c.name,
                        assignment=partial,
                        detail=(
                            f"约束 {c.name} 在赋值 {partial} 下不被保持："
                            f"原取值为 {c.satisfied(partial)}，置换后为 "
                            f"{c.satisfied(perm.apply(partial))}"
                        ),
                    ),
                )

    # 4. 目标函数保持（枚举完整赋值）
    if problem.objective is not None:
        for full in _ordered_assignments(domains, list(problem.names)):
            spend()
            if problem.objective(full) != problem.objective(perm.apply(full)):
                return Verification(
                    False,
                    Counterexample(
                        kind="objective",
                        assignment=full,
                        detail=(
                            f"目标函数在赋值 {full} 下不被保持："
                            f"原取值为 {problem.objective(full)}，置换后为 "
                            f"{problem.objective(perm.apply(full))}"
                        ),
                    ),
                )

    return Verification(True)
