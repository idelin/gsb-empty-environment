"""Group closure of permutation generators, constructed under a budget."""

from __future__ import annotations

from collections import deque

from .permutations import Perm, compose, identity, key, normalize


class BudgetExceeded(Exception):
    """Raised when the closure would grow beyond the given budget."""

    def __init__(self, budget: int, reached: int):
        super().__init__(
            f"group closure exceeded budget {budget} "
            f"(already reached {reached} distinct elements)"
        )
        self.budget = budget
        self.reached = reached


def closure(generators, variables, budget: int = 1000) -> list:
    """BFS closure of ``generators``; raises BudgetExceeded beyond ``budget``."""
    if budget < 1:
        raise ValueError("budget must be at least 1 (the identity)")
    gens = [normalize(g, variables) for g in generators]
    idp = identity(variables)
    seen = {key(idp, variables): idp}
    order = [idp]
    queue = deque([idp])
    while queue:
        g = queue.popleft()
        for gen in gens:
            h = compose(g, gen)
            hkey = key(h, variables)
            if hkey not in seen:
                if len(seen) >= budget:
                    raise BudgetExceeded(budget, len(seen))
                seen[hkey] = h
                order.append(h)
                queue.append(h)
    return order
