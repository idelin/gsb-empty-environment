"""Runnable tests for the three-valued online monitor (Python 3.11, stdlib)."""

from __future__ import annotations

import json
import math
import pathlib
import random
import subprocess
import sys
import tempfile
import unittest

from tsmonitor import (
    BackpressureError,
    Event,
    Monitor,
    RuleSpec,
    VerdictValue,
)

ALPHABET = ("A", "B", "C", "D", "X")


def rule_ab(name="R1", window=5.0, consume=True, require="B", forbid=("C",),
            prefix=("A",)):
    return RuleSpec(
        name=name,
        alphabet=ALPHABET,
        prefix=prefix,
        require=require,
        forbid=forbid,
        window=window,
        consume_witness=consume,
    )


def only_obligation(monitor):
    obs = monitor.obligations
    assert len(obs) == 1
    return obs[0]


class PendingTests(unittest.TestCase):
    def test_pending_while_window_open(self):
        """A seen, no B yet, window not closed -> PENDING (not a violation)."""
        monitor = Monitor([rule_ab(window=5.0)])
        monitor.ingest(Event("a1", 1.0, "A"))
        ob = only_obligation(monitor)
        self.assertIs(ob.value, VerdictValue.PENDING)
        self.assertFalse(ob.final)
        self.assertFalse(any(r.value is VerdictValue.FALSE for r in monitor.log))

    def test_multi_symbol_prefix_uses_explicit_transition_table(self):
        rule = rule_ab(prefix=("A", "B"), require="D", forbid=(), window=5.0)
        # The transition structure is explicitly materialised on the rule.
        self.assertEqual(rule.transitions[(0, "A")], 1)
        self.assertEqual(rule.transitions[(0, "B")], 0)
        self.assertEqual(rule.transitions[(1, "B")], 2)
        self.assertEqual(rule.transitions[(1, "A")], 1)  # overlap fallback
        self.assertEqual(rule.transitions[(2, "D")], 0)

        monitor = Monitor([rule])
        monitor.ingest(Event("a1", 1.0, "A"))
        self.assertEqual(monitor.obligations, [])  # prefix incomplete
        monitor.ingest(Event("b1", 2.0, "B"))
        ob = only_obligation(monitor)
        self.assertEqual(tuple(e.id for e in ob.trigger), ("a1", "b1"))
        self.assertIs(ob.value, VerdictValue.PENDING)
        monitor.ingest(Event("d1", 4.0, "D"))
        self.assertIs(ob.value, VerdictValue.TRUE)
        monitor.advance_watermark(10.0)
        self.assertIs(ob.value, VerdictValue.TRUE)
        self.assertTrue(ob.final)


class RevisionTests(unittest.TestCase):
    def test_late_c_revises_tentative_satisfaction(self):
        """A late, out-of-order C retracts a tentatively satisfied verdict."""
        monitor = Monitor([rule_ab(window=5.0)])  # window (1, 6]
        monitor.ingest(Event("a1", 1.0, "A"))
        monitor.ingest(Event("b1", 3.0, "B"))
        ob = only_obligation(monitor)
        self.assertIs(ob.value, VerdictValue.TRUE)
        self.assertFalse(ob.final)  # tentative: window still open

        monitor.ingest(Event("x1", 20.0, "X"))  # stream time advances
        monitor.ingest(Event("c1", 4.0, "C"))   # late, out-of-order C
        self.assertIs(ob.value, VerdictValue.FALSE)

        monitor.retract("c1")  # retraction revises again
        self.assertIs(ob.value, VerdictValue.TRUE)
        self.assertFalse(ob.final)

        monitor.advance_watermark(25.0)  # closes the window -> final
        self.assertIs(ob.value, VerdictValue.TRUE)
        self.assertTrue(ob.final)

        # All old versions are retained.
        values = [r.value for r in ob.history]
        self.assertEqual(
            values,
            [VerdictValue.PENDING, VerdictValue.TRUE, VerdictValue.FALSE,
             VerdictValue.TRUE, VerdictValue.TRUE],
        )
        self.assertEqual([r.final for r in ob.history],
                         [False, False, False, False, True])
        self.assertEqual(ob.history, monitor.log)

    def test_retracting_require_event_reopens_obligation(self):
        monitor = Monitor([rule_ab(window=5.0)])
        monitor.ingest(Event("a1", 1.0, "A"))
        monitor.ingest(Event("b1", 3.0, "B"))
        ob = only_obligation(monitor)
        self.assertIs(ob.value, VerdictValue.TRUE)
        monitor.retract("b1")
        self.assertIs(ob.value, VerdictValue.PENDING)
        monitor.ingest(Event("b1", 3.0, "B"))  # re-assertion is allowed
        self.assertIs(ob.value, VerdictValue.TRUE)

    def test_duplicates_create_no_new_obligations(self):
        monitor = Monitor([rule_ab(window=5.0)])
        self.assertTrue(monitor.ingest(Event("a1", 1.0, "A")))
        self.assertFalse(monitor.ingest(Event("a1", 1.0, "A")))  # duplicate
        self.assertTrue(monitor.ingest(Event("b1", 3.0, "B")))
        self.assertFalse(monitor.ingest(Event("b1", 3.0, "B")))  # duplicate
        ob = only_obligation(monitor)
        self.assertIs(ob.value, VerdictValue.TRUE)
        self.assertEqual(ob.satisfier.id, "b1")
        self.assertEqual(len(monitor.obligations), 1)

    def test_same_b_satisfying_multiple_as_is_per_rule(self):
        """consume_witness defines whether one B may satisfy several As."""
        consume = rule_ab("R_CONSUME", window=10.0, consume=True)
        share = rule_ab("R_SHARE", window=10.0, consume=False)
        monitor = Monitor([consume, share])
        monitor.ingest(Event("a1", 1.0, "A"))
        monitor.ingest(Event("a2", 2.0, "A"))
        monitor.ingest(Event("b1", 3.0, "B"))

        by_rule = {}
        for ob in monitor.obligations:
            by_rule.setdefault(ob.rule.name, {})[ob.trigger[-1].id] = ob

        # Consuming rule: the single B covers only the earliest obligation.
        self.assertIs(by_rule["R_CONSUME"]["a1"].value, VerdictValue.TRUE)
        self.assertIs(by_rule["R_CONSUME"]["a2"].value, VerdictValue.PENDING)
        # Sharing rule: the same B satisfies both obligations.
        self.assertIs(by_rule["R_SHARE"]["a1"].value, VerdictValue.TRUE)
        self.assertIs(by_rule["R_SHARE"]["a2"].value, VerdictValue.TRUE)

        monitor.advance_watermark(20.0)
        self.assertIs(by_rule["R_CONSUME"]["a2"].value, VerdictValue.FALSE)
        self.assertTrue(by_rule["R_CONSUME"]["a2"].final)
        self.assertIs(by_rule["R_SHARE"]["a2"].value, VerdictValue.TRUE)
        self.assertTrue(by_rule["R_SHARE"]["a2"].final)


