import os
import pickle
import random
import shutil
import tempfile
import unittest
from collections import defaultdict

from gdc import (
    ChangeEvent,
    DistinctConfig,
    GroupedDistinctCounter,
    MissingEventError,
    NULL,
    NullPolicy,
    ReferenceModel,
)


def ev(event_id, op, pk, before=None, after=None, seq=0, table="users", tx="tx"):
    return ChangeEvent(
        event_id=event_id, tx_id=tx, table=table, pk=(pk,),
        op=op, before=before, after=after, commit_seq=seq,
    )


def make_config(**kw):
    base = dict(table="users", group_cols=("country",), value_cols=("email",))
    base.update(kw)
    return DistinctConfig(**base)


class TestSupportCounts(unittest.TestCase):
    def setUp(self):
        self.counter = GroupedDistinctCounter(make_config())

    def test_same_value_two_rows_delete_one_still_counts_one(self):
        row1 = {"country": "CN", "email": "a@x"}
        row2 = {"country": "CN", "email": "a@x"}
        self.counter.apply(ev("e1", "insert", 1, after=row1))
        self.counter.apply(ev("e2", "insert", 2, after=row2))
        # 同值两行：distinct 1，支持计数 2
        self.assertEqual(self.counter.distinct_counts(), {("CN",): 1})
        self.assertEqual(self.counter.snapshot()[("CN",)][("a@x",)], 2)
        # 删其一：仍计 1
        self.counter.apply(ev("e3", "delete", 2, before=row2))
        self.assertEqual(self.counter.distinct_counts(), {("CN",): 1})
        self.assertEqual(self.counter.snapshot()[("CN",)][("a@x",)], 1)

    def test_delete_last_supporter_decrements_distinct(self):
        row = {"country": "CN", "email": "a@x"}
        self.counter.apply(ev("e1", "insert", 1, after=row))
        self.counter.apply(ev("e2", "delete", 1, before=row))
        # 删除最后一条：distinct 减一，组消失
        self.assertEqual(self.counter.distinct_counts(), {})
        self.assertEqual(self.counter.snapshot(), {})

    def test_negative_count_rejected(self):
        with self.assertRaises(MissingEventError):
            self.counter.apply(ev("e1", "delete", 1,
                                  before={"country": "CN", "email": "ghost@x"}))
        # 状态未被污染
        self.assertEqual(self.counter.snapshot(), {})

    def test_duplicate_event_deduped(self):
        row = {"country": "CN", "email": "a@x"}
        event = ev("e1", "insert", 1, after=row)
        self.assertTrue(self.counter.apply(event))
        self.assertFalse(self.counter.apply(event))  # 重复投递跳过
        self.assertEqual(self.counter.snapshot()[("CN",)][("a@x",)], 1)
        self.assertEqual(self.counter.stats["dedup_skipped"], 1)

    def test_other_table_ignored(self):
        self.counter.apply(ev("e1", "insert", 1, table="orders",
                              after={"country": "CN", "email": "a@x"}))
        self.assertEqual(self.counter.snapshot(), {})


class TestNullPolicy(unittest.TestCase):
    def test_null_counted_as_value_by_default(self):
        counter = GroupedDistinctCounter(make_config())
        counter.apply(ev("e1", "insert", 1, after={"country": "CN", "email": None}))
        self.assertEqual(counter.snapshot(), {("CN",): {(NULL,): 1}})

    def test_null_policy_ignore(self):
        counter = GroupedDistinctCounter(
            make_config(null_policy=NullPolicy.IGNORE))
        counter.apply(ev("e1", "insert", 1, after={"country": "CN", "email": None}))
        self.assertEqual(counter.snapshot(), {})
        # 对应删除同样被忽略，不误减
        counter.apply(ev("e2", "delete", 1, before={"country": "CN", "email": None}))
        self.assertEqual(counter.snapshot(), {})
        self.assertEqual(counter.stats["filtered"], 2)

    def test_null_equal_false_each_occurrence_distinct(self):
        counter = GroupedDistinctCounter(make_config(null_equal=False))
        counter.apply(ev("e1", "insert", 1, after={"country": "CN", "email": None}))
        counter.apply(ev("e2", "insert", 2, after={"country": "CN", "email": None}))
        # null != null：两个 null 出现是 2 个 distinct 值
        self.assertEqual(counter.distinct_counts(), {("CN",): 2})
        counter.apply(ev("e3", "delete", 1, before={"country": "CN", "email": None}))
        self.assertEqual(counter.distinct_counts(), {("CN",): 1})

    def test_null_equal_true_nulls_merge(self):
        counter = GroupedDistinctCounter(make_config(null_equal=True))
        counter.apply(ev("e1", "insert", 1, after={"country": "CN", "email": None}))
        counter.apply(ev("e2", "insert", 2, after={"country": "CN", "email": None}))
        self.assertEqual(counter.distinct_counts(), {("CN",): 1})
        self.assertEqual(counter.snapshot()[("CN",)][(NULL,)], 2)


