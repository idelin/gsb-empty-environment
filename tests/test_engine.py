import json
import os
import random
import tempfile
import unittest

from distinct_agg import (ChangeEvent, DistinctCountEngine, MissingEventError,
                          load_events, reference_counts)
from distinct_agg.engine import group_key_of, value_key_of

FIXTURE = os.path.join(os.path.dirname(__file__), "..", "fixtures",
                       "changes.jsonl")


def ev(seq, pk, before, after, txn="t", table="items"):
    return ChangeEvent(txn_id=f"{txn}{seq}", table=table, pk=pk,
                       before=before, after=after, commit_seq=seq)


def row(dept="eng", tag="x", score=5):
    return {"dept": dept, "tag": tag, "score": score}


class EngineCase(unittest.TestCase):
    def make(self, **kw):
        kw.setdefault("group_cols", ["dept"])
        kw.setdefault("distinct_cols", ["tag"])
        return DistinctCountEngine(**kw)

    def counts(self, eng):
        return {json.loads(g)[0]: n for g, n in eng.snapshot_counts().items()}

    def test_duplicate_rows_same_value_count_one_distinct(self):
        eng = self.make()
        eng.apply(ev(1, "A", None, row()))
        eng.apply(ev(2, "B", None, row()))
        self.assertEqual(self.counts(eng), {"eng": 1})
        g = group_key_of(row(), ["dept"])
        v = value_key_of(row(), ["tag"], "A", True)
        self.assertEqual(eng.support_count(g, v), 2)

    def test_delete_one_of_two_still_one_delete_last_decrements(self):
        eng = self.make()
        eng.apply(ev(1, "A", None, row()))
        eng.apply(ev(2, "B", None, row()))
        eng.apply(ev(3, "B", row(), None))
        self.assertEqual(self.counts(eng), {"eng": 1})  # 删一仍计 1
        eng.apply(ev(4, "A", row(), None))
        self.assertEqual(self.counts(eng), {})  # 删最后一条才减

    def test_negative_count_rejected_and_state_untouched(self):
        eng = self.make()
        eng.apply(ev(1, "A", None, row()))
        with self.assertRaises(MissingEventError):
            eng.apply(ev(2, "B", row(), None))  # 无对应插入 => 缺事件
        self.assertEqual(self.counts(eng), {"eng": 1})
        # 重复删除同一主键同样被拒绝
        eng.apply(ev(3, "A", row(), None))
        with self.assertRaises(MissingEventError):
            eng.apply(ev(4, "A", row(), None))
        self.assertEqual(self.counts(eng), {})

    def test_out_of_order_or_replayed_seq_rejected(self):
        eng = self.make()
        eng.apply(ev(5, "A", None, row()))
        with self.assertRaises(MissingEventError):
            eng.apply(ev(3, "B", None, row()))  # 序号回退 => 疑似缺事件

    def test_duplicate_delivery_deduped(self):
        eng = self.make()
        e = ev(1, "A", None, row())
        self.assertEqual(eng.apply(e), "applied")
        self.assertEqual(eng.apply(e), "duplicate")
        self.assertEqual(self.counts(eng), {"eng": 1})

    def test_cross_group_update_atomic(self):
        eng = self.make()
        eng.apply(ev(1, "A", None, row(dept="eng", tag="x")))
        eng.apply(ev(2, "B", None, row(dept="eng", tag="y")))
        eng.apply(ev(3, "A", row(dept="eng", tag="x"), row(dept="ops", tag="x")))
        self.assertEqual(self.counts(eng), {"eng": 1, "ops": 1})
        # 迁移可再迁回，贡献不丢不重
        eng.apply(ev(4, "A", row(dept="ops", tag="x"), row(dept="eng", tag="x")))
        self.assertEqual(self.counts(eng), {"eng": 2})

    def test_update_same_group_value_change(self):
        eng = self.make()
        eng.apply(ev(1, "A", None, row(tag="x")))
        eng.apply(ev(2, "A", row(tag="x"), row(tag="y")))
        self.assertEqual(self.counts(eng), {"eng": 1})

    def test_before_image_mismatch_rejected(self):
        eng = self.make()
        eng.apply(ev(1, "A", None, row(tag="x")))
        with self.assertRaises(MissingEventError):
            eng.apply(ev(2, "A", row(tag="z"), row(tag="y")))
        self.assertEqual(self.counts(eng), {"eng": 1})

    def test_null_equal_true_collapses(self):
        eng = self.make(null_equal=True)
        eng.apply(ev(1, "A", None, row(tag=None)))
        eng.apply(ev(2, "B", None, row(tag=None)))
        self.assertEqual(self.counts(eng), {"eng": 1})
        eng.apply(ev(3, "A", row(tag=None), None))
        self.assertEqual(self.counts(eng), {"eng": 1})  # B 仍支持
        eng.apply(ev(4, "B", row(tag=None), None))
        self.assertEqual(self.counts(eng), {})

    def test_null_equal_false_each_null_distinct(self):
        eng = self.make(null_equal=False)
        eng.apply(ev(1, "A", None, row(tag=None)))
        eng.apply(ev(2, "B", None, row(tag=None)))
        self.assertEqual(self.counts(eng), {"eng": 2})
        eng.apply(ev(3, "A", row(tag=None), None))
        self.assertEqual(self.counts(eng), {"eng": 1})

    def test_multi_column_composite_distinct(self):
        eng = self.make(distinct_cols=["tag", "score"])
        eng.apply(ev(1, "A", None, row(tag="x", score=5)))
        eng.apply(ev(2, "B", None, row(tag="x", score=5)))  # 复合键相同
        eng.apply(ev(3, "C", None, row(tag="x", score=6)))  # 复合键不同
        self.assertEqual(self.counts(eng), {"eng": 2})

    def test_conditional_filter(self):
        eng = self.make(filters=[("score", "ge", 3)])
        eng.apply(ev(1, "A", None, row(tag="x", score=5)))
        eng.apply(ev(2, "B", None, row(tag="y", score=1)))  # 被过滤
        self.assertEqual(self.counts(eng), {"eng": 1})
        # 更新使行进入/离开过滤范围
        eng.apply(ev(3, "B", row(tag="y", score=1), row(tag="y", score=9)))
        eng.apply(ev(4, "A", row(tag="x", score=5), row(tag="x", score=0)))
        self.assertEqual(self.counts(eng), {"eng": 1})

    def test_spill_to_disk_preserves_counts(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "spill.sqlite")
            eng = self.make(store_path=path, max_cached=2)
            for i in range(1, 8):
                eng.apply(ev(i, f"K{i}", None, row(tag=f"t{i}")))
            self.assertEqual(self.counts(eng), {"eng": 7})
            for i in range(1, 5):
                eng.apply(ev(10 + i, f"K{i}", row(tag=f"t{i}"), None))
            self.assertEqual(self.counts(eng), {"eng": 3})

    def test_checkpoint_restore_continues_identically(self):
        events = load_events(FIXTURE)
        cfg = dict(group_cols=["dept"], distinct_cols=["tag"],
                   filters=[("score", "ge", 3)])
        with tempfile.TemporaryDirectory() as d:
            ckpt = os.path.join(d, "state.ckpt")
            one = DistinctCountEngine(**cfg)
            one.apply_all(events)
            two = DistinctCountEngine(**cfg)
            for e in events[:5]:
                two.apply(e)
            two.checkpoint(ckpt)
            restored = DistinctCountEngine.restore(ckpt)
            restored.apply_all(events[5:])  # 含重复投递的 seq=5
            self.assertEqual(one.snapshot_counts(), restored.snapshot_counts())
            # 恢复后去重集合仍在：重放已见事件判重
            self.assertEqual(restored.apply(events[0]), "duplicate")

    def test_output_counts_alone_not_sufficient_for_recovery(self):
        # 只保存 distinct 输出计数的“伪恢复”会在后续删除时出错：
        # 同值两行删一，真状态支持计数 2->1 distinct 不变；伪状态无从得知。
        eng = self.make()
        eng.apply(ev(1, "A", None, row()))
        eng.apply(ev(2, "B", None, row()))
        with tempfile.TemporaryDirectory() as d:
            ckpt = os.path.join(d, "state.ckpt")
            eng.checkpoint(ckpt)
            restored = DistinctCountEngine.restore(ckpt)
            restored.apply(ev(3, "B", row(), None))
            self.assertEqual(self.counts(restored), {"eng": 1})  # 真恢复：仍计 1
        counts_only = {"eng": 1}  # 伪恢复只有输出计数
        naive = counts_only["eng"] - 1  # 删一条就减一的错误策略
        self.assertNotEqual(naive, self.counts(restored)["eng"])

    def test_reference_verification_shuffled_duplicated(self):
        events = load_events(FIXTURE)
        cfg = dict(group_cols=["dept"], distinct_cols=["tag"],
                   filters=[("score", "ge", 3)])
        eng = DistinctCountEngine(**cfg)
        eng.apply_all(events)
        shuffled = events + random.Random(7).sample(events, 4)
        random.Random(13).shuffle(shuffled)
        self.assertTrue(eng.verify(shuffled))
        self.assertEqual(eng.snapshot_counts(),
                         reference_counts(shuffled, **cfg))

    def test_random_replay_matches_reference(self):
        rng = random.Random(2026)
        tags = ["a", "b", None]
        depts = ["g1", "g2"]
        events, live, seq = [], {}, 0
        for _ in range(300):
            seq += 1
            if live and rng.random() < 0.4:
                pk = rng.choice(list(live))
                before = live.pop(pk)
                events.append(ev(seq, pk, before, None))
            elif live and rng.random() < 0.5:
                pk = rng.choice(list(live))
                before = live[pk]
                after = row(dept=rng.choice(depts), tag=rng.choice(tags),
                            score=rng.randint(0, 9))
                live[pk] = after
                events.append(ev(seq, pk, before, after))
            else:
                pk = f"P{seq}"
                live[pk] = row(dept=rng.choice(depts), tag=rng.choice(tags),
                               score=rng.randint(0, 9))
                events.append(ev(seq, pk, None, live[pk]))
        for null_equal in (True, False):
            for filters in (None, [("score", "ge", 3)]):
                eng = self.make(null_equal=null_equal, filters=filters)
                eng.apply_all(events)
                noisy = events + rng.sample(events, 30)
                rng.shuffle(noisy)
                self.assertTrue(eng.verify(noisy),
                                f"null_equal={null_equal} filters={filters}")


if __name__ == "__main__":
    unittest.main()