class ClosingTests(unittest.TestCase):
    def test_watermark_closes_missing_requirement_as_violation(self):
        monitor = Monitor([rule_ab(window=5.0)])
        monitor.ingest(Event("a1", 1.0, "A"))  # window (1, 6]
        monitor.advance_watermark(6.0)
        ob = only_obligation(monitor)
        self.assertIs(ob.value, VerdictValue.FALSE)
        self.assertTrue(ob.final)

        witness = monitor.counterexample()
        self.assertIsNotNone(witness)
        self.assertEqual(witness.kind, "missing-requirement")
        self.assertEqual(witness.completeness, "watermark")
        self.assertTrue(witness.verify())

    def test_terminate_closes_obligations(self):
        monitor = Monitor([rule_ab(window=math.inf)])  # only termination closes
        monitor.ingest(Event("a1", 1.0, "A"))
        monitor.advance_watermark(10**9)  # watermark cannot close inf window
        ob = only_obligation(monitor)
        self.assertIs(ob.value, VerdictValue.PENDING)
        monitor.terminate()
        self.assertIs(ob.value, VerdictValue.FALSE)
        self.assertTrue(ob.final)
        self.assertEqual(monitor.counterexample().completeness, "termination")

    def test_pure_forbid_rule(self):
        rule = rule_ab(require=None, forbid=("C",), window=5.0)
        monitor = Monitor([rule])
        monitor.ingest(Event("a1", 1.0, "A"))
        ob = only_obligation(monitor)
        self.assertIs(ob.value, VerdictValue.PENDING)
        monitor.advance_watermark(6.0)
        self.assertIs(ob.value, VerdictValue.TRUE)
        self.assertTrue(ob.final)

    def test_counterexample_is_shortest(self):
        monitor = Monitor([rule_ab(window=5.0)])
        monitor.ingest(Event("a1", 1.0, "A"))
        monitor.ingest(Event("c1", 3.0, "C"))  # forbidden: witness of 2 events
        monitor.ingest(Event("a2", 10.0, "A"))  # never satisfied: 1-event witness
        monitor.advance_watermark(20.0)
        witness = monitor.counterexample()
        self.assertEqual(witness.kind, "missing-requirement")
        self.assertEqual(tuple(e.id for e in witness.events), ("a2",))
        self.assertTrue(witness.verify())


