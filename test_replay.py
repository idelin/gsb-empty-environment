"""Tests for the revisable token-bucket replay engine.

Every edit is checked against the independent full-replay oracle by
comparing the *complete* decision state (decision, reason, bucket
before/after) before and after the edit -- never just the final bucket
level.  Identity stability is checked via versions: any decision outside
the reported changed range must be byte-identical, version included.
"""

import random
import unittest
from fractions import Fraction

from oracle import full_replay
from token_bucket import (
    REASON_ALLOWED,
    REASON_REJECTED,
    ReplayEngine,
)


def snapshot(engine):
    """Full state: request_id -> complete decision tuple incl. version."""
    return {
        d.request_id: (
            d.allowed, d.reason, d.bucket_before, d.bucket_after, d.version,
        )
        for d in engine.decisions
    }


def assert_matches_oracle(testcase, engine, segments, requests):
    outcomes = full_replay(segments, requests)
    decisions = engine.decisions
    testcase.assertEqual(len(decisions), len(outcomes))
    for d, o in zip(decisions, outcomes):
        testcase.assertEqual(d.request_id, o.request_id)
        testcase.assertEqual(d.allowed, o.allowed)
        testcase.assertEqual(d.reason, o.reason)
        testcase.assertEqual(d.bucket_before, o.bucket_before)
        testcase.assertEqual(d.bucket_after, o.bucket_after)


def assert_stable_outside_change(testcase, before, engine, report):
    """Every pre-existing decision outside the change set keeps identity."""
    changed = set(report.changed_request_ids)
    for d in engine.decisions:
        if d.request_id in before and d.request_id not in changed:
            testcase.assertEqual(
                before[d.request_id],
                (d.allowed, d.reason, d.bucket_before, d.bucket_after, d.version),
                f"{d.request_id} changed despite being outside the changed range",
            )


class BasicReplayTest(unittest.TestCase):
    def test_capacity_two_three_requests_at_t0(self):
        engine = ReplayEngine(
            segments=[(0, 2, 1)],
            requests=[(0, 1, "a"), (0, 2, "b"), (0, 3, "c")],
        )
        a, b, c = engine.decisions
        self.assertTrue(a.allowed)
        self.assertEqual((a.bucket_before, a.bucket_after), (Fraction(2), Fraction(1)))
        self.assertTrue(b.allowed)
        self.assertEqual((b.bucket_before, b.bucket_after), (Fraction(1), Fraction(0)))
        self.assertFalse(c.allowed)
        self.assertEqual(c.reason, REASON_REJECTED)
        self.assertEqual((c.bucket_before, c.bucket_after), (Fraction(0), Fraction(0)))
        self.assertEqual(a.reason, REASON_ALLOWED)
        self.assertTrue(all(d.version == 1 for d in engine.decisions))

    def test_fractional_refill_is_exact(self):
        engine = ReplayEngine(
            segments=[(0, 1, Fraction(1, 3))],
            requests=[(t, 1, f"r{t}") for t in range(5)],
        )
        expected = [
            (True, Fraction(1), Fraction(0)),
            (False, Fraction(1, 3), Fraction(1, 3)),
            (False, Fraction(2, 3), Fraction(2, 3)),
            (True, Fraction(1), Fraction(0)),
            (False, Fraction(1, 3), Fraction(1, 3)),
        ]
        for d, (allowed, before, after) in zip(engine.decisions, expected):
            self.assertEqual(d.allowed, allowed)
            self.assertEqual(d.bucket_before, before)
            self.assertEqual(d.bucket_after, after)

    def test_no_float_drift_over_many_steps(self):
        # 0.1 tokens/ms is not binary-representable; a float implementation
        # accumulates ~1e-16 per step and rejects the t=10k requests.
        n = 1001
        engine = ReplayEngine(
            segments=[(0, 1, Fraction(1, 10))],
            requests=[(t, 1, f"r{t}") for t in range(n)],
        )
        for t, d in enumerate(engine.decisions):
            if t % 10 == 0:
                self.assertTrue(d.allowed, f"t={t}")
                self.assertEqual(d.bucket_before, Fraction(1))
            else:
                self.assertFalse(d.allowed, f"t={t}")
                self.assertEqual(d.bucket_before, Fraction(t % 10, 10))

    def test_long_idle_caps_at_capacity(self):
        engine = ReplayEngine(
            segments=[(0, 2, 1)],
            requests=[(0, 1, "a"), (0, 2, "b"), (1000, 1, "c"),
                      (1000, 2, "d"), (1000, 3, "e")],
        )
        c, d, e = engine.decisions[2:]
        self.assertEqual(c.bucket_before, Fraction(2))  # capped, not 1000
        self.assertTrue(c.allowed)
        self.assertTrue(d.allowed)
        self.assertFalse(e.allowed)

    def test_capacity_shrink_clamps_at_boundary(self):
        engine = ReplayEngine(
            segments=[(0, 4, 1), (10, 1, 1)],
            requests=[(0, 1, "a"), (10, 1, "b"), (11, 1, "c")],
        )
        a, b, c = engine.decisions
        self.assertEqual(a.bucket_after, Fraction(3))
        # Idle gap would refill to 4, but the t=10 segment clamps to 1.
        self.assertEqual(b.bucket_before, Fraction(1))
        self.assertTrue(b.allowed)
        self.assertEqual(c.bucket_before, Fraction(1))

    def test_requests_exactly_on_config_boundary(self):
        # Shrink exactly at t=10: clamp applies before same-time requests.
        engine = ReplayEngine(
            segments=[(0, 3, 0), (10, 1, 0)],
            requests=[(0, 1, "a"), (10, 1, "x"), (10, 2, "y")],
        )
        a, x, y = engine.decisions
        self.assertEqual(a.bucket_after, Fraction(2))
        self.assertEqual(x.bucket_before, Fraction(1))  # clamped from 2
        self.assertTrue(x.allowed)
        self.assertFalse(y.allowed)

        # Rate change at t=10: same-time requests see the pre-boundary
        # level (a single instant refills nothing); the new rate applies
        # strictly after the boundary.
        engine = ReplayEngine(
            segments=[(0, 2, 0), (10, 2, 1)],
            requests=[(0, 1, "a"), (10, 1, "x"), (10, 2, "y"), (11, 1, "w")],
        )
        _, x, y, w = engine.decisions
        self.assertEqual(x.bucket_before, Fraction(1))
        self.assertTrue(x.allowed)
        self.assertFalse(y.allowed)
        self.assertEqual(w.bucket_before, Fraction(1))  # 1ms at new rate
        self.assertTrue(w.allowed)


