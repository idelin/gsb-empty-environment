"""Incremental AC-3 for binary CSPs with finite domains.

Features:
- Incremental arc consistency with a deduplicated propagation queue that
  never drops a required recheck.
- Every deleted value carries a Reason with evidence from the original
  constraint (neighbor variable and its domain at deletion time).
- Trail-based push/pop restores domains, deletion reasons, the propagation
  queue and the support cache.
- Constraints can be added and removed at any time. Removing a constraint
  triggers a restoration fixpoint: values that lost their justification are
  re-examined and restored when every remaining constraint supports them.
- Only the Python standard library is used.
"""

from collections import deque
from dataclasses import dataclass


class Conflict(Exception):
    """Raised immediately when a variable domain becomes empty."""


@dataclass(frozen=True)
class Reason:
    """Why a value is currently deleted.

    constraint_id: the constraint whose revision removed the value, or
        "<decision>" for branch decisions.
    neighbor: the other variable of that constraint.
    neighbor_domain: evidence, the neighbor's live domain at deletion time.
    kind: "arc" (restorable) or "decision" (only undone by pop).
    """

    constraint_id: str
    neighbor: str
    neighbor_domain: frozenset
    kind: str = "arc"

    def __str__(self):
        if self.kind == "decision":
            return "branch decision"
        return (
            f"no support in {self.neighbor} "
            f"(domain {sorted(self.neighbor_domain)}) "
            f"for constraint {self.constraint_id}"
        )


@dataclass
class Constraint:
    cid: str
    a: str
    b: str
    pred: object
    name: str = ""

    def other(self, var):
        return self.b if var == self.a else self.a

    def holds(self, var, value, other_value):
        if var == self.a:
            return self.pred(value, other_value)
        return self.pred(other_value, value)


