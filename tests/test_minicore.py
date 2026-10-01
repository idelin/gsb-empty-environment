"""unittest suite for the minicore package (Python 3.11, stdlib only)."""

from __future__ import annotations

import random
import unittest
from unittest import mock

from minicore import (
    CachedSolver,
    InternalSolverError,
    Status,
    check_assignment,
    deletion_based_core,
    enumerate_all_subsets,
    minimum_cardinality_core,
    solve,
    verify_core,
)
from minicore.cache import CachedSolver as _CachedSolverCls
from minicore.demo import order_sensitive_problem, tiny_sat_problem, tiny_unsat_problem
from minicore.solver import Constraint, Problem, SolveResult


class SolverTests(unittest.TestCase):
    def test_sat_with_witness(self):
        result = solve(tiny_sat_problem())
        self.assertIs(result.status, Status.SAT)
        self.assertEqual(check_assignment(tiny_sat_problem(), (0, 1), result.witness), [])

    def test_unsat_only_after_full_search(self):
        result = solve(tiny_unsat_problem())
        self.assertIs(result.status, Status.UNSAT)
        self.assertIsNone(result.witness)

    def test_unknown_on_budget_exhaustion(self):
        result = solve(tiny_unsat_problem(), budget=0)
        self.assertIs(result.status, Status.UNKNOWN)
        self.assertIsNone(result.witness)

    def test_empty_assumption_set_is_sat(self):
        result = solve(tiny_unsat_problem(), indices=())
        self.assertIs(result.status, Status.SAT)


class CheckerTests(unittest.TestCase):
    def setUp(self):
        self.problem = tiny_sat_problem()

    def test_valid_assignment(self):
        self.assertEqual(check_assignment(self.problem, (0, 1), {"x": 2}), [])

    def test_constraint_violation(self):
        errors = check_assignment(self.problem, (0, 1), {"x": 0})
        self.assertTrue(any("c1" in e for e in errors))

    def test_out_of_domain(self):
        errors = check_assignment(self.problem, (0, 1), {"x": 9})
        self.assertTrue(any("outside domain" in e for e in errors))

    def test_unassigned_variable(self):
        errors = check_assignment(self.problem, (0, 1), {})
        self.assertTrue(any("unassigned" in e for e in errors))


class CacheTests(unittest.TestCase):
    def setUp(self):
        self.problem = order_sensitive_problem()
        self.solver = CachedSolver(self.problem, per_call_budget=1_000)

    def test_cache_hit_binds_assumption_set_version(self):
        first = self.solver.solve((0, 2))
        second = self.solver.solve((2, 0))  # same assumption set, other order
        self.assertIs(first, second)
        self.assertEqual(self.solver.hits, 1)
        self.assertEqual(self.solver.solver_calls, 1)

    def test_invalidate_bumps_version_and_clears(self):
        self.solver.solve((0, 2))
        self.solver.invalidate()
        self.assertEqual(self.solver.version, 1)
        self.solver.solve((0, 2))
        self.assertEqual(self.solver.solver_calls, 2)

    def test_sat_claims_are_independently_checked(self):
        bogus = SolveResult(Status.SAT, witness={"a": 5, "b": 9}, nodes=1)
        with mock.patch("minicore.cache.solve", return_value=bogus):
            with self.assertRaises(InternalSolverError):
                self.solver.solve((0, 1))


class DeletionCoreTests(unittest.TestCase):
    def setUp(self):
        self.problem = order_sensitive_problem()
        self.solver = CachedSolver(self.problem)
        self.result = deletion_based_core(self.problem, self.solver)

    def test_core_is_subset_minimal_size_three(self):
        self.assertEqual(self.result.kind, "subset_minimal")
        self.assertEqual(self.result.core, (2, 3, 4))  # k3, k4, k5
        self.assertFalse(self.result.undecided)

    def test_check_records_are_complete(self):
        phases = [r.phase for r in self.result.records]
        self.assertEqual(phases[0], "initial")
        self.assertEqual(phases[-1], "final_verify")
        removed = [r for r in self.result.records if r.decision == "removed"]
        kept = [r for r in self.result.records if r.decision == "kept_sat"]
        self.assertEqual(len(removed), 2)
        self.assertEqual(len(kept), 3)
        for rec in self.result.records:
            self.assertIn(rec.status, (Status.SAT, Status.UNSAT))
            self.assertGreaterEqual(rec.nodes, 0)

    def test_no_conflict_instance(self):
        result = deletion_based_core(tiny_sat_problem(), CachedSolver(tiny_sat_problem()))
        self.assertEqual(result.kind, "no_conflict")
        self.assertEqual(result.core, ())


