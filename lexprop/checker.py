"""独立检查器：不依赖传播器内部逻辑，直接按原始语义枚举/判定。

语义即 Python 列表比较（字典序 + 前缀长度规则）：
  strict     -> list(a) <  list(b)
  非 strict  -> list(a) <= list(b)
"""
from __future__ import annotations

import itertools
from typing import Any, Dict, List, Sequence, Set, Tuple

from .core import LexConstraint, Var


def check_assignment(xs: Sequence[Var], ys: Sequence[Var], strict: bool,
                     assignment: Dict[Var, Any]) -> bool:
    """独立判定一个完整赋值是否满足原约束。"""
    a = [assignment[v] for v in xs]
    b = [assignment[v] for v in ys]
    return (a < b) if strict else (a <= b)


def ordered_vars(constraints: Sequence[LexConstraint]) -> List[Var]:
    seen: Set[int] = set()
    out: List[Var] = []
    for c in constraints:
        for v in c.vars:
            if id(v) not in seen:
                seen.add(id(v))
                out.append(v)
    return out


def enumerate_supported_values(constraints: Sequence[LexConstraint],
                               domains: Dict[Var, Set[Any]]
                               ) -> Tuple[Dict[Var, Set[Any]], int]:
    """全赋值枚举：返回 (每个变量的支持值集合, 解的个数)。

    保留域恰好应等于这里算出的支持值集合（GAC 精确性判据）。
    """
    vars_ = ordered_vars(constraints)
    supported: Dict[Var, Set[Any]] = {v: set() for v in vars_}
    count = 0
    pools = [sorted(domains[v]) for v in vars_]
    for combo in itertools.product(*pools):
        assignment = dict(zip(vars_, combo))
        if all(check_assignment(c.xs, c.ys, c.strict, assignment)
               for c in constraints):
            count += 1
            for v in vars_:
                supported[v].add(assignment[v])
    return supported, count
