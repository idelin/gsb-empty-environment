"""Unit tests for the incremental AC-3 solver (ac3.CSP).

Covers: domain shrinking, empty-domain conflict, push/pop restoration,
constraint removal with value restoration (including transitive/circular
cases), deletion-reason evidence, support-cache and queue restoration,
queue dedup without lost rechecks, and randomized session cross-validation
against an independent full-AC reference plus full assignment enumeration.
"""

import random
import unittest

from ac3 import CSP
from reference import check_solution, enumerate_solutions, reference_ac

PREDICATES = [
    ("<", lambda a, b: a < b),
    ("<=", lambda a, b: a <= b),
    ("==", lambda a, b: a == b),
    ("!=", lambda a, b: a != b),
    ("a+b<=4", lambda a, b: a + b <= 4),
    ("|a-b|>=2", lambda a, b: abs(a - b) >= 2),
]


class TestBasicPropagation(unittest.TestCase):
    def test_less_than_shrinks_domains(self):
        csp = CSP()
        csp.add_variable("x", {1, 2})
        csp.add_variable("y", {1, 2})
        csp.add_constraint("x", "y", lambda a, b: a < b, cid="lt", description="x<y")
        self.assertFalse(csp.conflict)
        self.assertEqual(csp.domains, {"x": {1}, "y": {2}})

    def test_empty_domain_is_immediate_conflict(self):
        csp = CSP()
        csp.add_variable("x", {2})
        csp.add_variable("y", {1})
        csp.add_constraint("x", "y", lambda a, b: a < b, cid="lt")
        self.assertTrue(csp.conflict)
        self.assertEqual(csp.domains["x"], set())
        self.assertFalse(csp.propagate())

    def test_chain_rechecks_are_not_lost(self):
        csp = CSP()
        for v in "abc":
            csp.add_variable(v, {1, 2, 3})
        csp.add_constraint("a", "b", lambda x, y: x < y, cid="ab")
        csp.add_constraint("b", "c", lambda x, y: x < y, cid="bc")
        self.assertTrue(csp.assign_value("c", 3))
        self.assertEqual(csp.domains, {"a": {1}, "b": {2}, "c": {3}})

    def test_queue_has_no_duplicate_arcs(self):
        csp = CSP()
        for v in "abcd":
            csp.add_variable(v, {1, 2, 3, 4})
        csp.add_constraint("a", "b", lambda x, y: x < y)
        csp.add_constraint("b", "c", lambda x, y: x < y)
        csp.add_constraint("c", "d", lambda x, y: x < y)
        csp.assign_value("d", 4)
        pending = csp.pending_arcs
        self.assertEqual(len(pending), len(set(pending)))
        csp.propagate()
        self.assertEqual(csp.domains, {"a": {1}, "b": {2}, "c": {3}, "d": {4}})

    def test_deletion_reasons_carry_constraint_evidence(self):
        csp = CSP()
        csp.add_variable("x", {1, 2})
        csp.add_variable("y", {1, 2})
        csp.add_constraint("x", "y", lambda a, b: a < b, cid="lt", description="x<y")
        con = csp.constraints["lt"]
        deleted = {(d.var, d.value) for d in csp.deletions}
        self.assertEqual(deleted, {("x", 2), ("y", 1)})
        for d in csp.deletions:
            r = d.reason
            self.assertEqual(r.constraint_id, "lt")
            self.assertEqual(r.neighbor, "y" if d.var == "x" else "x")
            self.assertIsInstance(r.neighbor_domain_snapshot, frozenset)
            for w in r.neighbor_domain_snapshot:
                self.assertFalse(con.holds(d.var, d.value, w))


class TestPushPop(unittest.TestCase):
    def test_pop_restores_domains_queue_cache_and_reasons(self):
        csp = CSP()
        for v in "xyz":
            csp.add_variable(v, {1, 2, 3})
        csp.add_constraint("x", "y", lambda a, b: a < b, cid="c1")
        snap_domains = csp.domains
        snap_cache = csp.support_cache
        snap_queue = csp.pending_arcs
        snap_deletions = list(csp.deletions)
        csp.push()
        csp.add_constraint("y", "z", lambda a, b: a < b, cid="c2")
        csp.assign_value("z", 3)
        self.assertEqual(csp.domains, {"x": {1}, "y": {2}, "z": {3}})
        self.assertNotEqual(csp.support_cache, snap_cache)
        csp.pop()
        self.assertEqual(csp.domains, snap_domains)
        self.assertEqual(csp.support_cache, snap_cache)
        self.assertEqual(csp.pending_arcs, snap_queue)
        self.assertEqual(csp.deletions, snap_deletions)
        self.assertNotIn("c2", csp.constraints)
        self.assertFalse(csp.conflict)

    def test_pop_restores_after_conflict(self):
        csp = CSP()
        csp.add_variable("x", {1, 2})
        csp.add_variable("y", {1, 2})
        csp.push()
        csp.add_constraint("x", "y", lambda a, b: a < b)
        csp.add_constraint("x", "y", lambda a, b: a > b)
        self.assertTrue(csp.conflict)
        csp.pop()
        self.assertFalse(csp.conflict)
        self.assertEqual(csp.domains, {"x": {1, 2}, "y": {1, 2}})


