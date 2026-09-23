import json
import unittest

from monitor import (
    BackpressureError,
    Event,
    Monitor,
    RuleTrie,
    Verdict,
    replay_witness,
)


def build_trie(consume=False):
    """Two rules sharing the prefix A: 'A then B within 10, no C' and 'A then D within 20'."""
    trie = RuleTrie(alphabet={"A", "B", "C", "D"})
    trie.add_rule("ab", ["A", "B"], bound=10, forbid={"C"}, consume=consume)
    trie.add_rule("ad", ["A", "D"], bound=20)
    return trie


def make_monitor(consume=False, lateness=100.0, max_events=1000):
    return Monitor(build_trie(consume), lateness=lateness, max_events=max_events)


class PendingTest(unittest.TestCase):
    def test_a_without_b_while_window_open_is_pending(self):
        mon = make_monitor()
        mon.ingest(Event("a1", "A", 1.0))
        mon.advance_watermark(5.0)  # deadline of ab:a1 is 11, not yet closed
        ob = mon.obligation("ab:a1")
        self.assertIs(ob.verdict, Verdict.PENDING)
        self.assertFalse(ob.final)
        self.assertIs(mon.obligation("ad:a1").verdict, Verdict.PENDING)
        # watermark past the ab deadline closes it into a violation
        mon.advance_watermark(11.5)
        self.assertIs(mon.obligation("ab:a1").verdict, Verdict.VIOLATED)
        # ad deadline is 21, still open
        self.assertIs(mon.obligation("ad:a1").verdict, Verdict.PENDING)
        # termination closes every remaining future obligation
        mon.close()
        self.assertIs(mon.obligation("ad:a1").verdict, Verdict.VIOLATED)
        self.assertTrue(all(ob.final for ob in mon.obligations.values()))

    def test_no_closure_without_watermark_or_termination(self):
        mon = make_monitor()
        mon.ingest(Event("a1", "A", 1.0))
        for i in range(5):  # late out-of-window traffic must not close the obligation
            mon.ingest(Event(f"x{i}", "B", 50.0 + i))
        # B at t>=50 is past the deadline (11): without a watermark the
        # obligation stays pending, it is neither satisfied nor violated
        self.assertIs(mon.obligation("ab:a1").verdict, Verdict.PENDING)
        self.assertFalse(mon.obligation("ab:a1").final)
        mon.advance_watermark(11.5)
        self.assertIs(mon.obligation("ab:a1").verdict, Verdict.VIOLATED)


class RevisionTest(unittest.TestCase):
    def test_late_c_revokes_tentative_satisfaction_and_retraction_restores(self):
        mon = make_monitor()
        mon.ingest(Event("a1", "A", 1.0))
        mon.ingest(Event("b1", "B", 5.0))
        ob = mon.obligation("ab:a1")
        self.assertIs(ob.verdict, Verdict.SATISFIED)
        self.assertFalse(ob.final)  # tentative: window still open
        # late out-of-order C inside the window, before the B
        mon.ingest(Event("c1", "C", 3.0))
        self.assertIs(ob.verdict, Verdict.VIOLATED)
        self.assertEqual(ob.version, 3)  # PENDING -> SATISFIED -> VIOLATED
        # retracting C revises the verdict back
        self.assertTrue(mon.retract("c1"))
        self.assertIs(ob.verdict, Verdict.SATISFIED)
        self.assertEqual(ob.version, 4)
        # all old versions retained, both per obligation and in the global log
        self.assertEqual(
            [r.verdict for r in ob.history],
            [Verdict.PENDING, Verdict.SATISFIED, Verdict.VIOLATED, Verdict.SATISFIED],
        )
        self.assertEqual([r.version for r in ob.history], [1, 2, 3, 4])
        logged = [r for r in mon.verdict_log if r.obligation_id == "ab:a1"]
        self.assertEqual(len(logged), 4)

    def test_duplicate_events_open_no_new_obligations(self):
        mon = make_monitor()
        self.assertEqual(len(mon.ingest(Event("a1", "A", 1.0))), 2)  # ab + ad pending
        self.assertEqual(mon.ingest(Event("a1", "A", 1.0)), [])  # duplicate ignored
        mon.ingest(Event("b1", "B", 5.0))
        mon.ingest(Event("b1", "B", 5.0))  # duplicate ignored
        mon.close()
        self.assertEqual(set(mon.obligations), {"ab:a1", "ad:a1"})
        self.assertIs(mon.obligation("ab:a1").verdict, Verdict.SATISFIED)

    def test_same_b_satisfies_many_a_when_rule_allows_sharing(self):
        mon = make_monitor(consume=False)
        mon.ingest(Event("a1", "A", 1.0))
        mon.ingest(Event("a2", "A", 2.0))
        mon.ingest(Event("b1", "B", 3.0))
        self.assertIs(mon.obligation("ab:a1").verdict, Verdict.SATISFIED)
        self.assertIs(mon.obligation("ab:a2").verdict, Verdict.SATISFIED)

    def test_same_b_consumed_by_oldest_a_when_rule_requires(self):
        mon = make_monitor(consume=True)
        mon.ingest(Event("a1", "A", 1.0))
        mon.ingest(Event("a2", "A", 2.0))
        mon.ingest(Event("b1", "B", 3.0))
        self.assertIs(mon.obligation("ab:a1").verdict, Verdict.SATISFIED)
        self.assertIs(mon.obligation("ab:a2").verdict, Verdict.PENDING)
        mon.close()
        self.assertIs(mon.obligation("ab:a2").verdict, Verdict.VIOLATED)


