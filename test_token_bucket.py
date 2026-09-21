"""令牌桶回放引擎测试：每次变更后与独立 oracle 对拍完整状态序列。"""

import unittest
from fractions import Fraction

from oracle import full_replay
from token_bucket import TokenBucketEngine


def oracle_of(engine):
    events = [(e.time, e.seq) for e in engine.events]
    segments = [(s.start, s.capacity, s.rate) for s in engine.config.segments]
    return full_replay(events, segments)


def assert_matches_oracle(testcase, engine):
    """比较完整状态：每个判定的时间、前后水位、放行结果，而非只看最终余量。"""
    records = engine.results()
    expected = oracle_of(engine)
    testcase.assertEqual(len(records), len(expected))
    for rec, exp in zip(records, expected):
        testcase.assertEqual(rec.time, exp.time)
        testcase.assertEqual(rec.seq, exp.seq)
        testcase.assertEqual(rec.level_before, exp.level_before)
        testcase.assertEqual(rec.allowed, exp.allowed)
        testcase.assertEqual(rec.level_after, exp.level_after)


def snapshot_by_id(engine):
    return {r.request_id: r for r in engine.results()}


def assert_identity_preserved(testcase, before, engine, rng):
    """修订范围之外的记录必须保持原对象身份与版本；范围之内必须全部换新版。"""
    after = engine.results()
    recomputed = set(rng.recomputed_ids)
    max_old_version = max((r.version for r in before.values()), default=0)
    for rec in after:
        if rec.request_id in recomputed:
            testcase.assertGreater(rec.version, max_old_version)
        else:
            testcase.assertIs(rec, before[rec.request_id])


class TestBasicBurst(unittest.TestCase):
    def test_capacity_two_burst_at_t0(self):
        engine = TokenBucketEngine([(0, 2, 1)])
        for seq in range(3):
            engine.add_request(0, seq)
        records = engine.results()
        self.assertEqual([r.allowed for r in records], [True, True, False])
        self.assertEqual([r.level_before for r in records], [2, 1, 0])
        self.assertEqual([r.level_after for r in records], [1, 0, 0])
        self.assertIn("< 1", records[2].reason)
        self.assertEqual([r.version for r in records], [1, 2, 3])
        assert_matches_oracle(self, engine)


class TestLateInsertRevision(unittest.TestCase):
    def test_insert_earlier_request_recomputes_only_affected(self):
        engine = TokenBucketEngine([(0, 2, 1)])
        engine.add_request(0, 1)
        engine.add_request(0, 2)
        engine.add_request(0, 3)
        engine.add_request(10, 0)
        before = snapshot_by_id(engine)

        _, rng = engine.add_request(0, 0)

        # 新请求挤占一个令牌：原 (0,1)(0,2) 重算，(0,3) 起状态收敛提前停止
        self.assertEqual((rng.start, rng.end), (0, 2))
        self.assertTrue(rng.stopped_early)
        assert_identity_preserved(self, before, engine, rng)
        records = engine.results()
        self.assertEqual(
            [r.allowed for r in records], [True, True, False, False, True]
        )
        assert_matches_oracle(self, engine)

    def test_insert_rejected_request_converges_immediately(self):
        engine = TokenBucketEngine([(0, 1, 1)])
        engine.add_request(0, 0)  # 通过，桶空
        engine.add_request(5, 0)  # 补满后通过
        before = snapshot_by_id(engine)

        _, rng = engine.add_request(0, 1)  # 桶空被拒，不改变水位

        self.assertEqual((rng.start, rng.end), (1, 1))
        assert_identity_preserved(self, before, engine, rng)
        assert_matches_oracle(self, engine)


class TestFractionalRefill(unittest.TestCase):
    def test_fractional_rate_exact(self):
        engine = TokenBucketEngine([(0, 100, Fraction(1, 3))])
        engine.add_request(0, 0)  # 100 -> 99
        engine.add_request(3, 0)  # 99 + 3*(1/3) = 100 整，无漂移
        records = engine.results()
        self.assertEqual(records[1].level_before, Fraction(100))
        self.assertIsInstance(records[1].level_before, Fraction)

    def test_long_fractional_accumulation_no_drift(self):
        engine = TokenBucketEngine([(0, Fraction(7, 2), Fraction(1, 7))])
        engine.add_request(0, 0)  # 7/2 -> 5/2
        engine.add_request(7000, 0)  # 5/2 + 7000/7 = 5/2 + 1000，封顶 7/2
        records = engine.results()
        self.assertEqual(records[1].level_before, Fraction(7, 2))
        assert_matches_oracle(self, engine)