class CSP:
    def __init__(self):
        self.universe = {}
        self.domains = {}
        self.deleted = {}
        self.constraints = {}
        self._by_var = {}
        self._supports = {}
        self._queue = deque()
        self._in_queue = set()
        self._trail = []
        self._checkpoints = []

    def add_variable(self, var, domain):
        if var in self.universe:
            raise ValueError(f"duplicate variable {var}")
        dom = frozenset(domain)
        if not dom:
            raise ValueError(f"empty initial domain for {var}")
        self.universe[var] = dom
        self.domains[var] = set(dom)
        self.deleted[var] = {}
        self._by_var[var] = []

    def add_constraint(self, cid, a, b, pred, name=None):
        if cid in self.constraints:
            raise ValueError(f"duplicate constraint id {cid}")
        for var in (a, b):
            if var not in self.universe:
                raise ValueError(f"unknown variable {var}")
        con = Constraint(cid, a, b, pred, name or cid)
        self._install(con)
        self._trail.append(("drop_constraint", cid))
        self._enqueue(a, cid)
        self._enqueue(b, cid)
        self._propagate()

    def remove_constraint(self, cid):
        con = self.constraints[cid]
        cache_entries = {
            key: sup for key, sup in self._supports.items() if key[0] == cid
        }
        self._uninstall(con)
        self._trail.append(("restore_constraint", con, cache_entries))
        self._restore_fixpoint()

    def delete_value(self, var, value):
        """Branch decision: remove a single value and propagate."""
        self._require_known(var, value)
        if value in self.deleted[var]:
            reason = self.deleted[var][value]
            if reason.kind != "decision":
                decision = Reason("<decision>", var, frozenset(), "decision")
                self._trail.append(("reason", var, value, reason))
                self.deleted[var][value] = decision
            return
        decision = Reason("<decision>", var, frozenset(), "decision")
        self._delete(var, value, decision)
        if not self.domains[var]:
            raise Conflict(f"domain of {var} emptied by decision")
        self._propagate()

    def assign(self, var, value):
        """Branch decision: keep only value in the domain of var."""
        self._require_known(var, value)
        if value not in self.domains[var]:
            raise Conflict(f"cannot assign {var}={value}: already deleted")
        for other_value in sorted(self.universe[var]):
            if other_value == value:
                continue
            decision = Reason("<decision>", var, frozenset(), "decision")
            if other_value in self.deleted[var]:
                reason = self.deleted[var][other_value]
                if reason.kind != "decision":
                    self._trail.append(("reason", var, other_value, reason))
                    self.deleted[var][other_value] = decision
            else:
                self._delete(var, other_value, decision)
        self._propagate()

    def push(self):
        self._checkpoints.append(
            (len(self._trail), deque(self._queue), set(self._in_queue))
        )

    def pop(self):
        trail_len, queue, in_queue = self._checkpoints.pop()
        while len(self._trail) > trail_len:
            record = self._trail.pop()
            kind = record[0]
            if kind == "undelete":
                _, var, value, _reason = record
                self.deleted[var].pop(value, None)
                self.domains[var].add(value)
            elif kind == "redelete":
                _, var, value, reason = record
                self.domains[var].discard(value)
                self.deleted[var][value] = reason
            elif kind == "reason":
                _, var, value, old_reason = record
                if value in self.deleted[var]:
                    self.deleted[var][value] = old_reason
            elif kind == "drop_constraint":
                _, cid = record
                self._uninstall(self.constraints[cid])
            elif kind == "restore_constraint":
                _, con, cache_entries = record
                self._install(con)
                self._supports.update(cache_entries)
            elif kind == "cache_forget":
                _, key = record
                self._supports.pop(key, None)
            else:
                raise AssertionError(f"unknown trail record {record}")
        self._queue = queue
        self._in_queue = in_queue

    def propagate(self):
        self._propagate()

    def current_domains(self):
        return {var: sorted(dom) for var, dom in self.domains.items()}

    def deleted_with_reasons(self):
        return {
            var: dict(entries) for var, entries in self.deleted.items() if entries
        }

    def _require_known(self, var, value):
        if var not in self.universe:
            raise ValueError(f"unknown variable {var}")
        if value not in self.universe[var]:
            raise ValueError(f"value {value} outside universe of {var}")

    def _install(self, con):
        self.constraints[con.cid] = con
        self._by_var[con.a].append(con.cid)
        self._by_var[con.b].append(con.cid)

    def _uninstall(self, con):
        del self.constraints[con.cid]
        self._by_var[con.a].remove(con.cid)
        self._by_var[con.b].remove(con.cid)
        for key in [key for key in self._supports if key[0] == con.cid]:
            del self._supports[key]

    def _enqueue(self, var, cid):
        key = (var, cid)
        if key not in self._in_queue:
            self._in_queue.add(key)
            self._queue.append(key)

    def _support_set(self, con, var, value):
        """Supporting values in the neighbor's universe (cached, static)."""
        key = (con.cid, var, value)
        sup = self._supports.get(key)
        if sup is None:
            other = con.other(var)
            sup = frozenset(
                w for w in self.universe[other] if con.holds(var, value, w)
            )
            self._supports[key] = sup
            self._trail.append(("cache_forget", key))
        return sup

    def _has_support(self, con, var, value):
        other = con.other(var)
        return not self._support_set(con, var, value).isdisjoint(self.domains[other])

    def _delete(self, var, value, reason):
        self.domains[var].discard(value)
        self.deleted[var][value] = reason
        self._trail.append(("undelete", var, value, reason))
        for cid in self._by_var[var]:
            con = self.constraints[cid]
            self._enqueue(con.other(var), cid)

    def _propagate(self):
        while self._queue:
            var, cid = self._queue.popleft()
            self._in_queue.discard((var, cid))
            con = self.constraints.get(cid)
            if con is None or var not in (con.a, con.b):
                continue
            other = con.other(var)
            for value in sorted(self.domains[var]):
                if not self._has_support(con, var, value):
                    reason = Reason(cid, other, frozenset(self.domains[other]))
                    self._delete(var, value, reason)
                    if not self.domains[var]:
                        raise Conflict(
                            f"domain of {var} emptied while enforcing {cid}"
                        )

    def _restore_fixpoint(self):
        """Restore deleted values that lost their justification.

        Computes the greatest fixpoint: all arc-deleted values start as
        candidates; a candidate is pruned when some remaining constraint
        has no support for it among live values plus surviving candidates
        (so mutually supporting deleted values are restored together).
        Decision deletions are never restored here; only pop undoes them.
        Values that stay deleted but were justified by a removed
        constraint get a fresh reason from a live constraint.
        """
        candidates = {}
        for var in self.deleted:
            values = {
                value
                for value, reason in self.deleted[var].items()
                if reason.kind != "decision"
            }
            if values:
                candidates[var] = values
        changed = True
        while changed:
            changed = False
            for var in sorted(candidates):
                for value in sorted(candidates[var]):
                    for cid in self._by_var[var]:
                        con = self.constraints[cid]
                        other = con.other(var)
                        pool = self.domains[other] | candidates.get(other, set())
                        if self._support_set(con, var, value).isdisjoint(pool):
                            candidates[var].discard(value)
                            changed = True
                            break
        for var in sorted(candidates):
            for value in sorted(candidates[var]):
                reason = self.deleted[var].pop(value)
                self.domains[var].add(value)
                self._trail.append(("redelete", var, value, reason))
        for var in sorted(self.deleted):
            for value, reason in list(self.deleted[var].items()):
                if reason.kind == "decision":
                    continue
                if reason.constraint_id in self.constraints:
                    continue
                for cid in self._by_var[var]:
                    con = self.constraints[cid]
                    if not self._has_support(con, var, value):
                        new_reason = Reason(
                            cid,
                            con.other(var),
                            frozenset(self.domains[con.other(var)]),
                        )
                        self._trail.append(("reason", var, value, reason))
                        self.deleted[var][value] = new_reason
                        break
                else:
                    raise AssertionError(
                        f"deleted value {var}={value} has no live justification"
                    )