class WitnessTest(unittest.TestCase):
    def test_shortest_forbidden_witness_is_independently_executable(self):
        mon = make_monitor()
        mon.ingest(Event("a1", "A", 1.0))
        mon.ingest(Event("b1", "B", 9.0))
        mon.ingest(Event("c1", "C", 4.0))  # late C revokes the tentative satisfaction
        record = mon.verdict_log[-1]
        self.assertIs(record.verdict, Verdict.VIOLATED)
        witness = record.witness
        self.assertEqual(witness.kind, "forbidden")
        # shortest evidence: trigger + the forbidden event, nothing else
        self.assertEqual([e.eid for e in witness.events], ["a1", "c1"])
        fresh_rule = build_trie().rules["ab"]  # independent of any monitor state
        self.assertTrue(replay_witness(fresh_rule, witness))
        # a tampered witness must not verify
        good = witness.events[1]
        bad = witness.__class__(
            witness.rule, witness.kind, (witness.events[0], Event("x", "B", good.ts)),
            witness.deadline, witness.closed,
        )
        self.assertFalse(replay_witness(fresh_rule, bad))

    def test_deadline_witness_is_independently_executable(self):
        mon = make_monitor()
        mon.ingest(Event("a1", "A", 1.0))
        mon.close()
        record = [r for r in mon.verdict_log if r.obligation_id == "ab:a1"][-1]
        witness = record.witness
        self.assertEqual(witness.kind, "deadline")
        self.assertEqual([e.eid for e in witness.events], ["a1"])
        self.assertTrue(replay_witness(build_trie().rules["ab"], witness))