class TestCapacityShrink(unittest.TestCase):
    def test_shrink_clamps_at_boundary(self):
        engine = TokenBucketEngine([(0, 10, 1)])
        engine.add_request(0, 0)  # 10 -> 9
        engine.add_request(6, 0)  # 9+6=15 -> 封顶 10 -> 9
        before = snapshot_by_id(engine)

        rng = engine.replace_config([(0, 10, 1), (5, 2, 1)])

        # 差异从 t=5 开始，首个 >=5 的事件在下标 1；t=6 处水位被截到 2
        self.assertEqual((rng.start, rng.end), (1, 1))
        assert_identity_preserved(self, before, engine, rng)
        records = engine.results()
        self.assertEqual(records[1].level_before, 2)
        self.assertEqual(records[1].level_after, 1)
        assert_matches_oracle(self, engine)

    def test_shrink_via_config_update_with_later_events(self):
        engine = TokenBucketEngine([(0, 10, 1)])
        engine.add_request(0, 0)
        engine.add_request(20, 0)
        engine.replace_config([(0, 10, 1), (10, 2, 1)])
        records = engine.results()
        # t=20：t=10 截断到 2，再补 10 -> 仍封顶 2
        self.assertEqual(records[1].level_before, 2)
        assert_matches_oracle(self, engine)


class TestIdleCap(unittest.TestCase):
    def test_long_idle_caps_at_capacity(self):
        engine = TokenBucketEngine([(0, 3, 1)])
        engine.add_request(0, 0)  # 3 -> 2
        engine.add_request(1000, 0)
        records = engine.results()
        self.assertEqual(records[1].level_before, 3)  # 封顶，不是 1002
        assert_matches_oracle(self, engine)


class TestConfigBoundarySameTime(unittest.TestCase):
    def test_requests_exactly_at_segment_boundary(self):
        engine = TokenBucketEngine([(0, 5, 1), (100, 1, 2)])
        engine.add_request(0, 0)  # 5 -> 4
        for seq in range(3):
            engine.add_request(100, seq)
        records = engine.results()
        # t=100 属于新段：先按旧段补到 5，边界截断到 1；同刻按 seq 依次判定
        self.assertEqual([r.level_before for r in records[1:]], [1, 0, 0])
        self.assertEqual([r.allowed for r in records[1:]], [True, False, False])
        assert_matches_oracle(self, engine)


class TestConfigUpdateEarlyStop(unittest.TestCase):
    def test_finite_diff_interval_allows_early_stop(self):
        engine = TokenBucketEngine([(0, 2, 1)])
        engine.add_request(0, 0)   # 2 -> 1
        engine.add_request(30, 0)  # 1+30 封顶 2 -> 1
        engine.add_request(60, 0)  # 同上
        before = snapshot_by_id(engine)

        # 仅在 [10, 20) 内速率不同；t=30 时新旧水位都收敛到封顶值
        rng = engine.replace_config([(0, 2, 1), (10, 2, 5), (20, 2, 1)])

        self.assertTrue(rng.is_empty)  # 重算立即收敛，无任何判定受影响
        assert_identity_preserved(self, before, engine, rng)
        assert_matches_oracle(self, engine)

    def test_diff_reaching_infinity_recomputes_to_end(self):
        engine = TokenBucketEngine([(0, 2, 1)])
        engine.add_request(0, 0)
        engine.add_request(1, 0)
        engine.add_request(2, 0)
        before = snapshot_by_id(engine)

        rng = engine.replace_config([(0, 2, 1), (1, 2, 3)])

        self.assertEqual((rng.start, rng.end), (1, 2))
        self.assertFalse(rng.stopped_early)
        assert_identity_preserved(self, before, engine, rng)
        assert_matches_oracle(self, engine)


class TestOrdering(unittest.TestCase):
    def test_same_time_ordered_by_seq(self):
        engine = TokenBucketEngine([(0, 1, 0)])
        engine.add_request(0, 2)
        engine.add_request(0, 0)
        engine.add_request(0, 1)
        self.assertEqual([r.seq for r in engine.results()], [0, 1, 2])
        self.assertEqual(
            [r.allowed for r in engine.results()], [True, False, False]
        )
        assert_matches_oracle(self, engine)

    def test_duplicate_time_seq_rejected(self):
        engine = TokenBucketEngine([(0, 1, 1)])
        engine.add_request(0, 0)
        with self.assertRaises(ValueError):
            engine.add_request(0, 0)


if __name__ == "__main__":
    unittest.main()