class MinimumCardinalityTests(unittest.TestCase):
    def setUp(self):
        self.problem = order_sensitive_problem()
        self.result = minimum_cardinality_core(self.problem, CachedSolver(self.problem))

    def test_minimum_cardinality_core_size_two(self):
        self.assertEqual(self.result.kind, "minimum_cardinality")
        self.assertIn(self.result.core, ((0, 2), (1, 3)))
        self.assertEqual(len(self.result.core), 2)

    def test_lower_bound_certificate_closes_gap(self):
        self.assertEqual(self.result.lower_bound, 2)
        self.assertEqual(self.result.lower_bound, len(self.result.core))
        last = self.result.records[-1]
        self.assertEqual(last.decision, "core_found")
        self.assertIs(last.status, Status.UNSAT)

    def test_subset_minimal_is_not_minimum_cardinality(self):
        deletion = deletion_based_core(self.problem, CachedSolver(self.problem))
        self.assertGreater(len(deletion.core), len(self.result.core))


class UnknownPropagationTests(unittest.TestCase):
    def setUp(self):
        self.problem = order_sensitive_problem()

    def test_starved_budget_initial_call(self):
        solver = CachedSolver(self.problem, per_call_budget=0)
        result = deletion_based_core(self.problem, solver, per_call_budget=0)
        self.assertEqual(result.kind, "undecided")
        self.assertTrue(result.undecided)

    def test_unknown_never_treated_as_unsat(self):
        # Initial UNSAT proof of the full pool costs exactly 2 nodes; allow
        # exactly that, so every later check hits an exhausted budget.
        solver = CachedSolver(self.problem, per_call_budget=6)
        result = deletion_based_core(
            self.problem, solver, per_call_budget=6, total_budget=2
        )
        self.assertTrue(result.undecided)
        self.assertNotEqual(result.kind, "subset_minimal")
        self.assertFalse(
            any(r.decision == "removed" for r in result.records),
            "no item may be removed on an UNKNOWN check",
        )
        unknown_records = [r for r in result.records if r.status is Status.UNKNOWN]
        self.assertTrue(unknown_records)
        for rec in unknown_records:
            self.assertNotEqual(rec.decision, "removed")

    def test_unknown_never_treated_as_sat(self):
        solver = CachedSolver(self.problem, per_call_budget=6)
        result = deletion_based_core(
            self.problem, solver, per_call_budget=6, total_budget=2
        )
        self.assertFalse(
            any(r.decision == "kept_sat" and r.status is Status.UNKNOWN
                for r in result.records),
            "UNKNOWN must not count as a SAT deletion witness",
        )

    def test_hitting_set_unknown_keeps_lower_bound(self):
        solver = CachedSolver(self.problem, per_call_budget=0)
        result = minimum_cardinality_core(self.problem, solver, per_call_budget=0)
        self.assertEqual(result.kind, "undecided")
        self.assertTrue(result.undecided)
        self.assertEqual(result.core, ())
        self.assertGreaterEqual(result.lower_bound, 0)


