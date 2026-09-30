"""置换群闭包：按预算构造生成元的群闭包。"""
from __future__ import annotations

from typing import Iterable

from .permutation import Permutation
from .verify import BudgetExceeded


def group_closure(
    generators: Iterable[Permutation],
    names: Iterable[str],
    budget: int = 100_000,
) -> list[Permutation]:
    """BFS 构造生成元的群闭包。

    群元素个数超过 budget 时抛出 BudgetExceeded，调用方可据此
    决定放弃对称破缺或提高预算。返回按 repr 排序的确定序列。
    """
    names = tuple(names)
    gens = list(generators)
    identity = Permutation.identity(names)
    seen: set[Permutation] = {identity}
    frontier = [identity]
    while frontier:
        current = frontier.pop()
        for gen in gens:
            nxt = current.compose(gen)
            if nxt not in seen:
                seen.add(nxt)
                frontier.append(nxt)
                if len(seen) > budget:
                    raise BudgetExceeded(
                        f"群闭包超过预算 {budget} 个元素（已发现 {len(seen)} 个）"
                    )
    return sorted(seen, key=repr)
