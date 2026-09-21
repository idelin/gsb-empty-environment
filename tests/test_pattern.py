import os
import random
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))

from streamcep.matcher import (  # noqa: E402
    Collector, Event, Match, SideReport, StreamMatcher, Watermark, Withdraw,
    feed_auto_watermarks,
    KIND_EVENT_AFTER_WITHDRAW,
)
from streamcep.reference import reference_matches  # noqa: E402


class Recorder(Collector):
    def __init__(self):
        self.proposed = []
        self.revoked = []
        self.final = []
        self.reports = []

    def on_match_proposed(self, match, late=False):
        self.proposed.append((match.mid, late))

    def on_match_revoked(self, match, cause, late=False, blocker=None):
        self.revoked.append((match.mid, cause, late))

    def on_match_final(self, match):
        self.final.append(match.mid)

    def on_side_report(self, report):
        self.reports.append(report)


def run(records):
    rec = Recorder()
    m = StreamMatcher(rec)
    m.push_many(records)
    return m, rec


def ev(t, etype, seq, key="k", src="s"):
    return Event(etype, t, key, src, seq)


def mid(a, b, key="k"):
    return (key, ("s", a), ("s", b))


class PatternTests(unittest.TestCase):

    def test_headline_two_matches_then_both_revoked(self):
        # A@0 A@1 B@3 => 2 tentative matches; C@2 arrives before closure
        # and revokes both; nothing finalized.
        _, rec = run([ev(0, "A", 1), ev(1, "A", 2), ev(3, "B", 3),
                      Watermark(2), ev(2, "C", 9), Watermark(10)])
        self.assertEqual(sorted(p[0] for p in rec.proposed),
                         sorted([mid(1, 3), mid(2, 3)]))
        self.assertEqual({r[0] for r in rec.revoked},
                         {mid(1, 3), mid(2, 3)})
        self.assertTrue(all(r[1] == "c_between" for r in rec.revoked))
        self.assertEqual(rec.final, [])

    def test_b6_matches_only_a1(self):
        _, rec = run([ev(0, "A", 1), ev(1, "A", 2), ev(6, "B", 3),
                      Watermark(10)])
        self.assertEqual(rec.final, [mid(2, 3)])

    def test_window_endpoints_inclusive(self):
        # d in [0,5] inclusive: A@0-B@5 yes, A@0-B@6 no.
        _, rec = run([ev(0, "A", 1), ev(5, "B", 2), ev(6, "B", 3),
                      Watermark(10)])
        self.assertEqual(rec.final, [mid(1, 2)])
        _, rec2 = run([ev(0, "A", 1), ev(0, "B", 2), Watermark(10)])
        self.assertEqual(rec2.final, [mid(1, 2)])  # d == 0

    def test_overlapping_patterns(self):
        # One B matches every live, non-expired A.
        _, rec = run([ev(0, "A", 1), ev(1, "A", 2), ev(2, "A", 3),
                      ev(4, "B", 9), Watermark(10)])
        self.assertEqual(sorted(rec.final),
                         sorted([mid(1, 9), mid(2, 9), mid(3, 9)]))

    def test_c_blocks_between_pairs_only(self):
        # A@0 A@1 C@2 B@3: C is after both As -> both blocked.
        _, rec = run([ev(0, "A", 1), ev(1, "A", 2), ev(2, "C", 7),
                      ev(3, "B", 3), Watermark(10)])
        self.assertEqual(rec.final, [])
        # A@0 C@1 A@2 B@3: C only between A@0 and B.
        _, rec2 = run([ev(0, "A", 1), ev(1, "C", 7), ev(2, "A", 2),
                       ev(3, "B", 3), Watermark(10)])
        self.assertEqual(rec2.final, [mid(2, 3)])

    def test_same_timestamp_ordering_a_c_b(self):
        # Same timestamp tie-break A < C < B; strict "between".
        _, rec = run([ev(7, "A", 1), ev(7, "C", 2), ev(7, "B", 3),
                      Watermark(20)])
        self.assertEqual(rec.final, [])
        # A and B same time, no C: matched.
        _, rec2 = run([ev(7, "A", 1), ev(7, "B", 3), Watermark(20)])
        self.assertEqual(rec2.final, [mid(1, 3)])
        # Same-time C before a different, earlier A is not "between" it.
        _, rec3 = run([ev(6, "A", 1), ev(7, "C", 2), ev(7, "B", 3),
                       Watermark(20)])
        self.assertEqual(rec3.final, [])  # C@7 strictly between A@6 and B@7

    def test_watermark_finalization_and_late_handling(self):
        # Late C while match still OPEN: incorporated and revokes.
        _, rec = run([ev(0, "A", 1), ev(1, "A", 2), ev(3, "B", 3),
                      Watermark(3), ev(2, "C", 8), Watermark(10)])
        self.assertEqual(rec.final, [])
        kinds = {r.kind for r in rec.reports}
        self.assertIn("late_incorporated", kinds)

        # Late C AFTER B sealed: finals unchanged; separate report.
        _, rec2 = run([ev(0, "A", 1), ev(3, "B", 2), Watermark(4),
                       ev(2, "C", 9), Watermark(10)])
        self.assertEqual(rec2.final, [mid(1, 2)])

        # Truly out-of-scope late C: out_of_scope_late, finals unchanged.
        _, rec3 = run([ev(0, "A", 1), ev(3, "B", 2), Watermark(10),
                       ev(1, "C", 9), Watermark(20)])
        self.assertEqual(rec3.final, [mid(1, 2)])
        self.assertTrue(any(r.kind == "out_of_scope_late"
                            for r in rec3.reports))

    def test_late_b_never_retroactive(self):
        _, rec = run([ev(0, "A", 1), Watermark(4), ev(3, "B", 2),
                      Watermark(10)])
        self.assertEqual(rec.final, [])
        self.assertEqual(rec.reports[0].kind, "out_of_scope_late")

    def test_dedup_by_source_seq(self):
        _, rec = run([ev(0, "A", 1), ev(0, "A", 1), ev(0, "A", 1),
                      ev(3, "B", 2), ev(3, "B", 2), Watermark(10)])
        self.assertEqual(rec.final, [mid(1, 2)])
        self.assertEqual(sum(r.kind == "duplicate" for r in rec.reports), 3)

    def test_withdraw_a_before_closure(self):
        _, rec = run([ev(0, "A", 1), ev(1, "A", 2), ev(3, "B", 3),
                      Withdraw("s", 2), Watermark(10)])
        self.assertEqual(rec.final, [mid(1, 3)])
        self.assertEqual({r[0] for r in rec.revoked}, {mid(2, 3)})

    def test_withdraw_c_reopens_match(self):
        # C blocks, then the C itself is withdrawn: match is proposed again.
        _, rec = run([ev(0, "A", 1), ev(3, "B", 3), ev(2, "C", 7),
                      Withdraw("s", 7), Watermark(10)])
        self.assertEqual(rec.final, [mid(1, 3)])
        causes = [r[1] for r in rec.revoked]
        self.assertEqual(causes, ["c_between"])  # revoked by C, re-proposed

    def test_withdraw_after_finalization_reports_only(self):
        _, rec = run([ev(0, "A", 1), ev(3, "B", 2), Watermark(10),
                      Withdraw("s", 1), Watermark(20)])
        self.assertEqual(rec.final, [mid(1, 2)])
        self.assertEqual(rec.reports[0].kind, "withdraw_unknown_or_closed")

    def test_event_after_withdraw_ignored(self):
        _, rec = run([Withdraw("s", 1), ev(0, "A", 1), ev(3, "B", 2),
                      Watermark(10)])
        self.assertEqual(rec.final, [])
        self.assertIn("event_after_withdraw",
                      {r.kind for r in rec.reports})

    def test_duplicate_withdraw_ignored(self):
        _, rec = run([ev(0, "A", 1), Withdraw("s", 1), Withdraw("s", 1),
                      ev(3, "B", 2), Watermark(10)])
        self.assertEqual(rec.final, [])
        self.assertEqual(sum(r.kind == "duplicate" for r in rec.reports), 1)

    def test_long_gap_without_b_cleans_state(self):
        m = StreamMatcher(Recorder())
        m.push_many([ev(0, "A", 1), ev(1, "A", 2)])
        m.advance(3)
        self.assertGreater(m.stats()["live_events"], 0)
        m.advance(7)  # 0+5 < 7 and 1+5 < 7 -> both As pruned
        self.assertEqual(m.stats()["live_events"], 0)
        self.assertEqual(m.stats()["tracked_open_matches"], 0)

    def test_pending_released_boundedly(self):
        rec = Recorder()
        m = StreamMatcher(rec)
        m.push(ev(0, "A", 1))
        m.push(ev(3, "B", 2))
        self.assertEqual(m.stats()["pending_proposed"], 1)
        m.advance(3)  # W == b does NOT close (seal at W > b)
        self.assertEqual(m.stats()["pending_proposed"], 1)
        m.advance(4)  # now sealed
        self.assertEqual(m.stats()["pending_proposed"], 0)
        self.assertEqual(rec.final, [mid(1, 2)])

    def test_late_a_proposes_against_open_b_only(self):
        # B@3 live, W=3 (B not sealed: closure needs W > b). Late A@1 arrives.
        _, rec = run([ev(3, "B", 2), Watermark(3), ev(1, "A", 1),
                      Watermark(10)])
        self.assertEqual(rec.final, [mid(1, 2)])
        self.assertTrue(any(p[1] for p in rec.proposed))  # proposed as late
        # B@3 already sealed: a later A@0 cannot resurrect anything.
        _, rec2 = run([ev(3, "B", 2), Watermark(4), ev(0, "A", 1),
                       Watermark(10)])
        self.assertEqual(rec2.final, [])
        self.assertTrue(any(r.kind == "late_incorporated"
                            for r in rec2.reports))

    def test_sealed_match_never_reopens(self):
        # Finalize A@0->B@3, then a withdrawing/re-adding cycle must not
        # recreate it.
        _, rec = run([ev(0, "A", 1), ev(3, "B", 2), Watermark(10),
                      Withdraw("s", 1), ev(0, "A", 1), Watermark(20)])
        self.assertEqual(rec.final, [mid(1, 2)])

    def test_keys_are_independent(self):
        # x-key pair is outside the window (A@0 -> B@9); y-key pair matches.
        # A@0y must not match B@9x across keys.
        _, rec = run([Event("A", 0, "x", "s", 1),
                      Event("B", 9, "x", "s", 2),
                      Event("A", 0, "y", "s", 3),
                      Event("B", 1, "y", "s", 4),
                      Watermark(20)])
        self.assertEqual(rec.final, [("y", ("s", 3), ("s", 4))])


