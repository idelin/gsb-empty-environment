"""Unit tests for the traced CSP solver and the independent replay checker."""

from __future__ import annotations

import unittest

from csp import CSP, Constraint, check_solution
from checker import check_trace
from solver import SAT, UNKNOWN, UNSAT, Solver
from trace import Event


def neq(a, b, values):
    return Constraint((a, b), frozenset(
        (x, y) for x in values for y in values if x != y))


def map_coloring():
    colors = ("R", "G", "B")
    domains = {v: colors for v in ("WA", "NT", "SA", "Q")}
    edges = [("WA", "NT"), ("WA", "SA"), ("NT", "SA"), ("NT", "Q"), ("SA", "Q")]
    return CSP(domains, [neq(a, b, colors) for a, b in edges])


def triangle_two_coloring():
    vals = (1, 2)
    domains = {v: vals for v in ("A", "B", "C")}
    edges = [("A", "B"), ("B", "C"), ("A", "C")]
    return CSP(domains, [neq(a, b, vals) for a, b in edges])


def tiny():
    # x in {1,2}, y in {1}; x != y  =>  only solution x=2, y=1
    return CSP({"x": (1, 2), "y": (1,)}, [neq("x", "y", (1, 2))])


class TestValidTraces(unittest.TestCase):
    def test_sat_trace_verifies(self):
        csp = map_coloring()
        result = Solver(csp).solve()
        self.assertEqual(result.status, SAT)
        chk = check_trace(csp, result.trace)
        self.assertTrue(chk.ok, chk.message)
        self.assertEqual(chk.status, SAT)
        ok, msg = check_solution(csp, result.assignment)
        self.assertTrue(ok, msg)

    def test_unsat_trace_verifies(self):
        csp = triangle_two_coloring()
        result = Solver(csp).solve()
        self.assertEqual(result.status, UNSAT)
        chk = check_trace(csp, result.trace)
        self.assertTrue(chk.ok, chk.message)
        self.assertEqual(chk.status, UNSAT)

    def test_root_propagation_unsat_verifies(self):
        # x, y both forced to 1, but x != y: conflict before any decision.
        csp = CSP({"x": (1,), "y": (1,)}, [neq("x", "y", (1,))])
        result = Solver(csp).solve()
        self.assertEqual(result.status, UNSAT)
        kinds = [ev.kind for ev in result.trace]
        self.assertNotIn("decide", kinds)
        chk = check_trace(csp, result.trace)
        self.assertTrue(chk.ok, chk.message)
        self.assertEqual(chk.status, UNSAT)

    def test_backtrack_snapshots_restore_correctly(self):
        csp = triangle_two_coloring()
        result = Solver(csp).solve()
        backtracks = [ev for ev in result.trace if ev.kind == "backtrack"]
        self.assertTrue(backtracks)
        chk = check_trace(csp, result.trace)
        self.assertTrue(chk.ok, chk.message)  # replay validates each snapshot


