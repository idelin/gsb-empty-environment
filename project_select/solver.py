"""Exact solver for the budgeted project-selection problem.

Semantics
---------
- Selecting a project requires selecting all of its dependencies
  (transitively). Selecting a child without its parent is illegal.
- A dependency shared by two selected projects is paid for exactly once
  (cost is summed over the *set* of selected projects).
- Mutually exclusive pairs may never be selected together.
- Dependency cycles are handled explicitly, per `on_cycle`:
    * "group"  : the whole strongly connected component is treated as one
                 atomic group (select all of it or none; cost/profit summed).
    * "reject" : the instance is rejected and the cycle members reported.
- Negative-profit projects are allowed and are selected exactly when a
  profitable dependent pulls them in.

No external solver is used: after cycle condensation the remaining group
graph is a DAG, and the optimum is found by exact branch-and-bound
enumeration over groups (synthetic data is small). All arithmetic is
exact (int / fractions.Fraction).

Result categories (kept separate, per requirements)
---------------------------------------------------
- feasible   : `is_feasible_selection` validates any candidate set.
- optimal    : `solve` returns status "optimal" with the best selection.
- budget undecided : when the optimum does not exhaust the budget AND the
  same profit is achievable without any budget limit, the budget was not
  decisive -> `budget_binding is False`.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .model import Instance

# A budget larger than any synthetic instance can reach: exact
# Fraction arithmetic keeps this safe.
_INFINITE = Fraction(10**30)



# ---------------------------------------------------------------------------
# Strongly connected components (Tarjan, iterative)
# ---------------------------------------------------------------------------

def strongly_connected_components(nodes, edges):
    """Return SCCs of the directed graph (each SCC as a frozenset)."""
    index_of = {}
    lowlink = {}
    on_stack = set()
    stack = []
    result = []
    counter = [0]

    for root in nodes:
        if root in index_of:
            continue
        work = [(root, iter(edges.get(root, ())))]
        index_of[root] = lowlink[root] = counter[0]
        counter[0] += 1
        stack.append(root)
        on_stack.add(root)
        while work:
            node, it = work[-1]
            advanced = False
            for nxt in it:
                if nxt not in index_of:
                    index_of[nxt] = lowlink[nxt] = counter[0]
                    counter[0] += 1
                    stack.append(nxt)
                    on_stack.add(nxt)
                    work.append((nxt, iter(edges.get(nxt, ()))))
                    advanced = True
                    break
                elif nxt in on_stack:
                    lowlink[node] = min(lowlink[node], index_of[nxt])
            if advanced:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                lowlink[parent] = min(lowlink[parent], lowlink[node])
            if lowlink[node] == index_of[node]:
                scc = []
                while True:
                    member = stack.pop()
                    on_stack.discard(member)
                    scc.append(member)
                    if member == node:
                        break
                result.append(frozenset(scc))
    return result


# ---------------------------------------------------------------------------
# Result
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class SolveResult:
    status: str                      # "optimal" | "infeasible" | "rejected_cycle"
    selected: frozenset = frozenset()
    total_cost: Fraction = Fraction(0)
    total_profit: Fraction = Fraction(0)
    budget: Fraction = Fraction(0)
    budget_remaining: Fraction = Fraction(0)
    budget_binding: bool | None = None   # False => budget undecided (slack)
    cycle: tuple | None = None           # members when rejected_cycle
    reason: str | None = None

    @property
    def is_optimal(self) -> bool:
        return self.status == "optimal"


# ---------------------------------------------------------------------------
# Feasibility of a concrete selection (project level)
# ---------------------------------------------------------------------------

def check_selection(instance: Instance, selected) -> list[str]:
    """Return a list of violation messages; empty means feasible."""
    selected = set(selected)
    errors = []
    for pid in selected:
        for dep in instance.projects[pid].deps:
            if dep not in selected:
                errors.append(
                    f"{pid!r} selected without its dependency {dep!r}")
    for pair in instance.mutex:
        if pair <= selected:
            a, b = sorted(pair)
            errors.append(f"mutually exclusive pair selected: {a!r}, {b!r}")
    missing = set(instance.required) - selected
    if missing:
        errors.append(f"required projects not selected: {sorted(missing)}")
    cost = sum((instance.projects[p].cost for p in selected), Fraction(0))
    if cost > instance.budget:
        errors.append(f"budget exceeded: cost {cost} > budget {instance.budget}")
    return errors


def is_feasible_selection(instance: Instance, selected) -> bool:
    return not check_selection(instance, selected)


# ---------------------------------------------------------------------------
# Internal: group = one SCC condensed to a super-node
# ---------------------------------------------------------------------------

class _Group:
    __slots__ = ("gid", "members", "cost", "profit", "req_mask",
                 "self_conflicting")

    def __init__(self, gid, members, cost, profit):
        self.gid = gid
        self.members = members          # frozenset of pids
        self.cost = cost                # Fraction
        self.profit = profit            # Fraction
        self.req_mask = 0               # transitive dependency groups (bits)
        self.self_conflicting = False   # mutex inside the group


def _build_groups(instance: Instance):
    """Condense the dependency graph. Returns (groups, cycle) — cycle is
    a tuple of pids if a non-trivial SCC exists, else None."""
    edges = {pid: instance.projects[pid].deps for pid in instance.projects}
    sccs = strongly_connected_components(list(instance.projects), edges)
    cycle = None
    for scc in sccs:
        if len(scc) > 1:
            cycle = tuple(sorted(scc))
            break
        (only,) = scc
        if only in edges[only]:  # self-dependency is a cycle too
            cycle = (only,)
            break

    group_of = {}
    groups = []
    for scc in sccs:
        gid = len(groups)
        cost = sum((instance.projects[p].cost for p in scc), Fraction(0))
        profit = sum((instance.projects[p].profit for p in scc), Fraction(0))
        groups.append(_Group(gid, scc, cost, profit))
        for pid in scc:
            group_of[pid] = gid

    # Direct dependency edges between groups.
    direct = [set() for _ in groups]
    for pid, project in instance.projects.items():
        g = group_of[pid]
        for dep in project.deps:
            gd = group_of[dep]
            if gd != g:
                direct[g].add(gd)

    # Transitive closure over the (now acyclic) group DAG.
    for g in groups:
        seen = set()
        stack = list(direct[g.gid])
        while stack:
            x = stack.pop()
            if x in seen:
                continue
            seen.add(x)
            stack.extend(direct[x])
        for x in seen:
            g.req_mask |= 1 << x

    # Mutex pairs -> group-level conflicts.
    mutex_group_pairs = set()
    for pair in instance.mutex:
        a, b = sorted(pair)
        ga, gb = group_of[a], group_of[b]
        if ga == gb:
            groups[ga].self_conflicting = True
        else:
            mutex_group_pairs.add((min(ga, gb), max(ga, gb)))

    return groups, group_of, mutex_group_pairs, cycle


# ---------------------------------------------------------------------------
# Exact branch-and-bound over groups
# ---------------------------------------------------------------------------

def _best_selection(groups, mutex_group_pairs, budget, forced_mask,
                    disabled_mask):
    """Return (profit, cost, mask) of the best feasible group selection,
    or None if the forced set alone is infeasible. Exact enumeration with
    cost and optimistic-profit pruning."""
    n = len(groups)
    order = sorted(range(n), key=lambda g: -groups[g].profit)

    # Suffix bound: sum of positive profits of not-yet-considered groups.
    suffix_pos = [Fraction(0)] * (n + 1)
    for i in range(n - 1, -1, -1):
        p = groups[order[i]].profit
        suffix_pos[i] = suffix_pos[i + 1] + (p if p > 0 else 0)

    def conflicts(mask):
        for ga, gb in mutex_group_pairs:
            if mask & (1 << ga) and mask & (1 << gb):
                return True
        return False

    def closed(mask):
        m = mask
        while m:
            bit = m & (-m)
            g = bit.bit_length() - 1
            if groups[g].req_mask & ~mask:
                return False
            m ^= bit
        return True

    # Validate the forced part first.
    forced_full = forced_mask
    changed = True
    while changed:  # expand forced set by dependency closure
        changed = False
        m = forced_full
        while m:
            bit = m & (-m)
            g = bit.bit_length() - 1
            add = groups[g].req_mask & ~forced_full
            if add:
                forced_full |= add
                changed = True
            m ^= bit
    if forced_full & disabled_mask or conflicts(forced_full):
        return None
    forced_cost = sum((groups[g].cost for g in range(n)
                       if forced_full & (1 << g)), Fraction(0))
    if forced_cost > budget:
        return None

    best = None  # (profit, neg_cost, mask)

    def consider(mask, cost, profit):
        nonlocal best
        key = (profit, -cost)
        if best is None or key > (best[0], -best[1]):
            best = (profit, cost, mask)

    def rec(i, mask, cost, profit):
        if cost > budget:
            return
        if best is not None and profit + suffix_pos[i] < best[0]:
            return
        if i == n:
            if closed(mask) and not conflicts(mask):
                consider(mask, cost, profit)
            return
        g = order[i]
        bit = 1 << g
        if mask & bit or disabled_mask & bit:
            rec(i + 1, mask, cost, profit)  # already forced, or disabled
            return
        # Branch 1: take g (only if its requirements can still be met).
        rec(i + 1, mask | bit, cost + groups[g].cost, profit + groups[g].profit)
        # Branch 2: skip g.
        rec(i + 1, mask, cost, profit)

    forced_profit = sum((groups[g].profit for g in range(n)
                         if forced_full & (1 << g)), Fraction(0))
    rec(0, forced_full, forced_cost, forced_profit)
    if best is None:
        return None
    return best[0], best[1], best[2]


# ---------------------------------------------------------------------------
# Public solver
# ---------------------------------------------------------------------------

def solve(instance: Instance, on_cycle: str = "group") -> SolveResult:
    """Solve exactly. `on_cycle` is "group" (merge cycle into one atomic
    group) or "reject" (refuse the instance, reporting the cycle)."""
    if on_cycle not in ("group", "reject"):
        raise ValueError("on_cycle must be 'group' or 'reject'")

    groups, group_of, mutex_pairs, cycle = _build_groups(instance)

    if cycle is not None and on_cycle == "reject":
        return SolveResult(
            status="rejected_cycle",
            budget=instance.budget,
            budget_remaining=instance.budget,
            cycle=cycle,
            reason=f"dependency cycle rejected: {list(cycle)}")

    disabled = 0
    for g in groups:
        if g.self_conflicting:
            disabled |= 1 << g.gid

    forced = 0
    for pid in instance.required:
        forced |= 1 << group_of[pid]

    found = _best_selection(groups, mutex_pairs, instance.budget,
                            forced, disabled)
    if found is None:
        return SolveResult(
            status="infeasible",
            budget=instance.budget,
            budget_remaining=instance.budget,
            reason="no feasible selection (required projects conflict, "
                   "exceed the budget, or the empty selection is disallowed)")

    profit, cost, mask = found

    # Budget decisiveness: would the answer change with unlimited budget?
    unbounded = _best_selection(groups, mutex_pairs, _INFINITE,
                                forced, disabled)
    budget_binding = unbounded is not None and unbounded[0] > profit

    selected = frozenset(
        pid for g in groups if mask & (1 << g.gid) for pid in g.members)
    return SolveResult(
        status="optimal",
        selected=selected,
        total_cost=cost,
        total_profit=profit,
        budget=instance.budget,
        budget_remaining=instance.budget - cost,
        budget_binding=budget_binding)