class RevisionTest(unittest.TestCase):
    SEGMENTS = [(0, 2, 1)]
    REQUESTS = [(0, 1, "a"), (0, 2, "b"), (1, 1, "c"), (2, 1, "d"), (3, 1, "e")]

    def make_engine(self):
        return ReplayEngine(self.SEGMENTS, self.REQUESTS)

    def test_backfill_recomputes_only_affected_prefix(self):
        engine = self.make_engine()
        before = snapshot(engine)
        assert_matches_oracle(self, engine, self.SEGMENTS, self.REQUESTS)

        report = engine.insert_request(0, 0, "x")

        # x consumes a token -> a, b shift; c reconverges (refill of exactly
        # 1 token over the 1ms gap) and stops the scan; d, e never touched.
        self.assertEqual(report.recomputed_range, (0, 3))
        self.assertEqual(report.changed_range, (0, 2))
        self.assertEqual(report.changed_request_ids, ("x", "a", "b"))
        decisions = {d.request_id: d for d in engine.decisions}
        self.assertEqual(decisions["b"].allowed, False)
        self.assertEqual(decisions["x"].version, 2)
        self.assertEqual(decisions["a"].version, 2)
        self.assertEqual(decisions["b"].version, 2)
        self.assertEqual(decisions["c"].version, 1)  # stable identity kept
        self.assertEqual(decisions["d"].version, 1)
        self.assertEqual(decisions["e"].version, 1)
        assert_stable_outside_change(self, before, engine, report)
        assert_matches_oracle(
            self, engine, self.SEGMENTS, [(0, 0, "x")] + self.REQUESTS
        )

    def test_backfill_at_end_touches_nothing_else(self):
        engine = self.make_engine()
        before = snapshot(engine)
        report = engine.insert_request(4, 1, "z")
        self.assertEqual(report.recomputed_range, (5, 5))
        self.assertEqual(report.changed_range, (5, 5))
        assert_stable_outside_change(self, before, engine, report)
        assert_matches_oracle(
            self, engine, self.SEGMENTS, self.REQUESTS + [(4, 1, "z")]
        )

    def test_config_change_open_ended_segment(self):
        engine = self.make_engine()
        before = snapshot(engine)
        new_segments = [(0, 2, 1), (2, 2, 2)]
        report = engine.update_config(new_segments)

        # d@2 recomputes identically (its refill predates the new segment)
        # but the edit is open-ended, so no early stop: e is recomputed too
        # and flips.  The changed range sits strictly inside the recomputed
        # range.
        self.assertEqual(report.recomputed_range, (3, 4))
        self.assertEqual(report.changed_range, (4, 4))
        self.assertEqual(report.changed_request_ids, ("e",))
        decisions = {d.request_id: d for d in engine.decisions}
        self.assertEqual(decisions["d"].version, 1)
        self.assertEqual(decisions["e"].version, 2)
        self.assertEqual(decisions["e"].bucket_before, Fraction(2))
        assert_stable_outside_change(self, before, engine, report)
        assert_matches_oracle(self, engine, new_segments, self.REQUESTS)

    def test_bounded_config_change_early_stop_no_changes(self):
        segments = [(0, 2, 1), (5, 2, 5), (10, 2, 1)]
        requests = [(t, 1, f"r{t}") for t in range(21)]
        engine = ReplayEngine(segments, requests)
        before = snapshot(engine)

        # Only the middle segment changes; the capacity ceiling hides the
        # difference entirely, so nothing changes -- but the engine must
        # recompute up to t=10 before it may stop.
        new_segments = [(0, 2, 1), (5, 2, 10), (10, 2, 1)]
        report = engine.update_config(new_segments)
        self.assertEqual(report.recomputed_range, (5, 10))
        self.assertIsNone(report.changed_range)
        self.assertEqual(report.changed_request_ids, ())
        self.assertEqual(snapshot(engine), before)  # versions untouched
        assert_matches_oracle(self, engine, new_segments, requests)

    def test_noop_config_update(self):
        engine = self.make_engine()
        report = engine.update_config([(0, 2, 1)])
        self.assertIsNone(report.recomputed_range)
        self.assertIsNone(report.changed_range)
        self.assertEqual(engine.version, 1)

    def test_retroactive_config_change_from_history(self):
        # Rate was actually 0 before t=2, not 1: decisions at t=1..2 flip.
        segments = [(0, 2, 1)]
        requests = [(0, 1, "a"), (0, 2, "b"), (1, 1, "c"), (2, 1, "d"), (3, 1, "e")]
        engine = ReplayEngine(segments, requests)
        before = snapshot(engine)
        new_segments = [(0, 2, 0), (2, 2, 1)]
        report = engine.update_config(new_segments)
        decisions = {d.request_id: d for d in engine.decisions}
        self.assertFalse(decisions["c"].allowed)  # no refill between t=0..2
        self.assertFalse(decisions["d"].allowed)
        self.assertTrue(decisions["e"].allowed)  # 1 token refilled by t=3
        assert_stable_outside_change(self, before, engine, report)
        assert_matches_oracle(self, engine, new_segments, requests)


