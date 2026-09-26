"""Budgeted project portfolio selection with dependencies and mutual exclusion.

Rules implemented
-----------------
* Selecting a project implies selecting every (transitive) dependency.
* Two projects sharing a dependency pay that dependency's cost only once.
* Mutually exclusive project groups: at most one member may be selected.
* A dependency with negative value may be pulled in by a valuable dependent.
* Dependency cycles are handled by an explicit policy:
    - "group":  each strongly connected component is taken/rejected as a whole;
    - "reject": a cyclic instance is rejected with CycleError.

Numbers may be ints, floats (decimal strings are safer), Fractions or rational
strings such as "1/3"; internally every number is exact (int or Fraction).
No external solver is used: selection is exact enumeration with dependency
closure propagation and branch-and-bound pruning.

The three questions from the task are kept separate:
feasibility (feasible / infeasible), optimality (optimal within a budget) and
the budget-undecided case (full cost/value Pareto frontier).
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from enum import Enum
from typing import Iterable, Optional


class Status(str, Enum):
    FEASIBLE = "FEASIBLE"
    INFEASIBLE = "INFEASIBLE"
    OPTIMAL = "OPTIMAL"
    OVER_BUDGET = "OVER_BUDGET"
    UNDECIDED = "UNDECIDED"
    INVALID_CYCLE = "INVALID_CYCLE"


class CycleError(ValueError):
    """Raised when a dependency cycle exists under policy='reject'."""


# --------------------------------------------------------------------------- #
# Result containers
# --------------------------------------------------------------------------- #

@dataclass(frozen=True)
class FrontierPoint:
    cost: object
    value: object
    projects: tuple


@dataclass(frozen=True)
class FeasibilityResult:
    status: Status
    feasible: bool
    reason: str = ""
    selected: tuple = ()


@dataclass(frozen=True)
class OptimizationResult:
    status: Status
    projects: tuple
    cost: object
    value: object
    reason: str = ""
    frontier: tuple = ()


# --------------------------------------------------------------------------- #
# Number handling
# --------------------------------------------------------------------------- #

def _num(x) -> object:
    if isinstance(x, bool):
        raise TypeError("boolean is not a valid number")
    if isinstance(x, int):
        return x
    if isinstance(x, Fraction):
        return x
    if isinstance(x, float):
        raise TypeError("floats are ambiguous; use int, Fraction or 'p/q' strings")
    if isinstance(x, str):
        text = x.strip()
        if "/" in text:
            p, q = text.split("/", 1)
            return Fraction(int(p), int(q))
        value = Fraction(text)
        if value.denominator == 1:
            return value.numerator
        return value
    raise TypeError(f"unsupported numeric value: {x!r}")


# --------------------------------------------------------------------------- #
# Compilation: projects -> atoms (SCCs) with masks
# --------------------------------------------------------------------------- #

@dataclass
class _Atom:
    ids: list
    cost: object
    value: object
    req_mask: int = 0          # atoms of direct dependencies
    pair_mask: int = 0         # atoms mutually exclusive with this one


@dataclass
class _Model:
    projects: dict
    atoms: list
    target_atoms: tuple
    cost: list
    value: list
    req_mask: list
    pair_mask: list


def _tarjan_succ(nodes, succ):
    index = {}
    low = {}
    stack = []
    on_stack = set()
    counter = [0]
    components = []

    def strong(v):
        index[v] = counter[0]
        low[v] = counter[0]
        counter[0] += 1
        stack.append(v)
        on_stack.add(v)
        for w in succ(v):
            if w not in index:
                strong(w)
                low[v] = min(low[v], low[w])
            elif w in on_stack:
                low[v] = min(low[v], index[w])
        if low[v] == index[v]:
            comp = []
            while True:
                w = stack.pop()
                on_stack.discard(w)
                comp.append(w)
                if w == v:
                    break
            components.append(comp)

    for v in nodes:
        if v not in index:
            strong(v)
    return components


def compile_projects(projects, required=(), mutex_groups=(),
                     cycle_policy="group") -> _Model:
    """Validate input and collapse dependency cycles into atomic groups."""
    if cycle_policy not in ("group", "reject"):
        raise ValueError("cycle_policy must be 'group' or 'reject'")
    if not isinstance(projects, dict):
        raise TypeError("projects must be a dict {id: {'cost':..., 'value':...}}")

    data = {}
    for pid, spec in projects.items():
        if not isinstance(spec, dict):
            raise TypeError(f"project {pid!r} must map to a dict")
        try:
            cost = _num(spec["cost"])
            value = _num(spec["value"])
        except KeyError as exc:
            raise KeyError(f"project {pid!r} is missing field {exc}") from None
        if cost < 0:
            raise ValueError(f"project {pid!r} has negative cost")
        deps = spec.get("deps", ())
        if isinstance(deps, str):
            raise TypeError(f"project {pid!r}: deps must be an iterable of ids")
        deps = tuple(deps)
        if len(set(deps)) != len(deps):
            raise ValueError(f"project {pid!r} lists a duplicate dependency")
        for dep in deps:
            if dep not in projects:
                raise KeyError(f"project {pid!r} depends on unknown project {dep!r}")
            if dep == pid:
                raise ValueError(f"project {pid!r} depends on itself")
        data[pid] = (cost, value, deps)

    req_list = tuple(required)
    for r in req_list:
        if r not in data:
            raise KeyError(f"required project {r!r} does not exist")
    if len(set(req_list)) != len(req_list):
        raise ValueError("required contains duplicates")

    groups = []
    seen_members = {}
    for gi, group in enumerate(mutex_groups):
        members = tuple(group)
        if len(set(members)) != len(members):
            raise ValueError(f"mutex group {gi} contains a duplicate member")
        for m in members:
            if m not in data:
                raise KeyError(f"mutex group {gi} references unknown project {m!r}")
            if m in seen_members:
                raise ValueError(
                    f"project {m!r} appears in multiple mutex groups "
                    f"({seen_members[m]} and {gi})"
                )
            seen_members[m] = gi
        groups.append(members)

    ids = list(data)
    succ = lambda v: data[v][2]
    components = _tarjan_succ(ids, succ)

    cyclic = [c for c in components if len(c) > 1]
    if cyclic:
        if cycle_policy == "reject":
            names = sorted(tuple(sorted(c)) for c in cyclic)
            raise CycleError(f"dependency cycle(s) detected: {names}")

    atom_of = {}
    atoms = []
    for comp in components:
        members = sorted(comp, key=ids.index)
        cost = sum((data[m][0] for m in members), 0)
        value = sum((data[m][1] for m in members), 0)
        atom = _Atom(ids=members, cost=cost, value=value)
        idx = len(atoms)
        atoms.append(atom)
        for m in members:
            atom_of[m] = idx

    n = len(atoms)
    for atom in atoms:
        deps = set()
        for m in atom.ids:
            deps.update(atom_of[d] for d in data[m][2])
        deps.discard(atoms.index(atom))
        atom.req_mask = sum(1 << d for d in deps)

    for group in groups:
        involved = sorted({atom_of[m] for m in group})
        for m in group:
            ai = atom_of[m]
            if any(atom_of[other] == ai for other in group if other != m):
                atoms[ai].pair_mask = -1  # mutex partners inside one SCC
        for a in involved:
            if atoms[a].pair_mask == -1:
                continue
            atoms[a].pair_mask |= sum(
                1 << b for b in involved if b != a)
    for atom in atoms:
        if atom.pair_mask != -1 and atom.pair_mask & atom.req_mask:
            atom.pair_mask = -1  # dead: requires an atom it excludes

    full = (1 << n) - 1
    req_mask = [a.req_mask for a in atoms]
    pair_mask = [a.pair_mask for a in atoms]
    costs = [a.cost for a in atoms]
    values = [a.value for a in atoms]

    target_atoms = tuple(sorted({atom_of[r] for r in req_list}))
    return _Model(projects=data, atoms=atoms, target_atoms=target_atoms,
                  cost=costs, value=values, req_mask=req_mask,
                  pair_mask=pair_mask)


# --------------------------------------------------------------------------- #
# Search engine: propagate include/exclude to a fixed point
# --------------------------------------------------------------------------- #

def _propagate(model, incl, excl, to_incl, to_excl):
    """Close the included set under dependencies and honour exclusion.

    Returns (incl, excl) or None if the branch is contradictory.
    """
    req_mask = model.req_mask
    pair_mask = model.pair_mask
    while to_incl or to_excl:
        while to_incl:
            bit = to_incl.pop()
            a = bit.bit_length() - 1
            if incl & bit:
                continue
            if excl & bit:
                return None
            if pair_mask[a] == -1:
                return None  # atom is internally infeasible
            if incl & pair_mask[a]:
                return None  # mutex partner already included
            incl |= bit
            req = req_mask[a]
            if req & excl:
                return None
            to_incl.update(b for b in _bits(req & ~incl))
            to_excl.update(b for b in _bits(pair_mask[a] & ~excl))
        while to_excl:
            bit = to_excl.pop()
            if excl & bit:
                continue
            if incl & bit:
                return None
            excl |= bit
            # every atom that requires this one must be excluded as well
            for a, rm in enumerate(req_mask):
                ab = 1 << a
                if rm & bit and not (incl | excl) & ab:
                    to_excl.add(ab)
    return incl, excl


def _bits(mask):
    while mask:
        low = mask & -mask
        yield low
        mask -= low


def _initial_state(model):
    incl = 0
    excl = 0
    to_incl = set()
    for a in model.target_atoms:
        to_incl.add(1 << a)
    state = _propagate(model, incl, excl, to_incl, set())
    if state is None:
        return None
    return state


def _branch_atom(incl, excl, n):
    undecided = ~(incl | excl) & ((1 << n) - 1)
    if not undecided:
        return 0
    low = undecided & -undecided
    return low.bit_length() - 1


def _mask_to_projects(model, incl):
    ids = []
    for i, atom in enumerate(model.atoms):
        if incl & (1 << i):
            ids.extend(atom.ids)
    return tuple(sorted(ids))


# --------------------------------------------------------------------------- #
# Public API
# --------------------------------------------------------------------------- #

def _min_nonempty_cost(model):
    state = _initial_state(model)
    if state is None:
        return None
    best = [None]

    def dfs(incl, excl, cur_cost):
        total = (1 << len(model.atoms)) - 1
        undecided = ~(incl | excl) & total
        if best[0] is not None and cur_cost >= best[0]:
            return
        if incl and (best[0] is None or cur_cost < best[0]):
            best[0] = cur_cost
        while undecided:
            low = undecided & -undecided
            a = low.bit_length() - 1
            bit = low
            undecided -= low
            if model.pair_mask[a] == -1:
                continue
            res = _propagate(model, incl, excl, {bit}, set())
            if res is None:
                continue
            ni, ne = res
            add = sum((model.cost[i] for i in _added(incl, ni)), 0)
            dfs(ni, ne, cur_cost + add)

    dfs(state[0], state[1], _state_cost(model, state[0]))
    return best[0]


def _added(old, new):
    diff = new & ~old
    for b in _bits(diff):
        yield b.bit_length() - 1


def _state_cost(model, incl):
    return sum((model.cost[i] for i in range(len(model.atoms))
                if incl & (1 << i)), 0)


def check_feasibility(projects, required=(), mutex_groups=(),
                      cycle_policy="group"):
    """Can the required projects (with all deps) be selected at all?"""
    model = compile_projects(projects, required, mutex_groups, cycle_policy)
    state = _initial_state(model)
    if state is None:
        return FeasibilityResult(Status.INFEASIBLE, False,
                                 "required set is not closed-consistent "
                                 "(mutex conflict among required dependencies)")
    return FeasibilityResult(
        Status.FEASIBLE, True,
        selected=_mask_to_projects(model, state[0]))


def optimize(projects, budget=None, required=(), mutex_groups=(),
             cycle_policy="group"):
    """Maximize total value with total cost <= budget.

    With a budget: OPTIMAL (the empty portfolio is itself feasible, so
    OPTIMAL with no projects means nothing fits), OVER_BUDGET when required
    projects are structurally possible but their minimum closure costs more
    than the budget, or INFEASIBLE when the required projects are mutually
    contradictory.
    Without a budget: UNDECIDED with the full cost/value Pareto frontier.
    """
    if budget is not None:
        budget = _num(budget)
        if budget < 0:
            raise ValueError("budget must be non-negative")
    model = compile_projects(projects, required, mutex_groups, cycle_policy)
    state = _initial_state(model)
    if state is None:
        return OptimizationResult(
            Status.INFEASIBLE, (), 0, 0,
            "required projects conflict through dependencies/mutex")

    if budget is None:
        return OptimizationResult(
            Status.UNDECIDED, (), 0, 0,
            "no budget supplied; inspect the Pareto frontier",
            frontier=_frontier(model, state))

    base_cost = _state_cost(model, state[0])
    if state[0]:
        if base_cost > budget:
            return OptimizationResult(
                Status.OVER_BUDGET, _mask_to_projects(model, state[0]),
                base_cost, _state_value(model, state[0]),
                "even the minimal required closure exceeds the budget")
        best = {"incl": state[0], "cost": base_cost,
                "value": _state_value(model, state[0])}
        required_present = True
    else:
        best = {"incl": 0, "cost": 0, "value": 0}
        required_present = False

    if not required_present:
        min_nonempty = _min_nonempty_cost(model)
        if min_nonempty is None or min_nonempty > budget:
            # no non-empty choice is available/affordable; empty choice stands
            reason = (
                "no non-empty feasible portfolio exists under the mutex rules"
                if min_nonempty is None
                else f"cheapest non-empty portfolio costs {min_nonempty} "
                     f"> budget {budget}; empty choice is optimal")
            return OptimizationResult(
                Status.OPTIMAL, (), 0, 0, reason)

    n = len(model.atoms)

    def dfs(incl, excl, cur_cost, cur_value):
        total = (1 << n) - 1
        undecided = ~(incl | excl) & total
        if cur_value > best["value"] or (
                cur_value == best["value"] and cur_cost < best["cost"]):
            best.update(incl=incl, cost=cur_cost, value=cur_value)
        if not undecided:
            return
        upper = cur_value + sum(
            model.value[i] for i in range(n)
            if undecided & (1 << i) and model.value[i] > 0)
        if upper < best["value"]:
            return
        if upper == best["value"] and cur_cost >= best["cost"]:
            return
        a = (undecided & -undecided).bit_length() - 1
        bit = 1 << a
        order = []
        if model.pair_mask[a] != -1:
            order.append(True)
        order.append(False)
        for take in order:
            if take:
                res = _propagate(model, incl, excl, {bit}, set())
            else:
                res = _propagate(model, incl, excl, set(), {bit})
            if res is None:
                continue
            ni, ne = res
            add_cost = sum((model.cost[i] for i in _added(incl, ni)), 0)
            if cur_cost + add_cost > budget:
                continue
            add_value = sum((model.value[i] for i in _added(incl, ni)), 0)
            dfs(ni, ne, cur_cost + add_cost, cur_value + add_value)

    dfs(state[0], state[1], base_cost, _state_value(model, state[0]))
    return OptimizationResult(
        Status.OPTIMAL, _mask_to_projects(model, best["incl"]),
        best["cost"], best["value"])


def _state_value(model, incl):
    return sum((model.value[i] for i in range(len(model.atoms))
                if incl & (1 << i)), 0)


def _frontier(model, state):
    points = []

    def visit(incl):
        cost = _state_cost(model, incl)
        value = _state_value(model, incl)
        points.append(FrontierPoint(cost, value,
                                    _mask_to_projects(model, incl)))
        return False

    def enumerate_from(incl, excl):
        total = (1 << len(model.atoms)) - 1
        undecided = ~(incl | excl) & total
        if not undecided:
            visit(incl)
            return
        low = undecided & -undecided
        a = low.bit_length() - 1
        bit = low
        if model.pair_mask[a] != -1:
            res = _propagate(model, incl, excl, {bit}, set())
            if res is not None:
                enumerate_from(*res)
        res = _propagate(model, incl, excl, set(), {bit})
        if res is not None:
            enumerate_from(*res)

    enumerate_from(state[0], state[1])

    nondominated = []
    for p in points:
        dominated = any(
            other.cost <= p.cost and other.value >= p.value
            and (other.cost < p.cost or other.value > p.value)
            for other in points)
        if not dominated:
            nondominated.append(p)
    nondominated.sort(key=lambda p: (p.cost, -p.value))
    return tuple(nondominated)


def undecide(projects, required=(), mutex_groups=(), cycle_policy="group"):
    """Return the Pareto frontier for the budget-undecided case."""
    model = compile_projects(projects, required, mutex_groups, cycle_policy)
    state = _initial_state(model)
    if state is None:
        return OptimizationResult(
            Status.INFEASIBLE, (), 0, 0,
            "required projects conflict through dependencies/mutex")
    return OptimizationResult(
        Status.UNDECIDED, (), 0, 0,
        "budget undecided; every frontier point is optimal for some budget",
        frontier=_frontier(model, state))


# --------------------------------------------------------------------------- #
# Runnable sample
# --------------------------------------------------------------------------- #

def _fmt(x):
    return str(x)


def main():
    print("=== Sample 1: shared dependency paid once ===")
    projects = {
        "platform": {"cost": 10, "value": -2, "deps": []},
        "app_a":    {"cost": 3,  "value": 8,  "deps": ["platform"]},
        "app_b":    {"cost": 4,  "value": 6,  "deps": ["platform"]},
        "standalone": {"cost": 5, "value": 4, "deps": []},
    }
    for budget in (9, 17, 100):
        r = optimize(projects, budget=budget)
        print(f"budget={budget:>3}: {r.status.value:<11} "
              f"projects={list(r.projects)} cost={_fmt(r.cost)} "
              f"value={_fmt(r.value)}")

    print()
    print("=== Sample 2: mutual exclusion ===")
    groups = [("app_a", "app_b")]
    r = optimize(projects, budget=100, mutex_groups=groups)
    print(f"mutex(app_a, app_b): {r.status.value} "
          f"projects={list(r.projects)} cost={_fmt(r.cost)} "
          f"value={_fmt(r.value)}")

    print()
    print("=== Sample 3: dependency cycle, group vs reject ===")
    cyclic = {
        "x": {"cost": 2, "value": 1, "deps": ["y"]},
        "y": {"cost": 3, "value": 7, "deps": ["x"]},
        "z": {"cost": 1, "value": 2, "deps": ["x"]},
    }
    r = optimize(cyclic, budget=100, cycle_policy="group")
    print(f"group : {r.status.value} projects={list(r.projects)} "
          f"cost={_fmt(r.cost)} value={_fmt(r.value)}")
    try:
        optimize(cyclic, budget=100, cycle_policy="reject")
    except CycleError as exc:
        print(f"reject: CycleError -> {exc}")

    print()
    print("=== Sample 4: rational numbers ===")
    rational = {
        "base": {"cost": "1/3", "value": 0},
        "feat": {"cost": "2/3", "value": "5/2", "deps": ["base"]},
    }
    r = optimize(rational, budget="1")
    print(f"budget=1: {r.status.value} projects={list(r.projects)} "
          f"cost={_fmt(r.cost)} value={_fmt(r.value)}")

    print()
    print("=== Sample 5: budget undecided -> Pareto frontier ===")
    r = undecide({"a": {"cost": 2, "value": 3},
                  "b": {"cost": 3, "value": 5},
                  "c": {"cost": 1, "value": 1}})
    print(r.status.value, "- frontier (cost, value, projects):")
    for p in r.frontier:
        print(f"  cost={_fmt(p.cost):<3} value={_fmt(p.value):<3} "
              f"projects={list(p.projects)}")

    print()
    print("=== Sample 6: feasibility ===")
    f = check_feasibility(projects, required=["app_a"])
    print(f"required=[app_a]: {f.status.value} closure={list(f.selected)}")
    f = check_feasibility(projects, required=["app_a", "app_b"],
                          mutex_groups=[("app_a", "app_b")])
    print(f"mutex(app_a,app_b), required both: {f.status.value} - {f.reason}")


if __name__ == "__main__":
    main()
