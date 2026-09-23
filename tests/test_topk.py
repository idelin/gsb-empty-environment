import json
import os
import random
import shutil
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from topk import FieldSpec, SortSpec, TopKIndex, reference_topk

SPEC = SortSpec([FieldSpec("score", desc=True, nulls_last=True),
                 FieldSpec("name", desc=False, nulls_last=True)])


def ev(pk, before, after, seq, table="t", tx="x"):
    return {"tx_id": tx, "table": table, "pk": pk, "before": before,
            "after": after, "commit_seq": seq}


def row(grp, score, name="n"):
    return {"grp": grp, "score": score, "name": name}


def replay(events, table="t"):
    records = {}
    for e in events:
        if e["table"] != table:
            continue
        if e["after"] is None:
            records.pop(e["pk"], None)
        else:
            records[e["pk"]] = e["after"]
    return records


def actual_topk(index):
    return {g: [e.id for e in es] for g, es in index.topk().items() if es}


def expected_topk(records, k, spec=SPEC):
    return {g: v for g, v in reference_topk(records, spec, k, "grp").items()
            if v}


class Base(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="topk-test-")
        self.addCleanup(shutil.rmtree, self.tmp, True)

    def make_index(self, k=3, window_size=None, page_size=8, spec=SPEC):
        return TopKIndex(spec, k=k, group_field="grp", table="t",
                         storage_dir=self.tmp, window_size=window_size,
                         page_size=page_size)


class TestSemantics(Base):
    def test_delete_first_promotes_k_plus_one(self):
        idx = self.make_index(k=3)
        events = [ev("u%d" % i, None, row("A", 100 - i * 10), 1)
                  for i in range(1, 6)]  # u1 best ... u5 worst
        idx.apply_batch(events)
        self.assertEqual([e.id for e in idx.topk("A")], ["u1", "u2", "u3"])
        diff = idx.apply_batch([ev("u1", row("A", 90), None, 2)])
        self.assertEqual([e.id for e in idx.topk("A")], ["u2", "u3", "u4"])
        self.assertEqual(diff, {"A": {"entered": ["u4"], "exited": ["u1"]}})

    def test_k_zero_always_empty(self):
        idx = self.make_index(k=0)
        diff = idx.apply_batch([ev("u1", None, row("A", 1), 1),
                                ev("u2", None, row("B", 2), 1)])
        self.assertEqual(diff, {})
        self.assertEqual(idx.topk("A"), [])
        self.assertTrue(all(not es for es in idx.topk().values()))
        snap = idx.snapshot()
        self.assertEqual(snap.page("A"), [])

    def test_update_sort_key_no_duplicate(self):
        idx = self.make_index(k=3)
        idx.apply_batch([ev("u1", None, row("A", 10), 1),
                         ev("u2", None, row("A", 20), 1)])
        idx.apply_batch([ev("u1", row("A", 10), row("A", 99), 2)])
        idx.apply_batch([ev("u1", row("A", 99), row("A", 1), 3)])
        ids = [e.id for e in idx.topk("A")]
        self.assertEqual(ids, ["u2", "u1"])
        self.assertEqual(ids.count("u1"), 1)
        self.assertEqual(len(idx.groups["A"]), 2)

    def test_group_migration_removes_from_old_group(self):
        idx = self.make_index(k=2)
        idx.apply_batch([ev("u1", None, row("A", 90), 1),
                         ev("u2", None, row("A", 80), 1),
                         ev("u3", None, row("B", 10), 1)])
        diff = idx.apply_batch([ev("u1", row("A", 90), row("B", 90), 2)])
        self.assertEqual([e.id for e in idx.topk("A")], ["u2"])
        self.assertEqual([e.id for e in idx.topk("B")], ["u1", "u3"])
        self.assertEqual(diff["A"], {"entered": [], "exited": ["u1"]})
        self.assertEqual(diff["B"], {"entered": ["u1"], "exited": []})
        self.assertEqual(idx.id_group["u1"], "B")

    def test_null_placement(self):
        spec = SortSpec([FieldSpec("score", desc=False, nulls_last=False)])
        idx = self.make_index(k=4, spec=spec)
        idx.apply_batch([ev("n1", None, row("A", None), 1),
                         ev("s1", None, row("A", 5), 1),
                         ev("s2", None, row("A", 1), 1)])
        self.assertEqual([e.id for e in idx.topk("A")], ["n1", "s2", "s1"])
        spec2 = SortSpec([FieldSpec("score", desc=True, nulls_last=True)])
        idx2 = self.make_index(k=4, spec=spec2)
        idx2.apply_batch([ev("n1", None, row("A", None), 1),
                          ev("s1", None, row("A", 5), 1),
                          ev("s2", None, row("A", 1), 1)])
        self.assertEqual([e.id for e in idx2.topk("A")], ["s1", "s2", "n1"])

    def test_multi_field_ordering(self):
        idx = self.make_index(k=4)
        idx.apply_batch([
            ev("a", None, row("G", 10, "z"), 1),
            ev("b", None, row("G", 20, "y"), 1),
            ev("c", None, row("G", 20, "x"), 1),
            ev("d", None, row("G", 20, "x"), 1),  # full tie with c -> id
        ])
        self.assertEqual([e.id for e in idx.topk("G")],
                         ["c", "d", "b", "a"])  # score desc, name asc, id

    def test_tie_break_by_id_at_last_rank(self):
        idx = self.make_index(k=3)
        events = [ev("u%d" % i, None, row("A", 50), 1) for i in range(1, 9)]
        idx.apply_batch(events)  # 8 identical keys, k=3
        self.assertEqual([e.id for e in idx.topk("A")], ["u1", "u2", "u3"])
        idx.apply_batch([ev("u1", row("A", 50), None, 2)])
        self.assertEqual([e.id for e in idx.topk("A")], ["u2", "u3", "u4"])

    def test_other_table_ignored(self):
        idx = self.make_index(k=2)
        idx.apply_batch([ev("x", None, row("A", 1), 1, table="other")])
        self.assertEqual(idx.topk(), {})