class TestCompositeAndFilter(unittest.TestCase):
    def test_multi_column_composite_distinct(self):
        counter = GroupedDistinctCounter(
            make_config(value_cols=("city", "tag")))
        counter.apply(ev("e1", "insert", 1, after={"country": "CN", "city": "BJ", "tag": "x"}))
        counter.apply(ev("e2", "insert", 2, after={"country": "CN", "city": "BJ", "tag": "y"}))
        counter.apply(ev("e3", "insert", 3, after={"country": "CN", "city": "BJ", "tag": "x"}))
        # 组合 (BJ,x) 与 (BJ,y) 两个 distinct 值
        self.assertEqual(counter.distinct_counts(), {("CN",): 2})
        self.assertEqual(counter.snapshot()[("CN",)][("BJ", "x")], 2)

    def test_row_filter(self):
        counter = GroupedDistinctCounter(
            make_config(row_filter=lambda r: r.get("active", False)))
        counter.apply(ev("e1", "insert", 1,
                         after={"country": "CN", "email": "a@x", "active": False}))
        counter.apply(ev("e2", "insert", 2,
                         after={"country": "CN", "email": "a@x", "active": True}))
        self.assertEqual(counter.snapshot(), {("CN",): {("a@x",): 1}})
        # 被过滤行的删除不产生任何影响
        counter.apply(ev("e3", "delete", 1,
                         before={"country": "CN", "email": "a@x", "active": False}))
        self.assertEqual(counter.snapshot(), {("CN",): {("a@x",): 1}})


class TestGroupMigration(unittest.TestCase):
    def test_cross_group_update(self):
        counter = GroupedDistinctCounter(make_config())
        counter.apply(ev("e1", "insert", 1, after={"country": "CN", "email": "a@x"}))
        counter.apply(ev("e2", "update", 1,
                         before={"country": "CN", "email": "a@x"},
                         after={"country": "US", "email": "a@x"}))
        # 旧组撤销、新组加入
        self.assertEqual(counter.snapshot(), {("US",): {("a@x",): 1}})

    def test_update_same_contribution_is_noop(self):
        counter = GroupedDistinctCounter(make_config())
        counter.apply(ev("e1", "insert", 1, after={"country": "CN", "email": "a@x"}))
        counter.apply(ev("e2", "update", 1,
                         before={"country": "CN", "email": "a@x"},
                         after={"country": "CN", "email": "a@x"}))
        self.assertEqual(counter.snapshot(), {("CN",): {("a@x",): 1}})

    def test_migration_rolls_back_when_add_fails(self):
        counter = GroupedDistinctCounter(make_config())
        counter.apply(ev("e1", "insert", 1, after={"country": "CN", "email": "a@x"}))
        original_increment = counter._increment

        def boom(group_key, value_key):
            if group_key == ("US",):
                raise RuntimeError("sink failure")
            return original_increment(group_key, value_key)

        counter._increment = boom
        with self.assertRaises(RuntimeError):
            counter.apply(ev("e2", "update", 1,
                             before={"country": "CN", "email": "a@x"},
                             after={"country": "US", "email": "a@x"}))
        # 原子性：旧贡献已回滚，新贡献未残留
        self.assertEqual(counter.snapshot(), {("CN",): {("a@x",): 1}})

    def test_migration_rolls_back_when_remove_fails(self):
        counter = GroupedDistinctCounter(make_config())
        # before 行从未插入 -> 撤销旧贡献时缺事件，整体拒绝
        with self.assertRaises(MissingEventError):
            counter.apply(ev("e1", "update", 1,
                             before={"country": "CN", "email": "ghost@x"},
                             after={"country": "US", "email": "a@x"}))
        self.assertEqual(counter.snapshot(), {})


