"""Incremental AC-3 for binary CSPs with dynamic constraints and backtracking.

Pure Python 3.11, standard library only.

Features:
- Incremental arc-consistency (AC-3) propagation over a deduplicated arc queue.
- Every deleted value carries a Reason with the original constraint evidence
  (the neighbor's domain snapshot at deletion time).
- A support cache memoizes the witness value that supports each
  (constraint, variable, value) triple.
- push()/pop() trail restores domains, the queue, the support cache, the
  deletion log and the constraint set.
- Constraints can be added and removed at any time.  Removing a constraint
  does not keep blindly shrinking domains: values whose deletion transitively
  depended on the removed constraint are restored and re-checked, so the
  result is exactly the arc-consistent fixpoint of the remaining constraints.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from typing import Any, Callable, Hashable, Iterable

Predicate = Callable[[Any, Any], bool]

_MISSING = object()


@dataclass(frozen=True)
class Reason:
    """Evidence explaining why a value was removed from a domain.

    For constraint-driven deletions, ``neighbor_domain_snapshot`` is the
    neighbor's domain at deletion time: the deleted value had no support in
    it under the referenced constraint.  ``constraint_id`` is ``None`` for
    branch (search-decision) deletions.
    """

    constraint_id: Hashable | None
    description: str
    neighbor: Hashable | None
    neighbor_domain_snapshot: frozenset

    def __str__(self) -> str:
        if self.constraint_id is None:
            return self.description
        snap = sorted(self.neighbor_domain_snapshot, key=repr)
        return (
            f"no support in {self.neighbor} (domain {snap} at deletion) "
            f"via constraint {self.constraint_id!r} ({self.description})"
        )


@dataclass(frozen=True)
class Deletion:
    var: Hashable
    value: Any
    reason: Reason


@dataclass(frozen=True)
class Constraint:
    cid: Hashable
    a: Hashable
    b: Hashable
    pred: Predicate
    description: str = ""

    def holds(self, var: Hashable, value: Any, other: Any) -> bool:
        """Check the constraint for ``var=value`` against ``other``'s value."""
        if var == self.a:
            return bool(self.pred(value, other))
        return bool(self.pred(other, value))