class ValidationTest(unittest.TestCase):
    def test_duplicate_time_seq_rejected(self):
        engine = ReplayEngine([(0, 2, 1)], [(0, 1, "a")])
        with self.assertRaises(ValueError):
            engine.insert_request(0, 1, "b")

    def test_duplicate_request_id_rejected(self):
        engine = ReplayEngine([(0, 2, 1)], [(0, 1, "a")])
        with self.assertRaises(ValueError):
            engine.insert_request(1, 1, "a")

    def test_float_inputs_rejected(self):
        with self.assertRaises(TypeError):
            ReplayEngine([(0.0, 2, 1)])
        with self.assertRaises(TypeError):
            ReplayEngine([(0, 2, 0.1)])
        engine = ReplayEngine([(0, 2, 1)])
        with self.assertRaises(TypeError):
            engine.insert_request(0.5, 1, "x")

    def test_empty_config_rejected(self):
        with self.assertRaises(ValueError):
            ReplayEngine([])

    def test_request_before_config_start_rejected(self):
        with self.assertRaises(ValueError):
            ReplayEngine([(10, 2, 1)], [(5, 1, "a")])
        engine = ReplayEngine([(10, 2, 1)])
        with self.assertRaises(ValueError):
            engine.insert_request(5, 1, "a")


class RandomizedOracleTest(unittest.TestCase):
    """Cross-check the incremental engine against the oracle after a
    sequence of random backfills and retroactive config edits."""

    def test_random_edits_match_oracle(self):
        rng = random.Random(20260921)
        rates = [Fraction(0), Fraction(1, 3), Fraction(1, 2),
                 Fraction(1), Fraction(3, 2), Fraction(2)]

        def random_segments():
            starts = [0] + sorted(rng.sample(range(1, 60), rng.randint(0, 3)))
            return [(s, rng.randint(1, 4), rng.choice(rates)) for s in starts]

        segments = random_segments()
        times = sorted(rng.sample(range(0, 60), 30))
        requests = [(t, 1, f"r{i}") for i, t in enumerate(times)]
        engine = ReplayEngine(segments, requests)
        assert_matches_oracle(self, engine, segments, requests)

        spare_times = iter(sorted(rng.sample(range(60, 120), 8)))
        for round_no in range(8):
            before = snapshot(engine)
            if round_no % 2 == 0:
                t = next(spare_times)
                # Backfill into the historical middle of the log.
                t = rng.randint(0, 59)
                taken = {(e.time, e.seq) for e in engine.events}
                seq = 1
                while (Fraction(t), seq) in taken:
                    seq += 1
                report = engine.insert_request(t, seq, f"x{round_no}")
                requests = requests + [(t, seq, f"x{round_no}")]
            else:
                segments = random_segments()
                report = engine.update_config(segments)
            assert_stable_outside_change(self, before, engine, report)
            assert_matches_oracle(self, engine, segments, requests)


if __name__ == "__main__":
    unittest.main()