class EnumerationTests(unittest.TestCase):
    def setUp(self):
        self.problem = order_sensitive_problem()
        self.table = enumerate_all_subsets(self.problem)

    def test_enumeration_is_decisive(self):
        self.assertEqual(len(self.table), 32)
        self.assertNotIn(Status.UNKNOWN, self.table.values())

    def test_mus_classification(self):
        report = verify_core(self.problem, (2, 3, 4), table=self.table)
        self.assertEqual(
            report.all_muses, [(0, 2), (1, 3), (0, 1, 4), (2, 3, 4)]
        )
        self.assertEqual(report.min_cardinality, 2)

    def test_deletion_core_category_and_recovery(self):
        report = verify_core(self.problem, (2, 3, 4), table=self.table)
        self.assertIs(report.core_status, Status.UNSAT)
        self.assertTrue(report.is_subset_minimal)
        self.assertFalse(report.is_minimum_cardinality)
        self.assertTrue(report.recovery_consistent)
        self.assertTrue(
            all(s is Status.SAT for s in report.deletion_witnesses.values())
        )

    def test_min_card_core_category_and_recovery(self):
        report = verify_core(self.problem, (0, 2), table=self.table)
        self.assertTrue(report.is_subset_minimal)
        self.assertTrue(report.is_minimum_cardinality)
        # Removing {k1,k3} leaves {k2,k4,k5} which still contains {k2,k4}:
        # a minimum-cardinality core need not restore consistency.
        self.assertFalse(report.recovery_consistent)


def _random_problem(rng: random.Random) -> Problem:
    domains = {"x": (0, 1, 2), "y": (0, 1, 2), "z": (0, 1, 2)}
    vars3 = ("x", "y", "z")
    cons = []
    for i in range(5):
        kind = rng.randrange(6)
        if kind == 0:
            v, c = rng.choice(vars3), rng.randrange(3)
            cons.append(Constraint(f"r{i}: {v}=={c}", (v,),
                                   lambda val, c=c: val == c))
        elif kind == 1:
            v, c = rng.choice(vars3), rng.randrange(3)
            cons.append(Constraint(f"r{i}: {v}!={c}", (v,),
                                   lambda val, c=c: val != c))
        elif kind == 2:
            u, v = rng.sample(vars3, 2)
            k = rng.randrange(5)
            cons.append(Constraint(f"r{i}: {u}+{v}<={k}", (u, v),
                                   lambda a, b, k=k: a + b <= k))
        elif kind == 3:
            u, v = rng.sample(vars3, 2)
            k = rng.randrange(5)
            cons.append(Constraint(f"r{i}: {u}+{v}>={k}", (u, v),
                                   lambda a, b, k=k: a + b >= k))
        elif kind == 4:
            u, v = rng.sample(vars3, 2)
            cons.append(Constraint(f"r{i}: {u}=={v}", (u, v),
                                   lambda a, b: a == b))
        else:
            u, v = rng.sample(vars3, 2)
            cons.append(Constraint(f"r{i}: {u}!={v}", (u, v),
                                   lambda a, b: a != b))
    return Problem(domains, tuple(cons))


class RandomizedCrossCheckTests(unittest.TestCase):
    """Extraction algorithms vs independent full-subset enumeration."""

    def test_random_small_instances(self):
        rng = random.Random(20261001)
        checked = 0
        attempts = 0
        while checked < 12 and attempts < 400:
            attempts += 1
            problem = _random_problem(rng)
            table = enumerate_all_subsets(problem)
            if Status.UNKNOWN in table.values():
                continue
            universe = frozenset(range(len(problem.constraints)))
            if table[universe] is not Status.UNSAT:
                continue
            checked += 1

            deletion = deletion_based_core(problem, CachedSolver(problem))
            self.assertEqual(deletion.kind, "subset_minimal")
            del_report = verify_core(problem, deletion.core, table=table)
            self.assertTrue(del_report.is_subset_minimal)
            self.assertFalse(del_report.is_minimum_cardinality
                             and len(deletion.core) != del_report.min_cardinality)

            min_card = minimum_cardinality_core(problem, CachedSolver(problem))
            self.assertEqual(min_card.kind, "minimum_cardinality")
            mc_report = verify_core(problem, min_card.core, table=table)
            self.assertTrue(mc_report.is_minimum_cardinality)
            self.assertEqual(len(min_card.core), mc_report.min_cardinality)
            self.assertEqual(min_card.lower_bound, len(min_card.core))
            self.assertLessEqual(len(min_card.core), len(deletion.core))

            # Recovery consistency reported by enumeration is well-defined.
            self.assertIsNotNone(del_report.recovery_consistent)
        self.assertGreaterEqual(checked, 12)


if __name__ == "__main__":
    unittest.main()