class TestTamperDetection(unittest.TestCase):
    def test_tampered_decide_value_located(self):
        csp = map_coloring()
        result = Solver(csp).solve()
        i = next(ev.seq for ev in result.trace if ev.kind == "decide")
        bad = list(result.trace)
        bad[i] = Event("decide", i, {"var": "WA", "value": "Z"})
        chk = check_trace(csp, bad)
        self.assertFalse(chk.ok)
        self.assertEqual(chk.first_bad, i)

    def test_unjustified_domain_deletion_fails(self):
        csp = tiny()
        # x=1 still has support (tuple (1,2)? no; table is {(1,2),(2,1)} and
        # y=1... x=1 supports via (1,2)? y domain is {1}; tuple (1,2) needs
        # y=2. Actually x=2 supports y=1 via (2,1); deleting x=2 is the
        # unjustified move at the root.)
        trace = [Event("propagate", 0, {"var": "x", "value": 2, "constraint": 0})]
        chk = check_trace(csp, trace)
        self.assertFalse(chk.ok)
        self.assertEqual(chk.first_bad, 0)
        self.assertIn("unjustified deletion", chk.message)

    def test_tampered_propagation_value_located(self):
        csp = map_coloring()
        result = Solver(csp).solve()
        target = next(ev for ev in result.trace if ev.kind == "propagate")
        i = target.seq
        other = next(v for v in csp.domains[target.payload["var"]]
                     if v != target.payload["value"])
        bad = list(result.trace)
        bad[i] = Event("propagate", i, {**target.payload, "value": other})
        chk = check_trace(csp, bad)
        self.assertFalse(chk.ok)
        self.assertEqual(chk.first_bad, i)

    def test_tampered_backtrack_snapshot_located(self):
        csp = triangle_two_coloring()
        result = Solver(csp).solve()
        target = next(ev for ev in result.trace if ev.kind == "backtrack")
        i = target.seq
        forged = {v: list(vals) for v, vals in target.payload["domains"].items()}
        forged["A"] = [1, 2, 3]
        bad = list(result.trace)
        bad[i] = Event("backtrack", i, {"level": target.payload["level"],
                                        "domains": forged})
        chk = check_trace(csp, bad)
        self.assertFalse(chk.ok)
        self.assertEqual(chk.first_bad, i)

    def test_dropped_event_detected(self):
        csp = map_coloring()
        result = Solver(csp).solve()
        bad = result.trace[:3] + result.trace[4:]
        chk = check_trace(csp, bad)
        self.assertFalse(chk.ok)
        self.assertEqual(chk.first_bad, 3)

    def test_conflict_without_empty_domain_fails(self):
        csp = tiny()
        trace = [Event("conflict", 0, {"var": "x"})]
        chk = check_trace(csp, trace)
        self.assertFalse(chk.ok)
        self.assertEqual(chk.first_bad, 0)

    def test_premature_exhausted_fails(self):
        csp = tiny()
        # Try x=1 (conflicts), backtrack, then claim x exhausted without
        # ever trying x=2.
        trace = [
            Event("decide", 0, {"var": "x", "value": 1}),
            Event("propagate", 1, {"var": "y", "value": 1, "constraint": 0}),
            Event("conflict", 2, {"var": "y"}),
            Event("backtrack", 3, {"level": 0, "domains": {"x": [1, 2], "y": [1]}}),
            Event("exhausted", 4, {"var": "x"}),
            Event("unsat", 5, {}),
        ]
        chk = check_trace(csp, trace)
        self.assertFalse(chk.ok)
        self.assertEqual(chk.first_bad, 4)


class TestTerminalClaims(unittest.TestCase):
    def test_incomplete_solution_rejected_by_checker(self):
        csp = tiny()
        trace = [Event("solution", 0, {"assignment": {"x": 2}})]
        chk = check_trace(csp, trace)
        self.assertFalse(chk.ok)
        self.assertEqual(chk.first_bad, 0)

    def test_incomplete_solution_rejected_by_solution_checker(self):
        csp = map_coloring()
        ok, msg = check_solution(csp, {"WA": "R"})
        self.assertFalse(ok)
        self.assertIn("incomplete", msg)

    def test_solution_outside_original_domain_rejected(self):
        csp = tiny()
        ok, msg = check_solution(csp, {"x": 2, "y": 7})
        self.assertFalse(ok)
        self.assertIn("not in original domain", msg)

    def test_solution_violating_constraint_rejected(self):
        csp = tiny()
        ok, msg = check_solution(csp, {"x": 1, "y": 1})
        self.assertFalse(ok)
        self.assertIn("violated", msg)

    def test_unknown_is_not_unsat(self):
        csp = map_coloring()
        result = Solver(csp, max_events=3).solve()
        self.assertEqual(result.status, UNKNOWN)
        self.assertEqual(result.trace[-1].kind, "unknown")
        chk = check_trace(csp, result.trace)
        self.assertTrue(chk.ok, chk.message)
        self.assertEqual(chk.status, UNKNOWN)
        # Forge the terminal event into an UNSAT claim.
        forged = list(result.trace)
        forged[-1] = Event("unsat", forged[-1].seq, {})
        chk2 = check_trace(csp, forged)
        self.assertFalse(chk2.ok)
        self.assertEqual(chk2.first_bad, len(forged) - 1)

    def test_trace_without_terminal_event_fails(self):
        csp = tiny()
        trace = [Event("decide", 0, {"var": "x", "value": 1})]
        chk = check_trace(csp, trace)
        self.assertFalse(chk.ok)

    def test_event_after_terminal_fails(self):
        csp = triangle_two_coloring()
        result = Solver(csp).solve()
        bad = result.trace + [Event("decide", len(result.trace),
                                    {"var": "A", "value": 1})]
        chk = check_trace(csp, bad)
        self.assertFalse(chk.ok)
        self.assertEqual(chk.first_bad, len(result.trace))


if __name__ == "__main__":
    unittest.main()
