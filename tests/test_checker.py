"""unittest suite for resource_checker (Python 3.11, stdlib only)."""
from __future__ import annotations

import unittest

from resource_checker import concrete as C
from resource_checker import examples, model as M
from resource_checker.analyzer import (
    LEAK,
    PENDING,
    SAFE,
    VIOLATED,
    WARNING,
    Analyzer,
)
from resource_checker.exhaustive import cross_check, enumerate_paths, ground_truth
from resource_checker.model import Builder, Edge, Op


def analyze(factory, **kwargs):
    return Analyzer(factory(), **kwargs).run()


def findings(report, kind=None, status=None):
    out = report.findings
    if kind is not None:
        out = [f for f in out if f.kind == kind]
    if status is not None:
        out = [f for f in out if f.status == status]
    return out


class TestExits(unittest.TestCase):
    def test_clean_program_is_safe(self):
        report = analyze(examples.clean_program)
        self.assertEqual(report.status, SAFE)
        self.assertEqual(report.findings, [])
        self.assertTrue(report.complete)

    def test_leak_on_normal_exit_is_definite(self):
        report = analyze(examples.leak_on_normal_exit)
        self.assertEqual(report.status, VIOLATED)
        (leak,) = findings(report, LEAK, "definite")
        self.assertEqual(leak.exit_kind, M.EXIT_NORMAL)
        self.assertEqual(leak.node, "RET")
        self.assertIn("replay", leak.evidence)

    def test_finally_cleanup_is_safe(self):
        report = analyze(examples.exception_with_finally)
        self.assertEqual(report.status, SAFE)
        self.assertEqual(report.findings, [])

    def test_exception_cleanup_path_is_searched(self):
        # the leak only exists on the exception/finally path
        report = analyze(examples.exception_missing_cleanup)
        self.assertEqual(report.status, VIOLATED)
        (leak,) = findings(report, LEAK, "definite")
        self.assertEqual(leak.exit_kind, M.EXIT_EXCEPTION)
        self.assertIn("fin", [node for node, _ in leak.path])

    def test_cancel_path_is_searched(self):
        report = analyze(examples.cancel_leak)
        self.assertEqual(report.status, VIOLATED)
        (leak,) = findings(report, LEAK, "definite")
        self.assertEqual(leak.exit_kind, M.EXIT_CANCEL)
        self.assertEqual(leak.node, "CXL")


class TestOwnershipTransfer(unittest.TestCase):
    def test_new_owner_may_release(self):
        report = analyze(examples.transfer_ok)
        self.assertEqual(report.status, SAFE)

    def test_old_owner_cannot_release_after_move(self):
        report = analyze(examples.release_after_move)
        self.assertEqual(report.status, VIOLATED)
        (err,) = findings(report, C.RELEASE_AFTER_MOVE, "definite")
        self.assertEqual(err.node, "n2")

    def test_double_release_is_a_distinct_error(self):
        report = analyze(examples.double_release)
        self.assertEqual(report.status, VIOLATED)
        (err,) = findings(report, C.DOUBLE_RELEASE, "definite")
        self.assertEqual(err.node, "n2")
        # not reported as release-after-move: the two errors are distinct
        self.assertEqual(findings(report, C.RELEASE_AFTER_MOVE), [])

    def test_use_after_move(self):
        b = Builder()
        b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
        b.node("n1", Op(M.TRANSFER, ("x", "y")), [Edge(M.NORMAL, "n2")])
        b.node("n2", Op(M.USE, ("x",)), [Edge(M.NORMAL, "n3")])
        b.node("n3", Op(M.RELEASE, ("y",)), [Edge(M.NORMAL, "RET")])
        b.exit("RET")
        report = Analyzer(b.build("n0")).run()
        (err,) = findings(report, C.USE_AFTER_MOVE, "definite")
        self.assertEqual(err.node, "n2")

    def test_transfer_does_not_touch_other_resources(self):
        # ownership is tracked per resource, not via a global refcount:
        # transferring x must not affect the independent resource in z
        b = Builder()
        b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
        b.node("n1", Op(M.ACQUIRE, ("z",)), [Edge(M.NORMAL, "n2")])
        b.node("n2", Op(M.TRANSFER, ("x", "y")), [Edge(M.NORMAL, "n3")])
        b.node("n3", Op(M.RELEASE, ("z",)), [Edge(M.NORMAL, "n4")])
        b.node("n4", Op(M.RELEASE, ("y",)), [Edge(M.NORMAL, "RET")])
        b.exit("RET")
        report = Analyzer(b.build("n0")).run()
        self.assertEqual(report.status, SAFE)
        self.assertEqual(report.findings, [])


