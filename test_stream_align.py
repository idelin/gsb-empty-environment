import random
import unittest

from stream_align import (
    Aligner,
    Emit,
    Finalize,
    Replace,
    Retract,
    align_batch,
    replay,
)


class PromptExampleTest(unittest.TestCase):
    """Main at t=10, aux at t=8 and t=12, tol=2 -> pick 8; late t=9 -> 9."""

    def test_example(self):
        al = Aligner(tolerance=2, sources=["aux"])
        events = al.add_anchor("m1", time=10)
        events += al.add_sample("aux", "s8", time=8)
        events += al.add_sample("aux", "s12", time=12)
        table, _ = replay(events)
        self.assertEqual(table["m1"].selections["aux"], "s8")  # tie -> earlier
        self.assertEqual(table["m1"].version, 2)

        late = al.add_sample("aux", "s9", time=9)
        self.assertIsInstance(late[0], Replace)
        table, _ = replay(events + late)
        self.assertEqual(table["m1"].selections["aux"], "s9")
        self.assertEqual(table["m1"].version, 3)


class TieBreakTest(unittest.TestCase):
    def test_same_time_smaller_seq_wins(self):
        al = Aligner(tolerance=2, sources=["aux"])
        al.add_anchor("m1", time=10)
        al.add_sample("aux", "hi", time=9, seq=7)
        events = al.add_sample("aux", "lo", time=9, seq=3)
        self.assertIsInstance(events[0], Replace)
        self.assertEqual(events[0].result.selections["aux"], "lo")

    def test_equal_distance_earlier_time_wins(self):
        al = Aligner(tolerance=5, sources=["aux"])
        al.add_anchor("m1", time=10)
        al.add_sample("aux", "late", time=12)
        events = al.add_sample("aux", "early", time=8)
        self.assertEqual(events[0].result.selections["aux"], "early")


class SharedSampleTest(unittest.TestCase):
    def test_one_sample_serves_many_anchors(self):
        al = Aligner(tolerance=2, sources=["aux"])
        al.add_anchor("m1", time=10)
        al.add_anchor("m2", time=11)
        events = al.add_sample("aux", "s10", time=10)
        self.assertEqual(len(events), 2)  # both anchors replaced
        table, _ = replay(events)
        self.assertEqual(table["m1"].selections["aux"], "s10")
        self.assertEqual(table["m2"].selections["aux"], "s10")


class RetractionFallbackTest(unittest.TestCase):
    def test_retract_best_falls_back_to_next(self):
        al = Aligner(tolerance=3, sources=["aux"])
        al.add_anchor("m1", time=10)
        al.add_sample("aux", "best", time=9)   # dist 1
        al.add_sample("aux", "next", time=7)   # dist 3
        events = al.retract_sample("aux", "best")
        self.assertIsInstance(events[0], Replace)
        self.assertEqual(events[0].result.selections["aux"], "next")

    def test_retract_last_candidate_marks_missing(self):
        al = Aligner(tolerance=3, sources=["aux"])
        al.add_anchor("m1", time=10)
        al.add_sample("aux", "only", time=9)
        events = al.retract_sample("aux", "only")
        res = events[0].result
        self.assertIsNone(res.selections["aux"])
        self.assertEqual(res.missing, ("aux",))


class MissingTest(unittest.TestCase):
    def test_only_out_of_tolerance_sample(self):
        al = Aligner(tolerance=2, sources=["aux"])
        events = al.add_anchor("m1", time=10)
        al.add_sample("aux", "far", time=20)
        res = events[0].result
        self.assertIsNone(res.selections["aux"])
        self.assertEqual(res.missing, ("aux",))


class AnchorRevokeTest(unittest.TestCase):
    def test_retract_anchor(self):
        al = Aligner(tolerance=2, sources=["aux"])
        al.add_anchor("m1", time=10)
        al.add_sample("aux", "s9", time=9)
        events = al.retract_anchor("m1")
        self.assertEqual(events, [Retract("m1")])
        table, _ = replay(events)
        self.assertNotIn("m1", table)
        # further sample updates must not resurrect the anchor
        self.assertEqual(al.add_sample("aux", "s10", time=10), [])