class WitnessScriptTests(unittest.TestCase):
    def test_witness_script_runs_independently(self):
        monitor = Monitor([rule_ab(window=5.0)])
        monitor.ingest(Event("a1", 1.0, "A"))
        monitor.advance_watermark(6.0)
        witness = monitor.counterexample()
        script = witness.to_script()
        self.assertNotIn("tsmonitor", script)  # self-contained
        with tempfile.TemporaryDirectory() as tmp:
            path = pathlib.Path(tmp) / "counterexample.py"
            path.write_text(script)
            result = subprocess.run(
                [sys.executable, str(path)],
                capture_output=True,
                text=True,
                timeout=30,
            )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("VIOLATION CONFIRMED", result.stdout)


class BackpressureTests(unittest.TestCase):
    def test_budget_abort_is_explicit_not_a_violation(self):
        monitor = Monitor([rule_ab(window=100.0)], lateness=2.0)
        monitor.ingest(Event("a1", 1.0, "A"))
        monitor.ingest(Event("x1", 5.0, "X"))
        monitor.ingest(Event("x2", 10.0, "X"))  # horizon becomes 8.0

        with self.assertRaises(BackpressureError):
            monitor.ingest(Event("b1", 3.0, "B"))  # too late to retain

        ob = only_obligation(monitor)
        # The budget abort did not turn into a violation.
        self.assertIs(ob.value, VerdictValue.PENDING)
        self.assertFalse(any(r.value is VerdictValue.FALSE for r in monitor.log))
        # State stayed bounded by the lateness budget.
        self.assertEqual(monitor.live_count, 1)


class CheckpointTests(unittest.TestCase):
    def test_checkpoint_preserves_pending_and_retraction_index(self):
        monitor = Monitor([rule_ab(window=5.0)])
        monitor.ingest(Event("a1", 1.0, "A"))
        monitor.ingest(Event("b1", 2.0, "B"))
        monitor.ingest(Event("c1", 3.0, "C"))
        monitor.retract("c1")

        state = json.loads(json.dumps(monitor.checkpoint()))
        self.assertEqual(state["retracted"], ["c1"])  # retraction index saved
        pending = [o for o in state["obligations"] if o["value"] == "PENDING"]
        satisfied = [o for o in state["obligations"] if o["value"] == "TRUE"]
        self.assertEqual(len(pending) + len(satisfied), 1)  # obligation saved
        self.assertEqual(len(satisfied), 1)

        restored = Monitor.restore([rule_ab(window=5.0)], state)
        ob = only_obligation(restored)
        self.assertIs(ob.value, VerdictValue.TRUE)
        self.assertFalse(ob.final)
        self.assertGreaterEqual(len(ob.history), 4)  # old versions survive
        restored.advance_watermark(10.0)
        self.assertIs(ob.value, VerdictValue.TRUE)
        self.assertTrue(ob.final)

    def test_batched_replay_with_checkpoints_matches_sorted_reference(self):
        rules = [
            rule_ab("R1", window=10.0, consume=True),          # A -> B<=10, no C
            rule_ab("R2", window=4.0, consume=False, require="D"),  # A -> D<=4
        ]
        events = [
            Event("a1", 1.0, "A"), Event("a2", 2.0, "A"), Event("b1", 3.0, "B"),
            Event("d1", 4.0, "D"), Event("c1", 5.0, "C"), Event("x1", 6.0, "X"),
            Event("a3", 7.0, "A"), Event("d2", 8.0, "D"), Event("b2", 9.0, "B"),
            Event("x2", 10.0, "X"),
        ]

        # Reference: everything fully sorted by event time.
        reference = Monitor(rules)
        for event in sorted(events, key=lambda e: (e.ts, e.id)):
            reference.ingest(event)
        reference.retract("c1")
        reference.advance_watermark(20.0)

        # Batched replay: shuffled arrival order, duplicate, mid-stream
        # retraction, checkpoint -> JSON -> restore between batches.
        arrival = list(events)
        random.Random(7).shuffle(arrival)
        ops = [("ingest", e) for e in arrival]
        c1_pos = next(i for i, (_, e) in enumerate(ops) if e.id == "c1")
        ops.insert(c1_pos + 1, ("retract", "c1"))
        b1_pos = next(i for i, op in enumerate(ops)
                      if op[0] == "ingest" and op[1].id == "b1")
        ops.insert(b1_pos + 1, ("ingest", Event("b1", 3.0, "B")))  # duplicate

        replay = Monitor(rules)
        for start in range(0, len(ops), 3):
            for op, payload in ops[start:start + 3]:
                if op == "ingest":
                    replay.ingest(payload)
                else:
                    replay.retract(payload)
            state = json.loads(json.dumps(replay.checkpoint()))
            replay = Monitor.restore(rules, state)
        replay.advance_watermark(20.0)

        self.assertEqual(replay.verdicts(), reference.verdicts())
        self.assertEqual(
            replay.counterexample().events, reference.counterexample().events
        )
        # Sanity: the reference really contains a final violation (a3/R1).
        finals = reference.verdicts()
        self.assertIn((("R1", ("a3",)), (VerdictValue.FALSE, True)), finals.items())


if __name__ == "__main__":
    unittest.main()
