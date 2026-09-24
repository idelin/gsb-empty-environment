"""unittest suite for the grouped retractable Top-K engine."""
import os
import random
import tempfile
import unittest

from topk import fixtures
from topk.engine import GroupedTopK, Pipeline
from topk.reference import expected_tops, replay_events, verify_engine

# score descending, nulls last; ts ascending, nulls first; pk breaks ties.
SPEC = fixtures.DEFAULT_SPEC


def rec(grp, score, ts=1):
    return {"grp": grp, "score": score, "ts": ts}


class EngineCase(unittest.TestCase):
    def setUp(self):
        self._td = tempfile.TemporaryDirectory()
        self.addCleanup(self._td.cleanup)

    def engine(self, k=3, spec=SPEC, buffer_size=8, spill_slack=8,
               group_field="grp", name="t"):
        return GroupedTopK(name, k, spec, group_field,
                           os.path.join(self._td.name, name),
                           buffer_size=buffer_size, spill_slack=spill_slack)

    def commit(self, engine, *rows, seq=None):
        return engine.commit([(r["pk"], r.get("before"), r.get("after"))
                              for r in rows], commit_seq=seq)

    def insert(self, engine, pk, grp, score, ts=1):
        engine.commit([(pk, None, rec(grp, score, ts))], commit_seq=pk)

    def delete(self, engine, pk, grp, score, ts=1, seq=None):
        engine.commit([(pk, rec(grp, score, ts), None)],
                      commit_seq=seq if seq is not None else 9000 + pk)

    # ------------------------------------------------------------------ #
    def test_delete_first_backfills_k_plus_1(self):
        e = self.engine(k=3)
        for i in range(1, 6):
            self.insert(e, i, "g", score=10 * i)
        self.assertEqual(e.top_ids("g"), [5, 4, 3])
        e.commit([(5, rec("g", 50), None)], commit_seq=100)
        # rank 1 removed; the former K+1-th (pk 2) moves in.
        self.assertEqual(e.top_ids("g"), [4, 3, 2])

    def test_k_zero_always_empty(self):
        e = self.engine(k=0)
        entry = e.commit([(1, None, rec("g", 1)), (2, None, rec("g", 2))],
                         commit_seq=1)
        self.assertEqual(e.top("g"), [])
        self.assertEqual(entry["groups"], {})
        self.assertEqual(e.snapshot().page("g", 0, 10), [])
        self.assertEqual(e.candidate_count("g"), 2)

    def test_group_migration_removes_from_old_group(self):
        e = self.engine(k=2)
        for pk, score in ((1, 1), (2, 2), (3, 3)):
            self.insert(e, pk, "g1", score)
        self.insert(e, 9, "g2", score=9)
        self.assertEqual(e.top_ids("g1"), [3, 2])
        e.commit([(3, rec("g1", 3), rec("g2", 3))], commit_seq=50)
        self.assertEqual(e.top_ids("g1"), [2, 1])
        self.assertEqual(e.top_ids("g2"), [9, 3])
        # same pk exists exactly once, in the new group
        self.assertEqual(e._id_index[3][0], "g2")
        self.assertEqual(e.candidate_count("g1") + e.candidate_count("g2"), 4)

    def test_sort_key_update_does_not_duplicate(self):
        e = self.engine(k=2)
        for pk, score in ((1, 10), (2, 20), (3, 30)):
            self.insert(e, pk, "g", score)
        self.assertEqual(e.top_ids("g"), [3, 2])
        e.commit([(1, rec("g", 10), rec("g", 40))], commit_seq=10)
        self.assertEqual(e.top_ids("g"), [1, 3])
        self.assertEqual(e.candidate_count("g"), 3)
        problems = verify_engine(e, {1: rec("g", 40), 2: rec("g", 20),
                                     3: rec("g", 30)})
        self.assertEqual(problems, [])

    def test_multifield_order_and_null_positioning(self):
        spec = (("a", False, True),   # a ascending, nulls first
                ("b", True, False))   # b descending, nulls last
        e = self.engine(k=4, spec=spec)
        rows = {1: {"grp": "g", "a": None, "b": 5},
                2: {"grp": "g", "a": 1, "b": None},
                3: {"grp": "g", "a": 1, "b": 7},
                4: {"grp": "g", "a": 2, "b": 1}}
        e.commit([(pk, None, row) for pk, row in rows.items()], commit_seq=1)
        self.assertEqual(e.top_ids("g"), [1, 3, 2, 4])

    def test_tie_break_by_id_at_last_rank(self):
        e = self.engine(k=3)
        for pk in range(1, 5):
            self.insert(e, pk, "g", score=10)
        self.assertEqual(e.top_ids("g"), [1, 2, 3])
        e.commit([(0, None, rec("g", 10))], commit_seq=10)
        self.assertEqual(e.top_ids("g"), [0, 1, 2])
        e.commit([(0, rec("g", 10), None)], commit_seq=11)
        self.assertEqual(e.top_ids("g"), [1, 2, 3])

    def test_snapshot_pagination_is_pinned_to_version(self):
        e = self.engine(k=6)
        for pk in range(1, 7):
            self.insert(e, pk, "g", score=pk)
        old = e.snapshot()
        self.assertEqual(old.version, 6)
        self.delete(e, 6, "g", 6, seq=20)
        pages = [old.page("g", offset=off, limit=2) for off in (0, 2, 4)]
        self.assertEqual([pk for page in pages for pk, _ in page], [6, 5, 4, 3, 2, 1])
        self.assertEqual(old.ids("g"), [6, 5, 4, 3, 2, 1])
        self.assertEqual(e.top_ids("g"), [5, 4, 3, 2, 1])
        self.assertEqual(e.snapshot().ids("g"), [5, 4, 3, 2, 1])

    def test_commit_delta_is_stable_id_difference(self):
        e = self.engine(k=2)
        d1 = e.commit([(1, None, rec("g", 10)), (2, None, rec("g", 20))],
                      commit_seq=1)
        self.assertEqual(d1["groups"], {"g": {"added": [2, 1], "removed": []}})
        d2 = e.commit([(3, None, rec("g", 30))], commit_seq=2)
        self.assertEqual(d2["groups"], {"g": {"added": [3], "removed": [1]}})
        d3 = e.commit([(3, rec("g", 30), None)], commit_seq=3)
        self.assertEqual(d3["groups"], {"g": {"added": [1], "removed": [3]}})

    def test_hot_group_skew_matches_full_sort(self):
        e = self.engine(k=5, buffer_size=4, spill_slack=4)
        records = {}
        # one hot group with hundreds of rows
        for pk in range(1, 401):
            row = rec("hot", (pk * 37) % 1000, ts=pk)
            records[pk] = row
            e.commit([(pk, None, row)], commit_seq=pk)
        # a couple of tiny groups
        for pk in (901, 902, 903):
            row = rec("cold", pk, ts=1)
            records[pk] = row
            e.commit([(pk, None, row)], commit_seq=pk)
        self.assertGreater(e.store.writes, 0)
        self.assertEqual(verify_engine(e, records), [])
        self.assertEqual(e.candidate_count("hot"), 400)

    def test_tiny_memory_still_correct_and_counts_blocks(self):
        # keep window is k+1 with slack 1: everything else lives on disk.
        e = self.engine(k=3, buffer_size=1, spill_slack=1)
        records = {}
        for pk in range(1, 51):
            row = rec("g", 100 - pk, ts=pk)
            records[pk] = row
            e.commit([(pk, None, row)], commit_seq=pk)
        self.assertGreater(e.store.writes, 0)
        self.assertEqual(e.top_ids("g"), [1, 2, 3])
        # delete the top two; backfill must pull from segments correctly.
        reads_before = e.store.reads
        e.commit([(1, rec("g", 99, ts=1), None),
                  (2, rec("g", 98, ts=2), None)], commit_seq=100)
        self.assertEqual(e.top_ids("g"), [3, 4, 5])
        self.assertGreater(e.store.reads, reads_before)
        records.pop(1); records.pop(2)
        self.assertEqual(verify_engine(e, records), [])

    def test_delete_backfill_is_bounded_not_full_scan(self):
        e = self.engine(k=5, buffer_size=8, spill_slack=8)
        n = 2000
        records = {}
        for pk in range(1, n + 1):
            row = rec("g", score=pk, ts=pk)
            records[pk] = row
            e.commit([(pk, None, row)], commit_seq=pk)
        segments = len(e._segments["g"])
        self.assertGreater(segments, 1)
        # buffer covers the first 8 deletions: zero block reads.
        e.store.reads = 0
        for rank, pk in enumerate(range(n, n - 8, -1)):
            e.commit([(pk, rec("g", pk, ts=pk), None)],
                     commit_seq=3000 + rank)
            records.pop(pk)
        self.assertEqual(e.store.reads, 0)
        self.assertEqual(e.top_ids("g")[:5],
                         [n - 8, n - 9, n - 10, n - 11, n - 12])
        # draining the buffer merges only a few overlapping segments,
        # never a scan proportional to the group size.
        for rank, pk in enumerate(range(n - 8, n - 300, -1)):
            e.commit([(pk, rec("g", pk, ts=pk), None)],
                     commit_seq=4000 + rank)
            records.pop(pk)
        self.assertLess(e.store.reads, segments)
        self.assertEqual(verify_engine(e, records), [])

    def test_recovery_from_checkpoint(self):
        e = self.engine(k=5, buffer_size=2, spill_slack=2)
        events = fixtures.generate_events()
        pipe = Pipeline({"metrics": e})
        head = [ev for ev in events if ev["commit_seq"] <= 6]
        tail = [ev for ev in events if ev["commit_seq"] > 6]
        pipe.apply_events(head)
        stats = e.store.stats()
        pipe.checkpoint()
        e2 = GroupedTopK.recover(os.path.join(self._td.name, "t"))
        self.assertEqual(e2.version, e.version)
        self.assertEqual(e2.top_ids("hot"), e.top_ids("hot"))
        self.assertEqual(e2.store.reads, stats["block_reads"])
        # old snapshots survive recovery
        self.assertEqual(e2.snapshot().ids("hot"), e.top_ids("hot"))
        Pipeline({"metrics": e2}).apply_events(tail)
        records = replay_events(events)["metrics"]
        self.assertEqual(verify_engine(e2, records), [])
        e2.checkpoint()
        e3 = GroupedTopK.recover(os.path.join(self._td.name, "t"))
        self.assertEqual(verify_engine(e3, records), [])

    def test_pipeline_applies_by_commit_seq_and_routes_tables(self):
        e = self.engine(k=5)
        events = fixtures.generate_events()
        shuffled = events[:]
        random.Random(1234).shuffle(shuffled)
        Pipeline({"metrics": e}).apply_events(shuffled)
        records = replay_events(events)["metrics"]
        self.assertEqual(verify_engine(e, records), [])
        last_seq = max(ev["commit_seq"] for ev in events
                       if ev["table"] == "metrics")
        self.assertEqual(e.commit_log[-1]["commit_seq"], last_seq)

    def test_tie_group_matches_oracle_after_deletes_and_updates(self):
        e = self.engine(k=4)
        events = fixtures.generate_events()
        Pipeline({"metrics": e}).apply_events(events)
        records = replay_events(events)["metrics"]
        self.assertEqual(verify_engine(e, records), [])
        # last-rank ties are id-decided, matching the full-sort oracle
        want = expected_tops(records, "grp", SPEC, 4)["tie"]
        self.assertEqual([list(t) for t in e.top("tie")],
                         [list(t) for t in want])


if __name__ == "__main__":
    unittest.main(verbosity=2)