class WatermarkTest(unittest.TestCase):
    def test_finalized_result_cannot_be_rewritten(self):
        al = Aligner(tolerance=2, sources=["aux"])
        al.add_anchor("m1", time=10)
        al.add_sample("aux", "s8", time=8)
        fin = al.advance_watermark("aux", 12)  # 10 + tol -> final
        self.assertEqual(fin, [Finalize("m1", 2)])
        # late better sample and retraction are both ignored for m1
        self.assertEqual(al.add_sample("aux", "s10", time=10), [])
        self.assertEqual(al.retract_sample("aux", "s8"), [])
        self.assertEqual(al.current_table()["m1"].selections["aux"], "s8")
        self.assertEqual(al.current_table()["m1"].version, 2)

    def test_anchor_arriving_after_watermark_is_born_final(self):
        al = Aligner(tolerance=2, sources=["aux"])
        al.advance_watermark("aux", 100)
        al.add_sample("aux", "s50", time=50)
        events = al.add_anchor("m1", time=50)
        self.assertIsInstance(events[0], Emit)
        self.assertIsInstance(events[1], Finalize)
        self.assertTrue(al.is_finalized("m1"))


class GcTest(unittest.TestCase):
    def test_gc_keeps_candidates_for_open_anchors(self):
        al = Aligner(tolerance=2, sources=["aux"])
        al.add_anchor("open", time=50)          # open, window [48, 52]
        al.add_anchor("done", time=10)
        al.advance_watermark("aux", 12)         # finalizes "done" only
        al.advance_main_watermark(40)           # future anchors >= 40
        for sid, t in [("a", 5), ("b", 11), ("c", 38), ("d", 49)]:
            al.add_sample("aux", sid, t)
        dropped = al.gc()
        # cutoff = min(40, 50) - 2 = 38 -> only t=5 and t=11 are dead
        self.assertEqual(dropped, 2)
        self.assertEqual(al._select("aux", 49), "d")
        self.assertEqual(al._select("aux", 40), "c")

    def test_gc_without_open_anchors_uses_main_watermark(self):
        al = Aligner(tolerance=2, sources=["aux"])
        al.advance_main_watermark(30)
        al.add_sample("aux", "old", 10)
        al.add_sample("aux", "keep", 29)
        self.assertEqual(al.gc(), 1)
        self.assertEqual(al._select("aux", 30), "keep")


class MultiSourceTest(unittest.TestCase):
    def test_sources_are_independent(self):
        al = Aligner(tolerance=2, sources=["a", "b"])
        al.add_anchor("m1", time=10)
        al.add_sample("a", "sa", time=9)
        res = al.current_table()["m1"]
        self.assertEqual(res.selections, {"a": "sa", "b": None})
        self.assertEqual(res.missing, ("b",))


class FuzzTest(unittest.TestCase):
    """Random arrival orders must converge to the full-scan reference."""

    def run_scenario(self, seed):
        rng = random.Random(seed)
        sources = ["s1", "s2"]
        tol = 3
        al = Aligner(tolerance=tol, sources=sources)
        events = []
        anchors = {}          # id -> (time, seq)
        samples = {s: {} for s in sources}  # id -> (time, seq)
        live_anchors, live_samples = set(), {s: set() for s in sources}
        main_wm = 0

        for step in range(300):
            op = rng.random()
            if op < 0.35:
                aid = f"a{step}"
                t = rng.randint(main_wm, 60)  # respect the main watermark
                anchors[aid] = (t, 0)
                live_anchors.add(aid)
                events += al.add_anchor(aid, t)
            elif op < 0.75:
                s = rng.choice(sources)
                sid = f"x{step}"
                t = rng.randint(0, 60)
                samples[s][sid] = (t, rng.randint(0, 5))
                live_samples[s].add(sid)
                events += al.add_sample(s, sid, t, samples[s][sid][1])
            elif op < 0.85 and any(live_samples[s] for s in sources):
                s = rng.choice([s for s in sources if live_samples[s]])
                sid = rng.choice(sorted(live_samples[s]))
                live_samples[s].discard(sid)
                del samples[s][sid]
                events += al.retract_sample(s, sid)
            elif live_anchors:
                aid = rng.choice(sorted(live_anchors))
                live_anchors.discard(aid)
                del anchors[aid]
                events += al.retract_anchor(aid)
            if rng.random() < 0.2:
                main_wm = max(main_wm, rng.randint(0, 60))
                al.advance_main_watermark(main_wm)
                al.gc()

        for s in sources:  # close everything out
            events += al.advance_watermark(s, 10_000)

        table, finalized = replay(events)
        ref = align_batch(
            [(aid, t, seq) for aid, (t, seq) in anchors.items()],
            {s: [(sid, t, seq) for sid, (t, seq) in samples[s].items()]
             for s in sources},
            tol,
        )
        self.assertEqual(set(table), set(ref))
        self.assertEqual(finalized, set(ref))
        for aid, res in ref.items():
            self.assertEqual(
                table[aid].selections, res.selections, f"seed={seed} {aid}"
            )

    def test_random_orders_match_reference(self):
        for seed in range(30):
            with self.subTest(seed=seed):
                self.run_scenario(seed)


if __name__ == "__main__":
    unittest.main(verbosity=2)