def solve_one(csp):
    """Depth-first search on top of push/pop + propagation."""
    if all(len(dom) == 1 for dom in csp.domains.values()):
        return {var: next(iter(dom)) for var, dom in csp.domains.items()}
    var = min(
        (v for v in csp.domains if len(csp.domains[v]) > 1),
        key=lambda v: len(csp.domains[v]),
    )
    for value in sorted(csp.domains[var]):
        csp.push()
        try:
            csp.assign(var, value)
            solution = solve_one(csp)
        except Conflict:
            solution = None
        csp.pop()
        if solution is not None:
            return solution
    return None


def solve_all(csp, limit=None):
    solutions = []

    def search():
        if limit is not None and len(solutions) >= limit:
            return
        if all(len(dom) == 1 for dom in csp.domains.values()):
            solutions.append(
                {var: next(iter(dom)) for var, dom in csp.domains.items()}
            )
            return
        var = min(
            (v for v in csp.domains if len(csp.domains[v]) > 1),
            key=lambda v: len(csp.domains[v]),
        )
        for value in sorted(csp.domains[var]):
            csp.push()
            try:
                csp.assign(var, value)
                search()
            except Conflict:
                pass
            csp.pop()

    search()
    return solutions


def _demo():
    from reference import check_assignment, enumerate_solutions

    print("=== 样例 1: x < y, 域 {1,2} ===")
    csp = CSP()
    csp.add_variable("x", {1, 2})
    csp.add_variable("y", {1, 2})
    csp.add_constraint("c_xy", "x", "y", lambda a, b: a < b, name="x<y")
    print("AC-3 后域:", csp.current_domains())
    for var, entries in csp.deleted_with_reasons().items():
        for value, reason in sorted(entries.items()):
            print(f"  删除 {var}={value}: {reason}")

    print("\n=== 样例 2: 空域立即冲突 (x<y, x∈{1,2}, y∈{1}) ===")
    csp2 = CSP()
    csp2.add_variable("x", {1, 2})
    csp2.add_variable("y", {1})
    try:
        csp2.add_constraint("c_xy", "x", "y", lambda a, b: a < b, name="x<y")
        print("未冲突(不应发生)")
    except Conflict as exc:
        print("立即冲突:", exc)

    print("\n=== 样例 3: push/pop 恢复分支 ===")
    csp3 = CSP()
    csp3.add_variable("x", {1, 2, 3})
    csp3.add_variable("y", {1, 2, 3})
    csp3.add_constraint("c_xy", "x", "y", lambda a, b: a < b, name="x<y")
    print("传播后:", csp3.current_domains())
    csp3.push()
    csp3.assign("x", 2)
    print("分支 x=2 后:", csp3.current_domains())
    csp3.pop()
    print("pop 恢复后:", csp3.current_domains())

    print("\n=== 样例 4: 撤回约束后重新判断值可恢复 ===")
    csp4 = CSP()
    csp4.add_variable("x", {1, 2})
    csp4.add_variable("y", {1, 2})
    csp4.add_variable("z", {2})
    csp4.add_constraint("c_xy", "x", "y", lambda a, b: a < b, name="x<y")
    csp4.add_constraint("c_xz", "x", "z", lambda a, b: a < b, name="x<z")
    print("两条约束传播后:", csp4.current_domains())
    csp4.remove_constraint("c_xy")
    print("撤回 x<y 后:", csp4.current_domains())
    for var, entries in csp4.deleted_with_reasons().items():
        for value, reason in sorted(entries.items()):
            print(f"  仍删除 {var}={value}: {reason}")
    csp4.remove_constraint("c_xz")
    print("再撤回 x<z 后:", csp4.current_domains())

    print("\n=== 样例 5: 4 皇后求解 + 独立检查器验证 ===")
    n = 4
    csp5 = CSP()
    for i in range(n):
        csp5.add_variable(f"r{i}", set(range(n)))
    for i in range(n):
        for j in range(i + 1, n):
            gap = j - i
            csp5.add_constraint(
                f"q{i}_{j}",
                f"r{i}",
                f"r{j}",
                lambda a, b, gap=gap: a != b and abs(a - b) != gap,
                name=f"row{i}/row{j} 不互攻",
            )
    solution = solve_one(csp5)
    print("求得一解:", solution)
    cons = [(con.a, con.b, con.pred) for con in csp5.constraints.values()]
    ok = check_assignment(csp5.universe, cons, solution)
    print("独立检查器验证原约束:", "OK" if ok else "FAIL")
    all_solutions = solve_all(csp5)
    ref_solutions = enumerate_solutions(csp5.universe, cons)
    print(f"全部解个数: CSP={len(all_solutions)}, 枚举={len(ref_solutions)}")


if __name__ == "__main__":
    _demo()