class TestConstraintRemoval(unittest.TestCase):
    def test_removal_restores_values(self):
        csp = CSP()
        csp.add_variable("x", {1, 2})
        csp.add_variable("y", {1, 2})
        csp.add_constraint("x", "y", lambda a, b: a < b, cid="lt")
        self.assertEqual(csp.domains, {"x": {1}, "y": {2}})
        csp.remove_constraint("lt")
        self.assertEqual(csp.domains, {"x": {1, 2}, "y": {1, 2}})
        self.assertEqual(csp.deletions, [])

    def test_removal_keeps_values_still_unsupported_elsewhere(self):
        csp = CSP()
        for v in "abc":
            csp.add_variable(v, {1, 2, 3})
        csp.add_constraint("a", "b", lambda x, y: x < y, cid="ab")
        csp.add_constraint("b", "c", lambda x, y: x < y, cid="bc")
        self.assertEqual(csp.domains, {"a": {1}, "b": {2}, "c": {3}})
        csp.remove_constraint("ab")
        expected = reference_ac(
            {v: {1, 2, 3} for v in "abc"},
            [("b", "c", lambda x, y: x < y)],
        )
        self.assertEqual(csp.domains, expected)
        self.assertEqual(csp.domains, {"a": {1, 2, 3}, "b": {1, 2}, "c": {2, 3}})

    def test_removal_restores_circular_support(self):
        csp = CSP()
        csp.add_variable("x", {1, 2})
        csp.add_variable("y", {1, 2})
        csp.add_constraint("x", "y", lambda a, b: a == b, cid="eq")
        csp.add_constraint("x", "y", lambda a, b: a + b <= 2, cid="sum")
        self.assertEqual(csp.domains, {"x": {1}, "y": {1}})
        csp.remove_constraint("sum")
        self.assertEqual(csp.domains, {"x": {1, 2}, "y": {1, 2}})

    def test_removal_redeletes_values_still_unsupported(self):
        csp = CSP()
        csp.add_variable("x", {1, 2, 3})
        csp.add_variable("y", {1, 2, 3})
        csp.add_variable("z", {1})
        csp.add_constraint("x", "y", lambda a, b: a == b, cid="eq")
        csp.add_constraint("x", "z", lambda a, b: a >= b + 2, cid="xz")
        csp.add_constraint("y", "z", lambda a, b: a >= b + 1, cid="yz")
        self.assertEqual(csp.domains, {"x": {3}, "y": {3}, "z": {1}})
        csp.remove_constraint("eq")
        expected = reference_ac(
            {"x": {1, 2, 3}, "y": {1, 2, 3}, "z": {1}},
            [("x", "z", lambda a, b: a >= b + 2), ("y", "z", lambda a, b: a >= b + 1)],
        )
        self.assertEqual(csp.domains, expected)
        self.assertEqual(csp.domains, {"x": {3}, "y": {2, 3}, "z": {1}})
        redeleted = [d for d in csp.deletions if (d.var, d.value) == ("y", 1)]
        self.assertTrue(redeleted)
        self.assertEqual(redeleted[-1].reason.constraint_id, "yz")

    def test_removal_respects_branch_assignment(self):
        csp = CSP()
        csp.add_variable("v1", {1, 2, 4, 5})
        csp.add_variable("v4", {1})
        csp.add_constraint("v4", "v1", lambda a, b: abs(a - b) >= 2, cid="gap")
        self.assertEqual(csp.domains["v1"], {4, 5})
        csp.assign_value("v1", 4)
        csp.remove_constraint("gap")
        self.assertEqual(csp.domains["v1"], {4})

    def test_removal_clears_conflict(self):
        csp = CSP()
        csp.add_variable("x", {2})
        csp.add_variable("y", {1})
        csp.add_constraint("x", "y", lambda a, b: a < b, cid="lt")
        self.assertTrue(csp.conflict)
        csp.remove_constraint("lt")
        self.assertFalse(csp.conflict)
        self.assertEqual(csp.domains, {"x": {2}, "y": {1}})


