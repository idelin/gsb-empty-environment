"""Event-interleaving tests: edits, queries, cancels, snapshots vs oracle.

A seeded random event stream interleaves segment moves/inserts/deletes,
exclusion toggles, radius-limited queries in both modes, query
cancellation and historical snapshots.  Every query result is compared
against a brute-force all-pairs enumeration of the same model.  At the
end, resource accounting proves every old version was released.
"""

import gc
import random
import unittest
from fractions import Fraction as F

from engine import Engine, QueryCancelled
from tests.oracle import oracle_query_all, oracle_query_pair


class StepToken:
    """Cancels the query after ``limit`` traversal steps."""

    def __init__(self, limit):
        self.limit = limit
        self.calls = 0

    def check(self):
        self.calls += 1
        if self.calls > self.limit:
            raise QueryCancelled("step limit reached")


def copy_model(model):
    return {g: dict(segs) for g, segs in model.items()}


class TestEventInterleaving(unittest.TestCase):
    def test_random_event_stream_matches_oracle(self):
        rng = random.Random(20260925)
        groups = ["A", "B", "C", "D"]
        eng = Engine()
        for g in groups:
            eng.add_group(g)
        model = {g: {} for g in groups}
        exclusions = set()
        open_snapshots = []  # (Snapshot, model_copy, exclusions_copy)
        stats = {"set": 0, "del": 0, "excl": 0, "qall": 0, "qpair": 0,
                 "snap": 0, "cancel": 0, "cancelled": 0}

        def rand_point():
            denom = rng.choice([1, 1, 1, 2, 3])
            return (F(rng.randint(-20, 20), denom),
                    F(rng.randint(-20, 20), denom))

        def check_all(radius, mode):
            res = eng.query_all(radius=radius, mode=mode)
            exp_d, exp_ev = oracle_query_all(
                model, exclusions,
                None if radius is None else radius ** 2, mode)
            self.assertEqual(res.dist2, exp_d)
            self.assertEqual(res.evidence, exp_ev)

        for step in range(500):
            op = rng.random()
            if op < 0.34:
                g = rng.choice(groups)
                sid = rng.randrange(12)
                p0, p1 = rand_point(), rand_point()
                if rng.random() < 0.08:
                    p1 = p0  # zero-length segment
                eng.set_segment(g, sid, p0, p1)
                model[g][sid] = (p0, p1)
                stats["set"] += 1
            elif op < 0.44:
                g = rng.choice(groups)
                sid = rng.randrange(12)
                if sid in model[g]:
                    eng.remove_segment(g, sid)
                    del model[g][sid]
                    stats["del"] += 1
            elif op < 0.52:
                a, b = rng.sample(groups, 2)
                key = frozenset((a, b))
                if key in exclusions:
                    eng.clear_exclusion(a, b)
                    exclusions.discard(key)
                else:
                    eng.set_exclusion(a, b)
                    exclusions.add(key)
                stats["excl"] += 1
            elif op < 0.72:
                radius = rng.choice([None, None, F(3), F(15, 2), F(11, 3)])
                mode = rng.choice(["all", "representative"])
                check_all(radius, mode)
                stats["qall"] += 1
            elif op < 0.80:
                a, b = rng.sample(groups, 2)
                mode = rng.choice(["all", "representative"])
                res = eng.query(a, b, mode=mode)
                exp_d, exp_ev = oracle_query_pair(model, a, b, exclusions,
                                                  None, mode)
                self.assertEqual(res.dist2, exp_d)
                self.assertEqual(res.evidence, exp_ev)
                stats["qpair"] += 1
            elif op < 0.90:
                # Open a snapshot, then query a random open snapshot and
                # compare against the model frozen at open time.
                snap = eng.snapshot()
                open_snapshots.append(
                    (snap, copy_model(model), frozenset(exclusions)))
                stats["snap"] += 1
                idx = rng.randrange(len(open_snapshots))
                old_snap, old_model, old_excl = open_snapshots[idx]
                res = eng.query_all(snapshot=old_snap)
                exp_d, exp_ev = oracle_query_all(old_model, old_excl)
                self.assertEqual(res.dist2, exp_d)
                self.assertEqual(res.evidence, exp_ev)
                # Randomly close some snapshots.
                still_open = []
                for item in open_snapshots:
                    if rng.random() < 0.4:
                        item[0].close()
                    else:
                        still_open.append(item)
                open_snapshots = still_open
            else:
                # Cancel an in-flight query; the engine must stay
                # consistent and the implicit snapshot must be released.
                token = StepToken(rng.randrange(4))
                live_before = eng.pool.live
                try:
                    eng.query_all(cancel=token)
                except QueryCancelled:
                    stats["cancelled"] += 1
                gc.collect()
                self.assertEqual(eng.pool.live, live_before)
                check_all(None, "all")
                stats["cancel"] += 1

        # Every snapshot closed -> every old version released.
        for snap, _, _ in open_snapshots:
            snap.close()
        gc.collect()
        self.assertEqual(eng.pool.live, eng.node_count())
        self.assertEqual(eng.pool.created, eng.pool.live + eng.pool.freed)

        # The stream must actually exercise every event kind.
        for key, count in stats.items():
            self.assertGreater(count, 0, f"event kind {key} never fired")
        print(f"\ninterleaving stats: {stats}")

    def test_deterministic_results_across_identical_streams(self):
        def run():
            rng = random.Random(7)
            eng = Engine()
            for g in ("X", "Y"):
                eng.add_group(g)
            out = []
            for _ in range(60):
                sid = rng.randrange(6)
                p0 = (F(rng.randint(-9, 9)), F(rng.randint(-9, 9)))
                p1 = (F(rng.randint(-9, 9)), F(rng.randint(-9, 9)))
                eng.set_segment(rng.choice(("X", "Y")), sid, p0, p1)
                out.append(eng.query_all())
            return out

        self.assertEqual(run(), run())


if __name__ == "__main__":
    unittest.main()
