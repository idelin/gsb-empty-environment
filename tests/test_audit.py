import unittest
from dataclasses import replace

from csp_audit import (BACKTRACK, CONFLICT, DECIDE, PROPAGATE, Constraint,
                       Event, Problem, Solver, Status, Trail, check_sat,
                       replay)


def tiny_problem():
    # a, b in {1, 2}, a != b
    return Problem(
        ["a", "b"],
        {"a": {1, 2}, "b": {1, 2}},
        [Constraint("neq", ("a", "b"), lambda t: t[0] != t[1])],
    )


def queens(n):
    variables = [f"q{r}" for r in range(n)]
    domains = {v: set(range(n)) for v in variables}
    constraints = []
    for r1 in range(n):
        for r2 in range(r1 + 1, n):
            gap = r2 - r1
            constraints.append(Constraint(
                f"q{r1}_q{r2}", (f"q{r1}", f"q{r2}"),
                lambda t, gap=gap: t[0] != t[1] and abs(t[0] - t[1]) != gap))
    return Problem(variables, domains, constraints)


def k4_3coloring():
    nodes = [1, 2, 3, 4]
    edges = [(u, v) for i, u in enumerate(nodes) for v in nodes[i + 1:]]
    domains = {f"x{n}": {1, 2, 3} for n in nodes}
    constraints = [
        Constraint(f"e{u}_{v}", (f"x{u}", f"x{v}"), lambda t: t[0] != t[1])
        for u, v in edges
    ]
    return Problem([f"x{n}" for n in nodes], domains, constraints)


class TestSatPath(unittest.TestCase):
    def test_sat_verified_by_independent_checker(self):
        problem = queens(4)
        result = Solver(problem).solve()
        self.assertIs(result.status, Status.SAT)
        # complete assignment: every variable, exactly one in-domain value
        self.assertEqual(set(result.assignment), set(problem.variables))
        self.assertTrue(check_sat(problem, result.assignment))
        rep = replay(problem, result.trail, "SAT", result.assignment)
        self.assertTrue(rep.ok, rep.message)
        self.assertTrue(rep.verdict_supported)

    def test_check_sat_rejects_bad_claims(self):
        problem = tiny_problem()
        self.assertTrue(check_sat(problem, {"a": 1, "b": 2}))
        self.assertFalse(check_sat(problem, {"a": 1, "b": 1}))      # violates neq
        self.assertFalse(check_sat(problem, {"a": 1}))              # incomplete
        self.assertFalse(check_sat(problem, {"a": 1, "b": 3}))      # out of domain
        self.assertFalse(check_sat(problem, {"a": 1, "b": 2, "c": 1}))  # extra var

    def test_incomplete_assignment_cannot_claim_sat(self):
        problem = tiny_problem()
        # Valid prefix: DECIDE a=1, but b still has two values -> not a leaf.
        trail = Trail()
        trail.record(Event(DECIDE, "a", 1, None, 0))
        rep = replay(problem, trail, "SAT", {"a": 1, "b": 2})
        self.assertFalse(rep.ok)
        self.assertIn("not complete", rep.message)


class TestUnsatPath(unittest.TestCase):
    def test_unsat_supported_by_trail(self):
        problem = k4_3coloring()
        result = Solver(problem).solve()
        self.assertIs(result.status, Status.UNSAT)
        rep = replay(problem, result.trail, "UNSAT")
        self.assertTrue(rep.ok, rep.message)
        self.assertTrue(rep.verdict_supported)

    def test_root_conflict_supports_unsat(self):
        # x in {1}, y in {1}, x != y -> propagation empties y at root.
        problem = Problem(["x", "y"], {"x": {1}, "y": {1}},
                          [Constraint("neq", ("x", "y"), lambda t: t[0] != t[1])])
        result = Solver(problem).solve()
        self.assertIs(result.status, Status.UNSAT)
        self.assertTrue(any(e.kind == CONFLICT for e in result.trail))
        rep = replay(problem, result.trail, "UNSAT")
        self.assertTrue(rep.ok, rep.message)

    def test_unknown_is_not_unsat(self):
        problem = queens(8)
        result = Solver(problem, max_events=3).solve()
        self.assertIs(result.status, Status.UNKNOWN)
        self.assertIsNot(result.status, Status.UNSAT)
        rep = replay(problem, result.trail, result.status.value)
        self.assertTrue(rep.ok, rep.message)
        self.assertFalse(rep.verdict_supported)
        # an UNKNOWN-producing truncated trail must not pass as UNSAT
        rep_bad = replay(problem, result.trail, "UNSAT")
        self.assertFalse(rep_bad.ok)