class ConsistencyTests(unittest.TestCase):

    def _streaming_finals(self, events, chunk=10 ** 9, shuffle_rng=None,
                          max_ooo=0):
        """Feed events in given order with auto watermarks; close at end."""
        data = list(events)
        if shuffle_rng is not None:
            shuffle_rng.shuffle(data)
        rec = Recorder()
        m = StreamMatcher(rec)
        buf = []
        for e in data:
            buf.append(e)
            if len(buf) == chunk:
                feed_auto_watermarks(m, buf, max_ooo)
                buf = []
        feed_auto_watermarks(m, buf, max_ooo)
        m.advance(max(e.time for e in events) + 100)
        return {(k, a, b) for (k, a, b) in rec.final}

    def _normalize_ref(self, events):
        ref = reference_matches(events)
        return {(k, (p[0], p[1]), (p[2], p[3]))
                for k, v in ref.items() for p in v}

    def test_arrival_permutations_and_chunking_match_reference(self):
        rng = random.Random(20260921)
        for seed in range(30):
            r = random.Random(seed)
            events = []
            for i in range(r.randint(4, 14)):
                events.append(
                    Event(r.choice("ABC"), r.randint(0, 12),
                          r.choice(["x", "y"]), "s%d" % r.randint(0, 2),
                          i + 1))
            expected = self._normalize_ref(events)

            # In-order (by event time) must equal the reference exactly.
            ordered = sorted(events, key=lambda e: (e.time, e.etype,
                                                    e.source, e.seq))
            got = self._streaming_finals(ordered)
            self.assertEqual(got, expected, seed)

            # Arbitrary arrival orders with bounded out-of-orderness:
            # finals must be a subset of reference (late drops may remove).
            for chunk in (1, 2, 5, 100):
                got = self._streaming_finals(
                    list(events), chunk=chunk,
                    shuffle_rng=random.Random(seed + 9000), max_ooo=5)
                self.assertTrue(got <= expected, (seed, chunk, got, expected))

            # With watermark held open (no late sealing), every order agrees.
            for chunk in (1, 3, 100):
                rec = Recorder()
                m = StreamMatcher(rec)
                data = list(events)
                random.Random(seed + 1).shuffle(data)
                for i in range(0, len(data), chunk):
                    m.push_many(data[i:i + chunk])
                m.advance(10 ** 6)
                got = {(k, a, b) for (k, a, b) in rec.final}
                self.assertEqual(got, expected, (seed, chunk))

    def test_chunk_splitting_determinism(self):
        events = [ev(0, "A", 1), ev(1, "A", 2), ev(2, "C", 3),
                  ev(3, "B", 4), ev(4, "A", 5), ev(6, "B", 6)]
        results = set()
        for chunk in range(1, 7):
            rec = Recorder()
            m = StreamMatcher(rec)
            for i in range(0, len(events), chunk):
                m.push_many(events[i:i + chunk])
            m.advance(100)
            results.add(frozenset(rec.final))
        self.assertEqual(len(results), 1)
        self.assertEqual(set(next(iter(results))),
                         {mid(5, 6)})  # first two As blocked by C@2



if __name__ == "__main__":
    unittest.main(verbosity=2)