class TestBorrows(unittest.TestCase):
    def test_release_while_borrowed(self):
        report = analyze(examples.borrow_then_release)
        self.assertEqual(report.status, VIOLATED)
        (err,) = findings(report, C.RELEASE_WHILE_BORROWED, "definite")
        self.assertEqual(err.node, "n2")

    def test_borrow_then_end_then_release_is_safe(self):
        report = analyze(examples.borrow_ok)
        self.assertEqual(report.status, SAFE)


class TestLeakClassification(unittest.TestCase):
    def test_possible_leak_has_distinct_evidence(self):
        report = analyze(examples.maybe_alias_leak)
        self.assertEqual(report.status, WARNING)
        possible = findings(report, LEAK, "possible")
        self.assertEqual(len(possible), 2)
        for leak in possible:
            # possible leaks carry a concrete witness plus the run ratio,
            # definite leaks state that *all* runs leak
            self.assertIn("1/2", leak.evidence)
        self.assertEqual(findings(report, LEAK, "definite"), [])

    def test_false_positive_is_filtered_by_replay(self):
        report = analyze(examples.false_positive_exception)
        self.assertEqual(report.status, SAFE)
        self.assertEqual(report.findings, [])
        (filtered,) = report.filtered
        self.assertEqual(filtered.kind, LEAK)
        self.assertIn("infeasible", filtered.evidence)

    def test_inconclusive_replay_stays_possible(self):
        # with a replay bound of 1 run, the maybe_alias nondeterminism
        # cannot be resolved: the candidate must remain "possible"
        report = analyze(examples.maybe_alias_leak, replay_max_runs=1)
        leaks = findings(report, LEAK)
        self.assertTrue(leaks)
        self.assertTrue(all(f.status == "possible" for f in leaks))


class TestPending(unittest.TestCase):
    def test_unfinished_search_is_pending(self):
        report = analyze(examples.exception_missing_cleanup, max_steps=1)
        self.assertEqual(report.status, PENDING)
        self.assertFalse(report.complete)

    def test_pending_is_not_reported_as_safe(self):
        report = analyze(examples.clean_program, max_steps=1)
        self.assertEqual(report.status, PENDING)
        self.assertNotEqual(report.status, SAFE)


class TestReplayableCounterexamples(unittest.TestCase):
    def test_definite_leak_witness_replays(self):
        report = analyze(examples.exception_missing_cleanup)
        (leak,) = findings(report, LEAK, "definite")
        result = C.replay(examples.exception_missing_cleanup(), leak.path)
        self.assertTrue(result.runs)
        for run in result.runs:
            self.assertTrue(C.leaked_resources(run, "n0"))

    def test_definite_error_witness_replays(self):
        report = analyze(examples.double_release)
        (err,) = findings(report, C.DOUBLE_RELEASE, "definite")
        result = C.replay(examples.double_release(), err.path)
        self.assertTrue(
            any(
                event.kind == C.DOUBLE_RELEASE and event.node == "n2"
                for run in result.runs
                for event in run.events
            )
        )


class TestLoops(unittest.TestCase):
    def test_loop_overwrite_leak(self):
        report = analyze(examples.loop_overwrite_leak)
        self.assertEqual(report.status, VIOLATED)
        leaks = findings(report, LEAK, "definite")
        self.assertTrue(any(f.exit_kind == "overwrite" for f in leaks))

    def test_loop_analysis_terminates(self):
        report = analyze(examples.loop_overwrite_leak)
        self.assertTrue(report.complete)


class TestExhaustiveCrossCheck(unittest.TestCase):
    """Small programs: enumerate every path, replay concretely, and require
    the analyzer to agree exactly with the concrete ground truth."""

    def test_all_acyclic_examples_match_ground_truth(self):
        for name in examples.CROSS_CHECKED:
            with self.subTest(example=name):
                result = cross_check(examples.EXAMPLES[name][0]())
                self.assertFalse(result.pending)
                self.assertEqual(result.missed_errors, set())
                self.assertEqual(result.missed_leaks, set())
                self.assertEqual(result.spurious_errors, set())
                self.assertEqual(result.spurious_leaks, set())
                self.assertTrue(result.program_ok)

    def test_ground_truth_itself_is_sane(self):
        truth = ground_truth(examples.exception_missing_cleanup())
        self.assertEqual(truth.leaks, {("EXC", "n0")})
        self.assertEqual(truth.errors, set())
        paths = enumerate_paths(examples.exception_missing_cleanup())
        self.assertEqual(len(paths), 2)  # normal path + finally path


if __name__ == "__main__":
    unittest.main()