class TestReplayRules(unittest.TestCase):
    def test_unjustified_domain_removal_fails(self):
        problem = tiny_problem()
        trail = Trail()
        # b=2 has support (a=1, b=2 satisfies neq): deletion is unjustified.
        trail.record(Event(PROPAGATE, "b", 2, "neq", 0))
        rep = replay(problem, trail, "UNKNOWN")
        self.assertFalse(rep.ok)
        self.assertEqual(rep.first_bad_index, 0)
        self.assertIn("unjustified deletion", rep.message)

    def test_propagate_unknown_constraint_fails(self):
        problem = tiny_problem()
        trail = Trail()
        trail.record(Event(PROPAGATE, "b", 2, "nope", 0))
        rep = replay(problem, trail, "UNKNOWN")
        self.assertFalse(rep.ok)
        self.assertEqual(rep.first_bad_index, 0)

    def test_decide_value_not_in_domain_fails(self):
        problem = tiny_problem()
        trail = Trail()
        trail.record(Event(DECIDE, "a", 7, None, 0))
        rep = replay(problem, trail, "UNKNOWN")
        self.assertFalse(rep.ok)
        self.assertEqual(rep.first_bad_index, 0)

    def test_fake_conflict_fails(self):
        problem = tiny_problem()
        trail = Trail()
        trail.record(Event(CONFLICT, "a", None, "neq", 0))
        rep = replay(problem, trail, "UNKNOWN")
        self.assertFalse(rep.ok)
        self.assertEqual(rep.first_bad_index, 0)
        self.assertIn("non-empty", rep.message)

    def test_backtrack_without_evidence_fails(self):
        problem = tiny_problem()
        trail = Trail()
        trail.record(Event(DECIDE, "a", 1, None, 0))
        # no conflict, no exhausted child -> backtracking a=1 is unsupported
        trail.record(Event(BACKTRACK, "a", 1, None, 0))
        rep = replay(problem, trail, "UNKNOWN")
        self.assertFalse(rep.ok)
        self.assertEqual(rep.first_bad_index, 1)
        self.assertIn("failure evidence", rep.message)

    def test_backtrack_mismatched_decision_fails(self):
        problem = tiny_problem()
        trail = Trail()
        trail.record(Event(DECIDE, "a", 1, None, 0))
        trail.record(Event(BACKTRACK, "a", 2, None, 0))
        rep = replay(problem, trail, "UNKNOWN")
        self.assertFalse(rep.ok)
        self.assertEqual(rep.first_bad_index, 1)


class TestTamperDetection(unittest.TestCase):
    def _solve(self):
        problem = queens(4)
        result = Solver(problem).solve()
        assert result.status is Status.SAT
        return problem, result

    def test_tampered_propagation_locates_first_illegal_event(self):
        problem, result = self._solve()
        events = list(result.trail)
        idx = next(i for i, e in enumerate(events) if e.kind == PROPAGATE)
        # forge the justifying constraint id
        events[idx] = replace(events[idx], cid="forged")
        tampered = Trail()
        tampered.events = events
        rep = replay(problem, tampered, "SAT", result.assignment)
        self.assertFalse(rep.ok)
        self.assertEqual(rep.first_bad_index, idx)

    def test_tampered_value_locates_first_illegal_event(self):
        # hand-built trail on a != b, a,b in {1,2}
        problem = tiny_problem()
        result = Solver(problem).solve()
        self.assertIs(result.status, Status.SAT)
        events = list(result.trail)
        idx = next(i for i, e in enumerate(events) if e.kind == PROPAGATE)
        victim = events[idx]
        # replace the pruned value with one that still has support
        supported = next(
            v for v in problem.domains[victim.var]
            if problem.constraint(victim.cid).has_support(
                victim.var, v, {"a": {1}, "b": {1, 2}}))
        events[idx] = replace(victim, value=supported)
        tampered = Trail()
        tampered.events = events
        rep = replay(problem, tampered, "SAT", result.assignment)
        self.assertFalse(rep.ok)
        self.assertEqual(rep.first_bad_index, idx)
        self.assertIn("unjustified deletion", rep.message)

    def test_deleted_event_locates_first_illegal_event(self):
        problem, result = self._solve()
        events = list(result.trail)
        idx = next(i for i, e in enumerate(events) if e.kind == DECIDE)
        del events[idx]  # following events now sit at a wrong depth
        tampered = Trail()
        tampered.events = events
        rep = replay(problem, tampered, "SAT", result.assignment)
        self.assertFalse(rep.ok)
        self.assertEqual(rep.first_bad_index, idx)

    def test_forged_unsat_from_sat_trail_fails(self):
        problem, result = self._solve()
        rep = replay(problem, result.trail, "UNSAT")
        self.assertFalse(rep.ok)


class TestSnapshots(unittest.TestCase):
    def test_restore_roundtrip(self):
        problem = queens(4)
        solver = Solver(problem)
        before = {v: set(d) for v, d in solver.domains.items()}
        solver._save(0)
        solver.domains["q0"] = {9}
        solver.domains["q1"] = set()
        solver._restore(0)
        self.assertEqual(solver.domains, before)

    def test_backtrack_restores_values_removed_by_propagation(self):
        # 4-queens search backtracks; a value pruned inside a failed branch
        # must be usable again after the snapshot is restored. The replay
        # checker maintains its own snapshots, so a broken restore anywhere
        # would make some later event illegal.
        problem = queens(4)
        result = Solver(problem).solve()
        self.assertIs(result.status, Status.SAT)
        events = list(result.trail)
        bt = next(i for i, e in enumerate(events) if e.kind == BACKTRACK)
        pruned_before = {(e.var, e.value) for e in events[:bt]
                         if e.kind == PROPAGATE}
        pruned_after = {(e.var, e.value) for e in events[bt:]
                        if e.kind == PROPAGATE}
        # a value pruned inside the failed branch is pruned again after the
        # backtrack: only possible if the snapshot restore brought it back
        self.assertTrue(pruned_before & pruned_after,
                        "expected a pruned value to reappear after restore")
        rep = replay(problem, result.trail, "SAT", result.assignment)
        self.assertTrue(rep.ok, rep.message)


if __name__ == "__main__":
    unittest.main()
