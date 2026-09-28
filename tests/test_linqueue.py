"""unittest suite for the queue linearizability checker."""
import unittest

from linqueue import (
    QueueModel, Ret, OK, EMPTY, FULL,
    enq, deq, pending_enq, pending_deq,
    check, Verdict, minimize, is_minimal,
    replay_counterexample, replay_witness,
)
from linqueue.counterexample import _simulate


def ok(value):
    return Ret("ok", value)


class LinearizableHistories(unittest.TestCase):

    def test_purely_sequential_history(self):
        model = QueueModel(capacity=2)
        ops = [
            enq(1, "A", 0, 1),
            enq(2, "B", 2, 3),
            deq(3, 4, 5, result=ok("A")),
            deq(4, 6, 7, result=ok("B")),
            deq(5, 8, 9, result=EMPTY),
        ]
        result = check(model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)
        self.assertEqual(tuple(op.id for op in result.linearization), (1, 2, 3, 4, 5))
        self.assertEqual(replay_witness(model, result.linearization), ())

    def test_overlapping_ops_may_be_reordered(self):
        # enq#2 is invoked after enq#1 but returns before it ("later starts,
        # earlier returns").  Real-time order forces #2 before #3 only; the
        # witness must place the slow #1 first so deq gets A.
        model = QueueModel(capacity=2)
        ops = [
            enq(1, "A", invoke=0, response=10),
            enq(2, "B", invoke=1, response=2),
            deq(3, invoke=3, response=4, result=ok("A")),
        ]
        result = check(model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)
        self.assertEqual(replay_witness(model, result.linearization), ("B",))
        order = [op.id for op in result.linearization]
        self.assertLess(order.index(2), order.index(3))  # #2 -> #3 edge

    def test_completion_time_ordering_is_not_enough(self):
        # The same history, sorted naively by response time, must fail:
        # enq#2 (res=2), deq#3 (res=4), enq#1 (res=10) dequeues B, not A.
        model = QueueModel(capacity=2)
        ops = [
            enq(1, "A", invoke=0, response=10),
            enq(2, "B", invoke=1, response=2),
            deq(3, invoke=3, response=4, result=ok("A")),
        ]
        by_completion = sorted(ops, key=lambda o: o.response)
        self.assertIsNotNone(_simulate(model, by_completion))
        # ...but the history itself is linearizable via real search.
        self.assertIs(check(model, ops).verdict, Verdict.LINEARIZABLE)

    def test_empty_exception_legal_on_empty_queue(self):
        model = QueueModel(capacity=1)
        ops = [
            enq(1, "A", 0, 1),
            deq(2, 2, 3, result=ok("A")),
            deq(3, 4, 5, result=EMPTY),
        ]
        self.assertIs(check(model, ops).verdict, Verdict.LINEARIZABLE)

    def test_full_exception_legal_on_full_queue(self):
        model = QueueModel(capacity=1)
        ops = [
            enq(1, "A", 0, 1, result=OK),
            enq(2, "B", 2, 3, result=FULL),
            deq(3, 4, 5, result=ok("A")),
        ]
        result = check(model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)
        self.assertEqual(tuple(op.id for op in result.linearization), (1, 2, 3))

    def test_pending_enq_can_be_dropped(self):
        model = QueueModel(capacity=1)
        ops = [
            pending_enq(1, "A", invoke=0),
            deq(2, invoke=1, response=2, result=EMPTY),
        ]
        result = check(model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)
        self.assertEqual(tuple(op.id for op in result.linearization), (2,))

    def test_pending_enq_must_take_effect(self):
        # The completed deq returned A; the only possible source is the
        # unfinished enq, so the witness is forced to include it.
        model = QueueModel(capacity=1)
        ops = [
            pending_enq(1, "A", invoke=0),
            deq(2, invoke=1, response=2, result=ok("A")),
        ]
        result = check(model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)
        self.assertEqual(tuple(op.id for op in result.linearization), (1, 2))
        self.assertEqual(replay_witness(model, result.linearization), ())

    def test_pending_op_complex_interleaving(self):
        # cap=1: completed enq A (slow), pending enq B, deq returns B.
        # Only witness: enq B (pending) -> deq B -> enq A returns OK on empty.
        model = QueueModel(capacity=1)
        ops = [
            enq(1, "A", invoke=0, response=5, result=OK),
            pending_enq(2, "B", invoke=1),
            deq(3, invoke=2, response=3, result=ok("B")),
        ]
        result = check(model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)
        self.assertEqual(tuple(op.id for op in result.linearization), (2, 3, 1))

    def test_pending_deq_treated_like_any_pending_call(self):
        model = QueueModel(capacity=1)
        ops = [
            enq(1, "A", 0, 1),
            pending_deq(2, invoke=1),
        ]
        # Pending deq may be dropped; completed history alone is fine.
        self.assertIs(check(model, ops).verdict, Verdict.LINEARIZABLE)