class CheckpointTest(unittest.TestCase):
    def test_batched_replay_with_checkpoints_matches_sorted_reference(self):
        batches = [
            [Event("a1", "A", 1.0), Event("b1", "B", 6.0), Event("a2", "A", 2.0)],
            [Event("c1", "C", 4.0), Event("d1", "D", 15.0), Event("b2", "B", 8.0)],
            [Event("a3", "A", 25.0)],
        ]
        watermarks = [None, 10.0, 30.0]

        # batched run: checkpoint (as JSON) and restore between batches
        mon = make_monitor()
        snap = None
        for batch, wm in zip(batches, watermarks):
            if snap is not None:
                mon = Monitor.restore(build_trie(), json.loads(json.dumps(snap)))
            for event in batch:
                mon.ingest(event)
            if wm is not None:
                mon.advance_watermark(wm)
            snap = mon.snapshot()
        # checkpoint must carry pending obligations and the retraction index
        pending = [o for o in snap["obligations"] if o["verdict"] == "PENDING"]
        self.assertTrue(pending, "checkpoint must contain pending obligations")
        self.assertIn("a1", snap["retraction_index"])
        self.assertEqual(set(snap["retraction_index"]["a1"]), {"ab", "ad"})
        # a retraction after the last checkpoint, then terminate
        mon = Monitor.restore(build_trie(), json.loads(json.dumps(snap)))
        self.assertTrue(mon.retract("c1"))
        mon.close()

        # reference: all live events fully sorted, single pass
        live = [e for batch in batches for e in batch if e.eid != "c1"]
        ref = make_monitor()
        for event in sorted(live, key=lambda e: (e.ts, e.eid)):
            ref.ingest(event)
        ref.close()

        self.assertEqual(mon.verdicts(), ref.verdicts())
        self.assertEqual(
            {oid: ob.verdict for oid, ob in mon.obligations.items()},
            {
                "ab:a1": Verdict.SATISFIED,
                "ab:a2": Verdict.SATISFIED,
                "ab:a3": Verdict.VIOLATED,
                "ad:a1": Verdict.SATISFIED,
                "ad:a2": Verdict.SATISFIED,
                "ad:a3": Verdict.VIOLATED,
            },
        )


class BackpressureTest(unittest.TestCase):
    def test_budget_abort_is_explicit_and_not_a_violation(self):
        mon = make_monitor(max_events=2)
        mon.ingest(Event("a1", "A", 1.0))
        mon.ingest(Event("b1", "B", 2.0))
        with self.assertRaises(BackpressureError):
            mon.ingest(Event("d1", "D", 3.0))
        # the abort published no violation; existing verdicts are untouched
        self.assertFalse(any(r.verdict is Verdict.VIOLATED for r in mon.verdict_log))
        self.assertIs(mon.obligation("ab:a1").verdict, Verdict.SATISFIED)
        self.assertIs(mon.obligation("ad:a1").verdict, Verdict.PENDING)
        # the monitor keeps working: free budget via retraction, then retry
        self.assertTrue(mon.retract("b1"))
        mon.ingest(Event("d1", "D", 3.0))
        self.assertIs(mon.obligation("ad:a1").verdict, Verdict.SATISFIED)

    def test_too_late_event_is_backpressure_not_violation(self):
        mon = make_monitor(lateness=5.0)
        mon.ingest(Event("a1", "A", 1.0))
        mon.ingest(Event("b1", "B", 3.0))
        mon.advance_watermark(100.0)  # seals ab:a1 as a final satisfaction
        ob = mon.obligation("ab:a1")
        self.assertIs(ob.verdict, Verdict.SATISFIED)
        self.assertTrue(ob.final)
        log_size = len(mon.verdict_log)
        with self.assertRaises(BackpressureError):
            mon.ingest(Event("c1", "C", 2.0))  # beyond the lateness window
        # the rejected event published nothing and the sealed verdict is unchanged
        self.assertEqual(len(mon.verdict_log), log_size)
        self.assertIs(ob.verdict, Verdict.SATISFIED)
        self.assertFalse(
            any(r.obligation_id == "ab:a1" and r.verdict is Verdict.VIOLATED
                for r in mon.verdict_log))


class ExplicitStructureTest(unittest.TestCase):
    def test_alphabet_and_transitions_are_explicit(self):
        trie = build_trie()
        table = trie.transitions()
        self.assertEqual(table[((), "A")], ("A",))
        self.assertEqual(table[(("A",), "B")], ("A", "B"))
        self.assertEqual(table[(("A",), "D")], ("A", "D"))
        # both rules share the prefix node ("A",)
        self.assertNotIn((("A", "B"), "D"), table)
        with self.assertRaises(ValueError):
            trie.add_rule("bad", ["A", "Z"], bound=5)
        mon = Monitor(trie, lateness=1.0, max_events=10)
        with self.assertRaises(ValueError):
            mon.ingest(Event("z1", "Z", 0.0))


if __name__ == "__main__":
    unittest.main(verbosity=2)