class CSP:
    """Binary CSP with incremental AC-3, dynamic constraints and a trail."""

    def __init__(self) -> None:
        self._original: dict[Hashable, frozenset] = {}
        self._domains: dict[Hashable, set] = {}
        self._deleted: dict[Hashable, set] = {}
        self._constraints: dict[Hashable, Constraint] = {}
        self._on: dict[Hashable, list[Hashable]] = {}
        self._queue: deque[tuple[Hashable, Hashable, Hashable]] = deque()
        self._in_queue: set[tuple[Hashable, Hashable, Hashable]] = set()
        self._support_cache: dict[tuple[Hashable, Hashable, Any], Any] = {}
        self.deletions: list[Deletion] = []
        self.conflict = False
        self._assignments: dict[Hashable, Any] = {}
        self._trail: list[tuple] = []
        self._cid_counter = 0

    # ------------------------------------------------------------------ API
    def add_variable(self, name: Hashable, domain: Iterable) -> None:
        if name in self._domains:
            raise ValueError(f"duplicate variable {name!r}")
        dom = set(domain)
        if not dom:
            raise ValueError("domain must be non-empty")
        self._original[name] = frozenset(dom)
        self._domains[name] = dom
        self._deleted[name] = set()
        self._on[name] = []

    def add_constraint(
        self,
        a: Hashable,
        b: Hashable,
        pred: Predicate,
        *,
        cid: Hashable | None = None,
        description: str = "",
    ) -> Hashable:
        """Add a binary constraint and incrementally propagate (AC-3)."""
        self._check_var(a)
        self._check_var(b)
        if a == b:
            raise ValueError("constraint variables must differ")
        if cid is None:
            self._cid_counter += 1
            cid = f"c{self._cid_counter}"
        if cid in self._constraints:
            raise ValueError(f"duplicate constraint id {cid!r}")
        con = Constraint(cid, a, b, pred, description or f"{a}~{b}")
        self._constraints[cid] = con
        self._on[a].append(cid)
        self._on[b].append(cid)
        self._enqueue(a, b, cid)
        self._enqueue(b, a, cid)
        self.propagate()
        return cid

    def remove_constraint(self, cid: Hashable) -> bool:
        """Remove a constraint, restore values that lost their reason, and
        re-propagate so domains are exactly arc-consistent again.

        Restoration is transitive: a deleted value is suspect if its reason
        references ``cid`` or if a deleted support of its reason is suspect.
        Suspects are restored and then re-checked by AC-3, so values that
        still lack support are deleted again with fresh reasons.
        """
        con = self._constraints.pop(cid, None)
        if con is None:
            raise ValueError(f"unknown constraint id {cid!r}")
        self._on[con.a].remove(cid)
        self._on[con.b].remove(cid)
        if any(arc[2] == cid for arc in self._queue):
            self._queue = deque(arc for arc in self._queue if arc[2] != cid)
            self._in_queue = {arc for arc in self._in_queue if arc[2] != cid}
        self._support_cache = {
            key: val for key, val in self._support_cache.items() if key[0] != cid
        }
        was_conflict = self.conflict
        suspects = self._restoration_suspects(cid)
        restored_vars: set = set()
        if suspects:
            remaining = []
            for d in self.deletions:
                if (d.var, d.value) in suspects and self._assignments.get(
                    d.var, d.value
                ) == d.value:
                    self._domains[d.var].add(d.value)
                    self._deleted[d.var].discard(d.value)
                    restored_vars.add(d.var)
                else:
                    remaining.append(d)
            self.deletions = remaining
        self.conflict = False
        if was_conflict:
            for c in self._constraints.values():
                self._enqueue(c.a, c.b, c.cid)
                self._enqueue(c.b, c.a, c.cid)
        else:
            for var in restored_vars:
                for cid2 in self._on[var]:
                    c2 = self._constraints[cid2]
                    nbr = c2.b if c2.a == var else c2.a
                    self._enqueue(var, nbr, cid2)
        if any(not d for d in self._domains.values()):
            self.conflict = True
            self._queue.clear()
            self._in_queue.clear()
            return False
        return self.propagate()

    def assign_value(self, var: Hashable, value: Any) -> bool:
        """Restrict ``var`` to ``value`` (a branch decision) and propagate."""
        self._check_var(var)
        if self.conflict:
            return False
        if value not in self._domains[var]:
            raise ValueError(f"{value!r} not in current domain of {var!r}")
        self._assignments[var] = value
        reason = Reason(None, f"branch assignment {var}={value}", None, frozenset())
        for v in list(self._domains[var]):
            if v != value:
                self._delete(var, v, reason)
        return self.propagate()

    def propagate(self) -> bool:
        """Drain the arc queue.  Returns False on an empty domain (conflict)."""
        if self.conflict:
            return False
        while self._queue:
            var, nbr, cid = self._queue.popleft()
            self._in_queue.discard((var, nbr, cid))
            con = self._constraints.get(cid)
            if con is None or var not in (con.a, con.b):
                continue
            if self._revise(var, nbr, con) and not self._domains[var]:
                self.conflict = True
                self._queue.clear()
                self._in_queue.clear()
                return False
        return True

    def push(self) -> None:
        """Snapshot domains, queue, support cache, reasons and constraints."""
        self._trail.append(
            (
                {v: set(d) for v, d in self._domains.items()},
                {v: set(s) for v, s in self._deleted.items()},
                deque(self._queue),
                set(self._in_queue),
                dict(self._support_cache),
                list(self.deletions),
                dict(self._constraints),
                {v: list(l) for v, l in self._on.items()},
                self.conflict,
                self._cid_counter,
                dict(self._assignments),
            )
        )

    def pop(self) -> None:
        """Restore the state saved by the matching push()."""
        if not self._trail:
            raise IndexError("pop without push")
        (
            self._domains,
            self._deleted,
            self._queue,
            self._in_queue,
            self._support_cache,
            self.deletions,
            self._constraints,
            self._on,
            self.conflict,
            self._cid_counter,
            self._assignments,
        ) = self._trail.pop()

    def solve(self) -> dict | None:
        """Backtracking search on top of push/pop + propagate.

        On success the CSP is left in the solved branch state and the full
        assignment is returned; on failure None is returned and the state is
        restored to what it was before the call.
        """
        if not self.propagate():
            return None
        var = next((v for v in self._domains if len(self._domains[v]) > 1), None)
        if var is None:
            return {v: next(iter(d)) for v, d in self._domains.items()}
        for value in sorted(self._domains[var], key=repr):
            self.push()
            self.assign_value(var, value)
            if not self.conflict:
                result = self.solve()
                if result is not None:
                    return result
            self.pop()
        return None

    # ------------------------------------------------------------ inspection
    @property
    def domains(self) -> dict:
        return {v: set(d) for v, d in self._domains.items()}

    @property
    def constraints(self) -> dict:
        return dict(self._constraints)

    @property
    def pending_arcs(self) -> list:
        return list(self._queue)

    @property
    def support_cache(self) -> dict:
        return dict(self._support_cache)

    @property
    def trail_depth(self) -> int:
        return len(self._trail)

    # ------------------------------------------------------------- internals
    def _check_var(self, var: Hashable) -> None:
        if var not in self._domains:
            raise ValueError(f"unknown variable {var!r}")

    def _enqueue(self, var: Hashable, nbr: Hashable, cid: Hashable) -> None:
        arc = (var, nbr, cid)
        if arc not in self._in_queue:
            self._in_queue.add(arc)
            self._queue.append(arc)

    def _delete(self, var: Hashable, value: Any, reason: Reason) -> None:
        self._domains[var].discard(value)
        self._deleted[var].add(value)
        self.deletions.append(Deletion(var, value, reason))
        for cid2 in self._on[var]:
            c2 = self._constraints[cid2]
            nbr2 = c2.b if c2.a == var else c2.a
            self._enqueue(nbr2, var, cid2)

    def _revise(self, var: Hashable, nbr: Hashable, con: Constraint) -> bool:
        revised = False
        dom = self._domains[var]
        ndom = self._domains[nbr]
        for value in list(dom):
            key = (con.cid, var, value)
            cached = self._support_cache.get(key, _MISSING)
            if cached is not _MISSING and cached in ndom:
                continue
            support = _MISSING
            for w in ndom:
                if con.holds(var, value, w):
                    support = w
                    break
            if support is not _MISSING:
                self._support_cache[key] = support
            else:
                reason = Reason(con.cid, con.description, nbr, frozenset(ndom))
                self._delete(var, value, reason)
                revised = True
        return revised

    def _restoration_suspects(self, removed_cid: Hashable) -> set:
        """Deleted (var, value) pairs whose deletion transitively depended on
        the removed constraint and therefore must be re-judged."""
        reasons = {(d.var, d.value): d.reason for d in self.deletions}
        suspects: set = set()
        changed = True
        while changed:
            changed = False
            for (var, value), reason in reasons.items():
                if (var, value) in suspects:
                    continue
                if reason.constraint_id == removed_cid:
                    suspects.add((var, value))
                    changed = True
                    continue
                con = self._constraints.get(reason.constraint_id)
                if con is None:
                    continue
                nbr = reason.neighbor
                for w in self._original[nbr]:
                    if w in self._domains[nbr] or (nbr, w) not in suspects:
                        continue
                    if con.holds(var, value, w):
                        suspects.add((var, value))
                        changed = True
                        break
        return suspects
