"""Unit tests for the queue linearizability checker (Python 3.11, stdlib only)."""

import unittest

from linearizability import (
    BoundedQueue,
    _value_closed,
    Op,
    Verdict,
    check_linearizability,
    format_witness,
    history_from_json,
    history_to_json,
    minimize_counterexample,
    replay,
)

OK = ("ok", None)


def enq(oid, value, t0, t1, response=OK):
    return Op(oid, ("enq", value), t0=t0, t1=t1, response=response)


def deq(oid, t0, t1, response):
    return Op(oid, ("deq",), t0=t0, t1=t1, response=response)


class TestBasicHistories(unittest.TestCase):
    def setUp(self):
        self.model = BoundedQueue(capacity=4)

    def test_empty_history_is_linearizable(self):
        result = check_linearizability(self.model, [])
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)
        self.assertEqual(result.witness, [])

    def test_sequential_fifo_ok(self):
        ops = [
            enq(0, "A", 0, 1),
            enq(1, "B", 2, 3),
            deq(2, 4, 5, ("ok", "A")),
            deq(3, 6, 7, ("ok", "B")),
        ]
        result = check_linearizability(self.model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)
        self.assertEqual([op.oid for op, _ in result.witness], [0, 1, 2, 3])

    def test_sequential_fifo_violation(self):
        ops = [
            enq(0, "A", 0, 1),
            enq(1, "B", 2, 3),
            deq(2, 4, 5, ("ok", "B")),  # A was enqueued first
        ]
        result = check_linearizability(self.model, ops)
        self.assertIs(result.verdict, Verdict.NOT_LINEARIZABLE)
        self.assertIsNone(result.witness)

    def test_dequeued_value_must_come_from_legal_enqueue(self):
        ops = [deq(0, 0, 1, ("ok", "ghost"))]
        result = check_linearizability(self.model, ops)
        self.assertIs(result.verdict, Verdict.NOT_LINEARIZABLE)

    def test_value_dequeued_twice_is_rejected(self):
        ops = [
            enq(0, "A", 0, 1),
            deq(1, 2, 3, ("ok", "A")),
            deq(2, 4, 5, ("ok", "A")),
        ]
        result = check_linearizability(self.model, ops)
        self.assertIs(result.verdict, Verdict.NOT_LINEARIZABLE)


class TestRealTimeOrder(unittest.TestCase):
    def setUp(self):
        self.model = BoundedQueue(capacity=4)

    def test_overlapping_ops_may_be_reordered(self):
        # enq A is slow and overlaps the others; enq B (invoked later,
        # responded earlier) may be linearized first, justifying deq -> B.
        ops = [
            enq(0, "A", 0, 5),
            enq(1, "B", 1, 2),
            deq(2, 3, 4, ("ok", "B")),
        ]
        result = check_linearizability(self.model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)
        order = [op.oid for op, _ in result.witness]
        self.assertLess(order.index(1), order.index(0))

    def test_non_overlapping_ops_keep_real_time_order(self):
        # enq B completes strictly before enq A is invoked, so B is ahead
        # of A in every legal linearization and must be dequeued first.
        ops = [
            enq(0, "B", 0, 1),
            enq(1, "A", 2, 3),
            deq(2, 4, 5, ("ok", "A")),
        ]
        result = check_linearizability(self.model, ops)
        self.assertIs(result.verdict, Verdict.NOT_LINEARIZABLE)

    def test_completion_time_sort_is_not_enough(self):
        # Sorting by response time gives enqA, deqA, deqB, enqB, which is
        # impossible (deq B before enq B).  A legal linearization exists
        # because enq B overlaps both dequeues: enqA, enqB, deqA, deqB.
        ops = [
            enq(0, "A", 0, 1),
            enq(1, "B", 2, 8),          # slow enqueue
            deq(2, 3, 4, ("ok", "A")),
            deq(3, 5, 6, ("ok", "B")),
        ]
        by_completion = sorted(ops, key=lambda op: op.t1)
        naive = check_linearizability(
            self.model,
            [Op(op.oid, op.call, i, i, op.response) for i, op in enumerate(by_completion)],
        )
        self.assertIs(naive.verdict, Verdict.NOT_LINEARIZABLE)
        result = check_linearizability(self.model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)