class TestSpillAndCheckpoint(unittest.TestCase):
    def setUp(self):
        self.tmpdir = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.tmpdir, ignore_errors=True)

    def test_spill_roundtrip(self):
        spill_dir = os.path.join(self.tmpdir, "spill")
        counter = GroupedDistinctCounter(
            make_config(spill_threshold=3), spill_dir=spill_dir)
        for i in range(5):
            counter.apply(ev(f"e{i}", "insert", i,
                             after={"country": "CN", "email": f"u{i}@x"}))
        # 超过阈值，组状态已溢写
        self.assertTrue(os.listdir(spill_dir))
        self.assertEqual(counter.distinct_counts(), {("CN",): 5})
        # 溢写组可正常删除（按需载入）
        counter.apply(ev("d0", "delete", 0, before={"country": "CN", "email": "u0@x"}))
        self.assertEqual(counter.distinct_counts(), {("CN",): 4})

    def test_checkpoint_preserves_support_counts_and_dedup(self):
        counter = GroupedDistinctCounter(make_config())
        row1 = {"country": "CN", "email": "a@x"}
        row2 = {"country": "CN", "email": "a@x"}
        counter.apply(ev("e1", "insert", 1, after=row1))
        counter.apply(ev("e2", "insert", 2, after=row2))
        path = os.path.join(self.tmpdir, "ckpt.pkl")
        counter.checkpoint(path)

        # checkpoint 里保存的是支持计数 2，而不是输出 distinct 数 1
        with open(path, "rb") as fh:
            raw = pickle.load(fh)
        self.assertEqual(raw["states"][("CN",)][("a@x",)], 2)
        self.assertEqual(set(raw["processed"]), {"e1", "e2"})

        restored = GroupedDistinctCounter.restore(path, make_config())
        # 去重集合随 checkpoint 恢复：重放旧事件被跳过
        self.assertFalse(restored.apply(ev("e1", "insert", 1, after=row1)))
        # 恢复后继续删一条：支持计数 2 -> 1，distinct 仍为 1
        restored.apply(ev("e3", "delete", 2, before=row2))
        self.assertEqual(restored.distinct_counts(), {("CN",): 1})
        restored.apply(ev("e4", "delete", 1, before=row1))
        self.assertEqual(restored.distinct_counts(), {})

    def test_restore_rejects_mismatched_table(self):
        counter = GroupedDistinctCounter(make_config())
        path = os.path.join(self.tmpdir, "ckpt.pkl")
        counter.checkpoint(path)
        with self.assertRaises(ValueError):
            GroupedDistinctCounter.restore(path, make_config(table="orders"))


class TestReferenceVerification(unittest.TestCase):
    def _random_events(self, rng, n_rows=30):
        """生成随机事件流：跨主键乱序交织，但同一主键保持因果序。"""
        countries = ["CN", "US", "DE", None]
        emails = ["a@x", "b@x", "c@x", None]
        by_pk = defaultdict(list)
        seq = 0
        live = {}
        for _ in range(200):
            pk = rng.randrange(n_rows)
            row = {"country": rng.choice(countries), "email": rng.choice(emails)}
            if pk not in live:
                by_pk[pk].append(("insert", None, row))
                live[pk] = row
            else:
                if rng.random() < 0.4:
                    by_pk[pk].append(("delete", live[pk], None))
                    del live[pk]
                else:
                    by_pk[pk].append(("update", live[pk], row))
                    live[pk] = row
        # 交织打乱各主键的事件序列（保持每键内部顺序）
        events = []
        while by_pk:
            pk = rng.choice(list(by_pk))
            op, before, after = by_pk[pk].pop(0)
            seq += 1
            events.append(ev(f"ev{seq}", op, pk, before=before, after=after, seq=seq))
            if not by_pk[pk]:
                del by_pk[pk]
        return events

    def test_shuffled_and_duplicated_stream_matches_reference(self):
        rng = random.Random(42)
        events = self._random_events(rng)
        # 随机重复投递部分事件
        duplicated = []
        for e in events:
            duplicated.append(e)
            if rng.random() < 0.2:
                duplicated.append(e)
        config = make_config()
        counter = GroupedDistinctCounter(config)
        reference = ReferenceModel(config)
        for e in duplicated:
            counter.apply(e)
            reference.apply(e)
        self.assertEqual(reference.verify(counter), [])
        self.assertGreater(counter.stats["dedup_skipped"], 0)

    def test_recovery_verified_against_reference(self):
        tmpdir = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmpdir, ignore_errors=True)
        rng = random.Random(7)
        events = self._random_events(rng)
        config = make_config(spill_threshold=5)
        spill_dir = os.path.join(tmpdir, "spill")
        counter = GroupedDistinctCounter(config, spill_dir=spill_dir)
        reference = ReferenceModel(config)
        for e in events:
            counter.apply(e)
            reference.apply(e)
        path = os.path.join(tmpdir, "ckpt.pkl")
        counter.checkpoint(path)
        restored = GroupedDistinctCounter.restore(path, config, spill_dir=spill_dir)
        # 恢复后的状态必须通过独立全量参考核验
        self.assertEqual(reference.verify(restored), [])

    def test_reference_detects_output_count_only_recovery(self):
        # 只按输出 distinct 数“恢复”的状态无法通过全量参考核验
        config = make_config()
        counter = GroupedDistinctCounter(config)
        counter.apply(ev("e1", "insert", 1, after={"country": "CN", "email": "a@x"}))
        counter.apply(ev("e2", "insert", 2, after={"country": "CN", "email": "a@x"}))
        reference = ReferenceModel(config)
        for e in [ev("e1", "insert", 1, after={"country": "CN", "email": "a@x"}),
                  ev("e2", "insert", 2, after={"country": "CN", "email": "a@x"})]:
            reference.apply(e)
        fake = GroupedDistinctCounter(config)
        fake._states = {("CN",): {("a@x",): 1}}  # 只恢复了 distinct=1
        self.assertNotEqual(reference.verify(fake), [])


if __name__ == "__main__":
    unittest.main()
