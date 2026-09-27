"""GAC propagator for the element constraint: value = array[index].

Features
--------
- Array entries may be constants or Variables.
- index / value / array entries may ALIAS the same Variable object; the
  propagator enumerates over the *unique* variable set so aliasing
  correlations are preserved exactly.
- Out-of-bounds index values are pruned with an explicit reason.
- Trail-based undo (save/restore) for backtracking search.
- Every removed value gets an Explanation recording the no-support evidence
  (a snapshot of the other variables' domains at removal time).
- Generalized arc consistency on the declared constraint subset, computed by
  enumerating all tuples of the (small) finite domains.
- An independent checker verifies any claimed solution against the original
  constraint.  No SAT/SMT library is used; pure standard library.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from itertools import product


class Variable:
    """A finite-domain variable identified by object identity (aliasing-safe)."""

    def __init__(self, name: str, domain):
        self.name = name
        self.initial = frozenset(domain)

    def __repr__(self):  # identity-based hash/eq from object
        return f"Var({self.name})"


@dataclass
class Explanation:
    """Why (variable /= value): the no-support evidence at removal time."""

    constraint: str
    variable: str
    value: object
    reason: str
    others: dict = field(default_factory=dict)  # var name -> sorted domain snapshot

    def __str__(self):
        others = ", ".join(f"{k}={v}" for k, v in sorted(self.others.items()))
        return (f"[{self.constraint}] {self.variable} /= {self.value!r}: "
                f"{self.reason} | other domains: {{{others}}}")


class Store:
    """Mutable domains with a trail for undo and an explanation table."""

    def __init__(self, variables):
        self.domains = {v: set(v.initial) for v in variables}
        self.trail = []                 # list of (var, value) removals
        self.explanations = {}          # (var, value) -> Explanation

    def domain(self, var):
        return self.domains[var]

    def remove(self, var, value, explanation: Explanation) -> bool:
        dom = self.domains[var]
        if value not in dom:
            return False
        dom.remove(value)
        self.trail.append((var, value))
        self.explanations[(var, value)] = explanation
        return True

    def save(self) -> int:
        return len(self.trail)

    def restore(self, mark: int):
        """Undo every removal past `mark`, dropping their explanations."""
        while len(self.trail) > mark:
            var, value = self.trail.pop()
            self.domains[var].add(value)
            del self.explanations[(var, value)]

    def wiped_out(self) -> bool:
        return any(not d for d in self.domains.values())


class ElementConstraint:
    """value = array[index]; array items are Variables or constants."""

    def __init__(self, value, array, index, name="element"):
        self.value = value
        self.array = list(array)
        self.index = index
        self.name = name
        # Unique variables in canonical order: index, value, then array vars.
        self.vars = []
        for item in [index, value, *self.array]:
            if isinstance(item, Variable) and all(item is not u for u in self.vars):
                self.vars.append(item)

    def _entry_value(self, i, env):
        item = self.array[i]
        return env[id(item)] if isinstance(item, Variable) else item

    def supported_values(self, store: Store):
        """GAC support sets: var -> set of values appearing in some valid tuple."""
        supported = {v: set() for v in self.vars}
        n = len(self.array)
        doms = [sorted(store.domain(v)) for v in self.vars]
        for combo in product(*doms):
            env = {id(v): val for v, val in zip(self.vars, combo)}
            idx = env[id(self.index)]
            if not (0 <= idx < n):
                continue
            if env[id(self.value)] != self._entry_value(idx, env):
                continue
            for v, val in zip(self.vars, combo):
                supported[v].add(val)
        return supported

    def propagate(self, store: Store) -> bool:
        """Prune unsupported values (GAC).  Returns False on domain wipeout."""
        supported = self.supported_values(store)
        n = len(self.array)
        for v in self.vars:
            for val in sorted(store.domain(v)):
                if val in supported[v]:
                    continue
                if v is self.index and not (0 <= val < n):
                    reason = f"index {val!r} out of bounds for array of size {n}"
                else:
                    reason = "no supporting tuple under current domains (GAC)"
                others = {u.name: sorted(store.domain(u))
                          for u in self.vars if u is not v}
                store.remove(v, val, Explanation(self.name, v.name, val, reason, others))
            if not store.domain(v):
                return False
        return True

    def describe(self) -> str:
        def show(item):
            return item.name if isinstance(item, Variable) else repr(item)
        arr = "[" + ", ".join(show(a) for a in self.array) + "]"
        return f"{self.value.name} = {arr}[{self.index.name}]"


class Solver:
    """DFS search with GAC fixpoint propagation and trail-based undo."""

    def __init__(self, constraints):
        self.constraints = list(constraints)
        self.variables = []
        for c in self.constraints:
            for v in c.vars:
                if all(v is not u for u in self.variables):
                    self.variables.append(v)
        self.store = Store(self.variables)

    def propagate(self) -> bool:
        changed = True
        while changed:
            changed = False
            for c in self.constraints:
                before = self.store.save()
                if not c.propagate(self.store):
                    return False
                if self.store.save() != before:
                    changed = True
        return not self.store.wiped_out()

    def solve(self):
        """Return an assignment dict {Variable: value} or None."""
        if not self.propagate():
            return None
        return self._search()

    def _search(self):
        var = next((v for v in self.variables
                    if len(self.store.domain(v)) > 1), None)
        if var is None:
            return {v: next(iter(self.store.domain(v))) for v in self.variables}
        for val in sorted(self.store.domain(var)):
            mark = self.store.save()
            for other in sorted(self.store.domain(var) - {val}):
                self.store.remove(var, other, Explanation(
                    "branch", var.name, other, f"trying branch {var.name}={val!r}", {}))
            if self.propagate():
                result = self._search()
                if result is not None:
                    return result
            self.store.restore(mark)  # undo branch + its propagation
        return None


def check_element(value, array, index, assignment, variables=None) -> bool:
    """Independent checker: verify `value = array[index]` on a full assignment.

    Also (optionally) checks every assigned value lies in its original domain.
    Does not share any propagation code with the solver.
    """
    if index not in assignment or value not in assignment:
        return False
    idx = assignment[index]
    if not isinstance(idx, int) or isinstance(idx, bool) or not (0 <= idx < len(array)):
        return False
    target = array[idx]
    target_val = assignment[target] if isinstance(target, Variable) else target
    if assignment[value] != target_val:
        return False
    if variables is not None:
        for var, val in assignment.items():
            if val not in var.initial:
                return False
    return True


def enumerate_solutions(value, array, index):
    """Brute-force enumeration of ALL satisfying assignments over the initial
    domains.  Ground truth for tests: support sets and solution checking."""
    variables = []
    for item in [index, value, *array]:
        if isinstance(item, Variable) and all(item is not u for u in variables):
            variables.append(item)
    doms = [sorted(v.initial) for v in variables]
    sols = []
    for combo in product(*doms):
        assignment = dict(zip(variables, combo))
        if check_element(value, array, index, assignment):
            sols.append(assignment)
    return variables, sols


def _demo():
    print("=== Demo: value = array[index] with aliasing, undo, explanations ===\n")

    # Problem: idx = [x, 4, y][idx], val aliases nothing here; x,y are vars.
    idx = Variable("idx", {0, 1, 2, 3, 7})      # 3 and 7 are out of bounds
    val = Variable("val", {1, 2, 3, 4, 5})
    x = Variable("x", {1, 2})
    y = Variable("y", {2, 5})
    con = ElementConstraint(val, [x, 4, y], idx)
    solver = Solver([con])
    print("constraint:", con.describe())
    print("initial domains:",
          {v.name: sorted(v.initial) for v in solver.variables})

    ok = solver.propagate()
    print("\nafter GAC propagation (ok =", ok, "):")
    for v in solver.variables:
        print(f"  {v.name} = {sorted(solver.store.domain(v))}")
    print("\nexplanations for removed values:")
    for (var, value), expl in sorted(solver.store.explanations.items(),
                                     key=lambda kv: (kv[0][0].name, kv[0][1])):
        print(" ", expl)

    # Undo demo (before solving): branch idx=0, then retract it.
    mark = solver.store.save()
    for other in sorted(solver.store.domain(idx) - {0}):
        solver.store.remove(idx, other, Explanation("branch", "idx", other,
                                                    "trying branch idx=0", {}))
    solver.propagate()
    print("\nunder branch idx=0: val =", sorted(solver.store.domain(val)),
          " x =", sorted(solver.store.domain(x)))
    solver.store.restore(mark)
    print("after undo: idx =", sorted(solver.store.domain(idx)),
          " val =", sorted(solver.store.domain(val)),
          " x =", sorted(solver.store.domain(x)))

    sol = solver.solve()
    print("\nsolution found:", {v.name: s for v, s in sol.items()})
    verified = check_element(val, [x, 4, y], idx, sol, variables=solver.variables)
    print("independent checker verifies solution:", verified)

    # Aliasing demo: the index IS the first array element.
    print("\n=== Aliasing demo: i = [i, 10][i] ===")
    i = Variable("i", {0, 1})
    v2 = Variable("v", {0, 1, 10})
    con2 = ElementConstraint(v2, [i, 10], i)
    s2 = Solver([con2])
    s2.propagate()
    print("after propagation: i =", sorted(s2.store.domain(i)),
          " v =", sorted(s2.store.domain(v2)))
    for (var, value), expl in s2.store.explanations.items():
        print(" ", expl)
    sol2 = s2.solve()
    print("solutions check:",
          all(check_element(v2, [i, 10], i, {i: a[i], v2: a[v2]})
              for a in [sol2]) if sol2 else None,
          "| one solution:", {v.name: s for v, s in sol2.items()})


if __name__ == "__main__":
    _demo()