class TestSolve(unittest.TestCase):
    def test_solve_four_queens_and_verify(self):
        csp = CSP()
        n = 4
        for i in range(n):
            csp.add_variable(f"q{i}", set(range(n)))
        constraints = []
        for i in range(n):
            for j in range(i + 1, n):
                pred = lambda a, b, d=j - i: a != b and abs(a - b) != d
                csp.add_constraint(f"q{i}", f"q{j}", pred)
                constraints.append((f"q{i}", f"q{j}", pred))
        sol = csp.solve()
        self.assertIsNotNone(sol)
        self.assertTrue(check_solution(sol, constraints))
        all_sols = enumerate_solutions({f"q{i}": set(range(n)) for i in range(n)}, constraints)
        self.assertEqual(len(all_sols), 2)
        self.assertIn(sol, all_sols)

    def test_solve_unsat_matches_enumeration(self):
        csp = CSP()
        csp.add_variable("x", {1, 2})
        csp.add_variable("y", {1, 2})
        csp.add_constraint("x", "y", lambda a, b: a < b)
        csp.add_constraint("x", "y", lambda a, b: a > b)
        self.assertIsNone(csp.solve())
        sols = enumerate_solutions(
            {"x": {1, 2}, "y": {1, 2}},
            [("x", "y", lambda a, b: a < b), ("x", "y", lambda a, b: a > b)],
        )
        self.assertEqual(sols, [])


class TestRandomSessions(unittest.TestCase):
    """Random operation sessions cross-validated against the independent
    full-AC reference and full assignment enumeration."""

    def test_random_sessions_against_reference(self):
        rng = random.Random(20231007)
        for session in range(25):
            csp = CSP()
            names = [f"v{i}" for i in range(rng.randint(3, 5))]
            original = {}
            for n in names:
                dom = set(rng.sample(range(6), rng.randint(1, 4)))
                original[n] = dom
                csp.add_variable(n, dom)
            active = {}
            assigns = {}
            stack = []
            for step in range(60):
                op = rng.random()
                if op < 0.30:
                    a, b = rng.sample(names, 2)
                    desc, pred = rng.choice(PREDICATES)
                    cid = csp.add_constraint(a, b, pred, description=desc)
                    active[cid] = (a, b, pred)
                elif op < 0.50 and active:
                    cid = rng.choice(sorted(active, key=repr))
                    csp.remove_constraint(cid)
                    del active[cid]
                elif op < 0.62:
                    csp.push()
                    stack.append((dict(active), dict(assigns)))
                elif op < 0.74 and stack:
                    csp.pop()
                    active, assigns = stack.pop()
                elif op < 0.86 and not csp.conflict:
                    cand = [v for v in names if len(csp.domains[v]) > 1]
                    if cand:
                        v = rng.choice(cand)
                        val = rng.choice(sorted(csp.domains[v]))
                        csp.assign_value(v, val)
                        assigns[v] = val
                else:
                    csp.propagate()
                where = f"session {session} step {step}"
                model_domains = {
                    v: ({assigns[v]} if v in assigns else set(original[v]))
                    for v in names
                }
                model_constraints = list(active.values())
                ref = reference_ac(model_domains, model_constraints)
                ref_empty = any(not d for d in ref.values())
                if csp.conflict:
                    self.assertTrue(ref_empty, where)
                    continue
                self.assertFalse(ref_empty, where)
                self.assertEqual(csp.domains, ref, where)
                self.assertEqual(set(csp.constraints), set(active), where)
                pending = csp.pending_arcs
                self.assertEqual(len(pending), len(set(pending)), where)
                if step % 3 == 0:
                    sols = enumerate_solutions(model_domains, model_constraints, limit=200)
                    for sol in sols:
                        for v, val in sol.items():
                            self.assertIn(val, csp.domains[v], where)
                    if all(len(d) == 1 for d in ref.values()):
                        assignment = {v: next(iter(d)) for v, d in ref.items()}
                        self.assertTrue(
                            check_solution(assignment, model_constraints), where
                        )

    def test_random_solve_verified_by_independent_checker(self):
        rng = random.Random(99)
        for trial in range(40):
            csp = CSP()
            names = [f"v{i}" for i in range(rng.randint(3, 5))]
            full = {}
            for n in names:
                dom = set(rng.sample(range(6), rng.randint(1, 4)))
                full[n] = dom
                csp.add_variable(n, dom)
            constraints = []
            for _ in range(rng.randint(0, 8)):
                a, b = rng.sample(names, 2)
                _, pred = rng.choice(PREDICATES)
                csp.add_constraint(a, b, pred)
                constraints.append((a, b, pred))
            sol = csp.solve()
            if sol is None:
                self.assertEqual(enumerate_solutions(full, constraints), [])
            else:
                self.assertEqual(set(sol), set(names))
                self.assertTrue(check_solution(sol, constraints))


if __name__ == "__main__":
    unittest.main(verbosity=2)