class NonLinearizableHistories(unittest.TestCase):

    def test_deq_value_must_come_from_legal_enq(self):
        model = QueueModel(capacity=1)
        ops = [
            enq(1, "A", invoke=0, response=1),
            deq(2, invoke=2, response=3, result=ok("B")),
        ]
        result = check(model, ops)
        self.assertIs(result.verdict, Verdict.NOT_LINEARIZABLE)

        report = replay_counterexample(model, ops)
        # Only one candidate order (both completed, real-time forces #1->#2).
        self.assertEqual(report.orders_explored, 1)
        self.assertEqual(len(report.failures), 1)
        self.assertEqual(report.failures[0].expected, ok("B"))
        self.assertEqual(report.failures[0].allowed, (ok("A"),))

    def test_deq_before_its_value_was_enqueued(self):
        model = QueueModel(capacity=1)
        ops = [
            deq(1, invoke=0, response=1, result=ok("A")),
            enq(2, "A", invoke=2, response=3),
        ]
        # Real-time order forces deq first; queue empty => only EMPTY legal.
        result = check(model, ops)
        self.assertIs(result.verdict, Verdict.NOT_LINEARIZABLE)
        report = replay_counterexample(model, ops)
        self.assertEqual(report.failures[0].allowed, (EMPTY,))

    def test_empty_exception_illegal_when_non_empty(self):
        model = QueueModel(capacity=2)
        ops = [
            enq(1, "A", invoke=0, response=1),
            deq(2, invoke=2, response=3, result=EMPTY),
        ]
        self.assertIs(check(model, ops).verdict, Verdict.NOT_LINEARIZABLE)

    def test_full_exception_illegal_when_not_full(self):
        model = QueueModel(capacity=2)
        ops = [enq(1, "A", invoke=0, response=1, result=FULL)]
        self.assertIs(check(model, ops).verdict, Verdict.NOT_LINEARIZABLE)


class CounterexampleMinimization(unittest.TestCase):

    def test_minimize_strips_irrelevant_operations(self):
        model = QueueModel(capacity=3)
        bad_core = [
            enq(1, "A", invoke=0, response=1),
            deq(2, invoke=2, response=3, result=EMPTY),  # impossible
        ]
        # Noise: independent successful enqueues that never form a
        # contradiction by themselves, even after other ops are removed.
        noise = [
            enq(3, "C", invoke=10, response=11),
            enq(4, "D", invoke=12, response=13),
        ]
        ops = bad_core + noise
        self.assertIs(check(model, ops).verdict, Verdict.NOT_LINEARIZABLE)

        core = minimize(model, ops)
        self.assertEqual(tuple(op.id for op in core), (1, 2))
        self.assertTrue(is_minimal(model, core))
        report = replay_counterexample(model, core)
        self.assertEqual(report.orders_explored, len(report.failures))
        self.assertGreaterEqual(report.orders_explored, 1)

    def test_minimize_rejects_linearizable_input(self):
        model = QueueModel(capacity=1)
        ops = [deq(1, 0, 1, result=EMPTY)]
        with self.assertRaises(ValueError):
            minimize(model, ops)


class UndecidedSearches(unittest.TestCase):

    def test_zero_budget_stays_undecided(self):
        model = QueueModel(capacity=1)
        ops = [
            enq(1, "A", 0, 1),
            deq(2, 2, 3, result=ok("A")),
        ]
        result = check(model, ops, budget=0)
        self.assertIs(result.verdict, Verdict.UNKNOWN)
        self.assertIsNone(result.linearization)

    def test_undecided_never_produces_counterexample(self):
        model = QueueModel(capacity=1)
        ops = [
            enq(1, "A", 0, 1),
            deq(2, 2, 3, result=ok("B")),  # truly not linearizable
        ]
        with self.assertRaises(ValueError):
            minimize(model, ops, budget=0)


class ReplayAndValidation(unittest.TestCase):

    def test_replay_rejects_fake_witness(self):
        model = QueueModel(capacity=1)
        ops = [
            enq(1, "A", 0, 1),
            deq(2, 2, 3, result=ok("B")),
        ]
        with self.assertRaises(AssertionError):
            replay_witness(model, ops)

    def test_replay_counterexample_enumerates_pending_options(self):
        # Pending enq B may be dropped or placed; both completions still fail
        # to explain deq returning C, so the counterexample is robust.
        model = QueueModel(capacity=1)
        ops = [
            pending_enq(1, "B", invoke=0),
            deq(2, invoke=1, response=2, result=ok("C")),
        ]
        self.assertIs(check(model, ops).verdict, Verdict.NOT_LINEARIZABLE)
        report = replay_counterexample(model, ops)
        # candidates: {#2} alone, and {#1, #2} in both topological orders
        # (the pending enq has no real-time edge, so both orders count)
        self.assertEqual(report.orders_explored, 3)
        self.assertEqual(len(report.failures), 3)

    def test_replay_refutes_invalid_counterexample(self):
        model = QueueModel(capacity=1)
        ops = [
            enq(1, "A", 0, 1),
            deq(2, 2, 3, result=ok("A")),
        ]
        with self.assertRaises(ValueError):
            replay_counterexample(model, ops)

    def test_duplicate_ids_rejected(self):
        model = QueueModel(capacity=1)
        with self.assertRaises(ValueError):
            check(model, [enq(1, "A", 0, 1), enq(1, "B", 2, 3)])


if __name__ == "__main__":
    unittest.main(verbosity=2)