class TestExceptionsAndPending(unittest.TestCase):
    def test_empty_exception_is_legal_on_empty_queue(self):
        model = BoundedQueue(capacity=2)
        ops = [deq(0, 0, 1, ("exc", "Empty"))]
        result = check_linearizability(model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)

    def test_empty_exception_illegal_when_value_available(self):
        model = BoundedQueue(capacity=2)
        ops = [
            enq(0, "A", 0, 1),
            deq(1, 2, 3, ("exc", "Empty")),
        ]
        result = check_linearizability(model, ops)
        self.assertIs(result.verdict, Verdict.NOT_LINEARIZABLE)

    def test_full_exception_is_legal_at_capacity(self):
        model = BoundedQueue(capacity=1)
        ops = [
            enq(0, "A", 0, 1),
            enq(1, "B", 2, 3, response=("exc", "Full")),
        ]
        result = check_linearizability(model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)

    def test_full_exception_illegal_below_capacity(self):
        model = BoundedQueue(capacity=2)
        ops = [
            enq(0, "A", 0, 1),
            enq(1, "B", 2, 3, response=("exc", "Full")),
        ]
        result = check_linearizability(model, ops)
        self.assertIs(result.verdict, Verdict.NOT_LINEARIZABLE)

    def test_pending_enqueue_may_take_effect(self):
        model = BoundedQueue(capacity=2)
        ops = [
            Op(0, ("enq", "X"), t0=0),  # pending: no response recorded
            deq(1, 1, 2, ("ok", "X")),
        ]
        result = check_linearizability(model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)
        pending_steps = [op for op, _ in result.witness if op.pending]
        self.assertEqual([op.oid for op in pending_steps], [0])

    def test_pending_enqueue_may_be_dropped(self):
        model = BoundedQueue(capacity=2)
        ops = [
            Op(0, ("enq", "X"), t0=0),          # pending, never observed
            deq(1, 1, 2, ("exc", "Empty")),     # queue looked empty
        ]
        result = check_linearizability(model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)
        self.assertEqual([op.oid for op, _ in result.witness], [1])

    def test_pending_call_cannot_fabricate_a_completed_response(self):
        model = BoundedQueue(capacity=1)
        ops = [
            enq(0, "A", 0, 1),
            Op(1, ("deq",), t0=2),  # pending dequeue; recorded nothing
            deq(2, 3, 4, ("ok", "A")),
        ]
        # Linearizable: the pending dequeue is simply dropped (or takes
        # effect after op 2).  But op 2's recorded response must hold.
        result = check_linearizability(model, ops)
        self.assertIs(result.verdict, Verdict.LINEARIZABLE)

    def test_malformed_op_rejected(self):
        model = BoundedQueue(capacity=1)
        with self.assertRaises(ValueError):
            check_linearizability(model, [Op(0, ("deq",), t0=0, t1=1)])


class TestBudget(unittest.TestCase):
    def test_budget_exhaustion_keeps_verdict_undecided(self):
        model = BoundedQueue(capacity=8)
        ops = [enq(i, i, 2 * i, 2 * i + 1) for i in range(8)] + [
            deq(8 + i, 16 + 2 * i, 17 + 2 * i, ("ok", i)) for i in range(8)
        ]
        limited = check_linearizability(model, ops, max_states=3)
        self.assertIs(limited.verdict, Verdict.UNKNOWN)
        full = check_linearizability(model, ops)
        self.assertIs(full.verdict, Verdict.LINEARIZABLE)

    def test_zero_budget_on_nonempty_history_is_unknown(self):
        model = BoundedQueue(capacity=1)
        result = check_linearizability(model, [enq(0, "A", 0, 1)], max_states=0)
        self.assertIs(result.verdict, Verdict.UNKNOWN)


class TestCounterexample(unittest.TestCase):
    def setUp(self):
        self.model = BoundedQueue(capacity=4)
        self.ops = [
            enq(0, "A", 0, 1),
            enq(1, "B", 2, 3),
            deq(2, 4, 5, ("ok", "B")),   # FIFO violation
            enq(3, "C", 6, 7),           # noise
            deq(4, 8, 9, ("ok", "A")),   # noise
        ]

    def test_minimal_counterexample_is_one_minimal(self):
        minimal = minimize_counterexample(self.model, self.ops)
        result = check_linearizability(self.model, minimal)
        self.assertIs(result.verdict, Verdict.NOT_LINEARIZABLE)
        self.assertEqual([op.oid for op in minimal], [0, 1, 2])
        for op in minimal:
            rest = [o for o in minimal if o.oid != op.oid]
            rest_result = check_linearizability(self.model, rest)
            # 1-minimal w.r.t. value-closed removals: dropping any op
            # either breaks value-closure or restores linearizability.
            if _value_closed(rest):
                self.assertIs(rest_result.verdict, Verdict.LINEARIZABLE)

    def test_minimize_requires_a_counterexample(self):
        with self.assertRaises(ValueError):
            minimize_counterexample(self.model, [enq(0, "A", 0, 1)])

    def test_unjustified_dequeue_isolates_single_op(self):
        model = BoundedQueue(capacity=2)
        ops = [enq(0, "A", 0, 1), deq(1, 2, 3, ("ok", "Z"))]
        minimal = minimize_counterexample(model, ops)
        self.assertEqual([op.oid for op in minimal], [1])

    def test_counterexample_is_replayable(self):
        minimal = minimize_counterexample(self.model, self.ops)
        blob = history_to_json(self.model, minimal)
        replayed = replay(blob)
        self.assertIs(replayed.verdict, Verdict.NOT_LINEARIZABLE)
        model, ops = history_from_json(blob)
        self.assertEqual(model.capacity, self.model.capacity)
        self.assertEqual(ops, minimal)


class TestSerialization(unittest.TestCase):
    def test_json_round_trip_preserves_pending_and_exceptions(self):
        model = BoundedQueue(capacity=1)
        ops = [
            Op(0, ("enq", "X"), t0=0),
            deq(1, 1, 2, ("ok", "X")),
            deq(2, 3, 4, ("exc", "Empty")),
        ]
        model2, ops2 = history_from_json(history_to_json(model, ops))
        self.assertEqual(model2.capacity, 1)
        self.assertEqual(ops2, ops)
        self.assertTrue(ops2[0].pending)
        self.assertEqual(ops2[2].response, ("exc", "Empty"))

    def test_witness_formatting_mentions_pending_and_exceptions(self):
        model = BoundedQueue(capacity=1)
        ops = [
            Op(0, ("enq", "X"), t0=0),
            deq(1, 1, 2, ("ok", "X")),
            deq(2, 3, 4, ("exc", "Empty")),
        ]
        result = check_linearizability(model, ops)
        text = format_witness(result.witness)
        self.assertIn("pending", text)
        self.assertIn("raise Empty", text)


if __name__ == "__main__":
    unittest.main()