class TestBlockBound(Base):
    def test_delete_backfill_reads_only_head_block(self):
        idx = self.make_index(k=5, window_size=8, page_size=16)
        events = [ev("u%04d" % i, None, row("hot", i), 1)
                  for i in range(2000)]
        idx.apply_batch(events)
        n_pages = len(idx.groups["hot"].pages)
        self.assertGreater(n_pages, 10)  # data really spilled to segments
        before = [e.id for e in idx.groups["hot"].topk(6)]
        idx.stats.reset()
        idx.apply_batch([ev(before[0], row("hot", 0), None, 2)])
        stats = idx.stats.snapshot()
        after = [e.id for e in idx.topk("hot")]
        self.assertEqual(after, before[1:6])  # ranks 2..K+1 shift up
        self.assertLessEqual(stats["blocks_read"], 2,
                             "backfill must not scan the whole group")
        self.assertLessEqual(stats["blocks_written"], 2)
        print("\n[blocks] delete-first backfill over %d pages: %s"
              % (n_pages, stats))


class TestRecovery(Base):
    def test_recovery_round_trip(self):
        rng = random.Random(7)
        idx = self.make_index(k=4, window_size=5, page_size=4)
        records = {}
        for seq in range(1, 30):
            pk = "u%d" % rng.randrange(120)
            before = records.get(pk)
            if before is not None and rng.random() < 0.3:
                records.pop(pk)
                idx.apply_batch([ev(pk, before, None, seq)])
            else:
                val = row(rng.choice(["A", "B", "C"]), rng.randrange(100))
                records[pk] = val
                idx.apply_batch([ev(pk, before, val, seq)])
        idx.save()
        restored = TopKIndex.load(self.tmp)
        self.assertEqual(actual_topk(restored), actual_topk(idx))
        self.assertEqual(actual_topk(restored), expected_topk(records, 4))
        self.assertEqual(restored.commit_seq, idx.commit_seq)
        # keep writing to the restored index
        idx.apply_batch([ev("u999", None, row("A", 500), 100)])
        restored.apply_batch([ev("u999", None, row("A", 500), 100)])
        records["u999"] = row("A", 500)
        self.assertEqual(actual_topk(restored), expected_topk(records, 4))


class TestSnapshot(Base):
    def test_snapshot_pagination_fixed_version(self):
        idx = self.make_index(k=5)
        idx.apply_batch([ev("u%d" % i, None, row("A", i), 1)
                         for i in range(10)])
        snap1 = idx.snapshot()
        page_before = snap1.page("A", 0, 3)
        idx.apply_batch([ev("u0", row("A", 0), None, 2),
                         ev("uX", None, row("A", 999), 2)])
        snap2 = idx.snapshot()
        self.assertLess(snap1.version, snap2.version)
        self.assertEqual(snap1.page("A", 0, 3), page_before)  # pinned
        self.assertNotEqual([r["id"] for r in snap2.page("A", 0, 3)],
                            [r["id"] for r in page_before])
        full = [r["id"] for r in snap2.page("A")]
        paged = []
        for off in range(0, 5, 2):
            paged += [r["id"] for r in snap2.page("A", off, 2)]
        self.assertEqual(full, paged)


class TestRandomizedReference(Base):
    def test_skewed_groups_tiny_memory_vs_full_sort(self):
        rng = random.Random(20260923)
        k = 4
        idx = self.make_index(k=k, window_size=4, page_size=3)
        records = {}
        pool = (["hot"] * 70 + ["g1", "g2", "g3", None] * 6)
        seq = 0
        for _ in range(40):
            events = []
            for _ in range(20):
                seq += 1
                roll = rng.random()
                if roll < 0.55 or not records:
                    pk = "u%d" % rng.randrange(400)
                    before = records.get(pk)
                    val = {"grp": rng.choice(pool),
                           "score": rng.choice([None, 1, 2, 3, 5, 8,
                                                rng.randrange(100)]),
                           "name": rng.choice("abcd")}
                    records[pk] = val
                    events.append(ev(pk, before, val, seq))
                elif roll < 0.75:
                    pk = rng.choice(list(records))
                    before = records.pop(pk)
                    events.append(ev(pk, before, None, seq))
                else:  # group migration
                    pk = rng.choice(list(records))
                    before = records[pk]
                    val = dict(before)
                    val["grp"] = rng.choice(pool)
                    records[pk] = val
                    events.append(ev(pk, before, val, seq))
            idx.apply_batch(events)
            self.assertEqual(actual_topk(idx), expected_topk(records, k))
        stats = idx.stats.snapshot()
        print("\n[blocks] randomized run totals: %s" % stats)


class TestFixture(Base):
    def test_fixture_replay_matches_reference(self):
        path = os.path.join(os.path.dirname(os.path.dirname(
            os.path.abspath(__file__))), "fixtures", "changes.jsonl")
        with open(path, encoding="utf-8") as fh:
            events = [json.loads(line) for line in fh if line.strip()]
        idx = TopKIndex(SPEC, k=3, group_field="grp", table="scores",
                        storage_dir=self.tmp, window_size=4, page_size=4)
        idx.apply_batch(events)
        self.assertEqual(actual_topk(idx),
                         expected_topk(replay(events, "scores"), 3))


if __name__ == "__main__":
    unittest.main(verbosity=2)
