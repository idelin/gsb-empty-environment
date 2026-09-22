"""变化点索引测试。

oracle 采用独立的全量重算：每个操作后，从全部现存事件出发，
扫描所有可行间隙，与增量索引的状态逐条核对；同时用一个消费方模型
验证 add / retract 通知最终维护出的结果与 oracle 完全一致。
"""

import random
import unittest
from collections import Counter
from fractions import Fraction

from changepoint import ChangePointIndex, ChangePoint


def brute_force(state, left_min, right_min, threshold):
    """全量重算 oracle：state 为 (key -> value) 的字典。"""
    keys = sorted(state)
    values = [state[k] for k in keys]
    n = len(keys)
    out = {}
    for j in range(n - 1):
        left_lo = j - left_min + 1
        right_hi = j + right_min
        if left_lo < 0 or right_hi > n - 1:
            continue
        left_count = j - left_lo + 1
        right_count = right_hi - j
        left_mean = sum(values[left_lo:j + 1], Fraction(0)) / left_count
        right_mean = sum(values[j + 1:right_hi + 1], Fraction(0)) / right_count
        if abs(left_mean - right_mean) >= threshold:
            gid = (keys[j], keys[j + 1])
            out[gid] = ChangePoint(
                gap=gid,
                diff=abs(left_mean - right_mean),
                left_evidence=tuple(keys[left_lo:j + 1]),
                right_evidence=tuple(keys[j + 1:right_hi + 1]),
            )
    return out


class Consumer:
    """按通知维护结果集合，模拟消费方对旧结果的显式撤销。"""

    def __init__(self):
        self.points = {}
        self.event_log = []

    def apply(self, events):
        for ev in events:
            self.event_log.append((ev.kind, ev.point.gap))
            if ev.kind == "add":
                assert ev.point.gap not in self.points, \
                    "add 了一个尚未撤销的结果: %r" % (ev.point.gap,)
                self.points[ev.point.gap] = ev.point
            elif ev.kind == "retract":
                assert ev.point.gap in self.points, \
                    "retract 了一个消费方不持有的结果: %r" % (ev.point.gap,)
                old = self.points.pop(ev.point.gap)
                assert old == ev.point, "retract 的证据与 add 时不一致"
            else:  # pragma: no cover
                raise AssertionError("未知通知类型 %r" % ev.kind)


def snapshot_map(idx):
    return {p.gap: p for p in idx.change_points()}


class ChangePointTests(unittest.TestCase):
    def assertMatchesOracle(self, idx, state, consumer):
        expected = brute_force(state, idx.left_min, idx.right_min,
                               idx.threshold)
        self.assertEqual(snapshot_map(idx), expected)
        self.assertEqual(consumer.points, expected)
        return expected

    def test_left1_right5_threshold3_finds_boundary(self):
        idx = ChangePointIndex(1, 5, 3)
        consumer = Consumer()
        state = {}
        values = [Fraction(3)] * 6 + [Fraction(0)] * 6
        for seq, value in enumerate(values):
            consumer.apply(idx.upsert("a", seq, value))
            state[("a", seq)] = value
        points = self.assertMatchesOracle(idx, state, consumer)
        self.assertEqual(set(points), {(("a", 5), ("a", 6))})
        point = points[(("a", 5), ("a", 6))]
        self.assertEqual(point.diff, Fraction(3))
        self.assertEqual(len(point.left_evidence), 1)
        self.assertEqual(len(point.right_evidence), 5)
        self.assertEqual(point.left_evidence, (("a", 5),))
        self.assertEqual(point.right_evidence,
                         tuple(("a", seq) for seq in range(6, 11)))

    def test_diff_equal_threshold_qualifies(self):
        # L=2, R=2，左均值 1.5、右均值 0，差恰为 3/2，阈值同为 3/2。
        idx = ChangePointIndex(2, 2, Fraction(3, 2))
        consumer = Consumer()
        state = {}
        for seq, value in enumerate([0, 3, 0, 0]):
            consumer.apply(idx.upsert("a", seq, value))
            state[("a", seq)] = Fraction(value)
        points = self.assertMatchesOracle(idx, state, consumer)
        self.assertEqual(set(points), {(("a", 1), ("a", 2))})
        # 阈值再提高一点则不成立。
        idx2 = ChangePointIndex(2, 2, Fraction(3, 2) + Fraction(1, 1000))
        for seq, value in enumerate([0, 3, 0, 0]):
            idx2.upsert("a", seq, value)
        self.assertEqual(idx2.change_points(), [])

    def test_minimum_counts_and_one_short(self):
        # L=2, R=2，前四个样本刚够两侧窗口。
        idx = ChangePointIndex(2, 2, 1)
        consumer = Consumer()
        state = {}
        for seq, value in enumerate([9, 9, 0, 0]):
            consumer.apply(idx.upsert("a", seq, value))
            state[("a", seq)] = Fraction(value)
        points = self.assertMatchesOracle(idx, state, consumer)
        self.assertEqual(set(points), {(("a", 1), ("a", 2))})

        # 撤回一条右证据后右窗口差一个样本，变化点必须被显式撤销。
        events = idx.withdraw("a", 2)
        consumer.apply(events)
        del state[("a", 2)]
        self.assertMatchesOracle(idx, state, consumer)
        kinds = [e.kind for e in events]
        self.assertIn("retract", kinds)
        self.assertFalse(any(e.kind == "add" for e in events))

        # 重新补回该样本，变化点以 add 形式恢复。
        consumer.apply(idx.upsert("a", 2, 0))
        state[("a", 2)] = Fraction(0)
        self.assertMatchesOracle(idx, state, consumer)

    def test_withdraw_near_candidate_retracts_old_result(self):
        # 12 个样本，L=1, R=5，唯一变化点在 5|6。
        idx = ChangePointIndex(1, 5, 3)
        consumer = Consumer()
        state = {}
        values = [3] * 6 + [0] * 6
        for seq, value in enumerate(values):
            consumer.apply(idx.upsert("a", seq, value))
            state[("a", seq)] = Fraction(value)
        gid = (("a", 5), ("a", 6))
        self.assertIn(gid, consumer.points)

        # 在变化点紧邻位置撤回右证据，右窗口一度不足 5 个样本。
        consumer.apply(idx.withdraw("a", 6))
        del state[("a", 6)]
        self.assertMatchesOracle(idx, state, consumer)
        self.assertNotIn(gid, consumer.points)
        self.assertNotIn(gid, snapshot_map(idx))

        # 以高值补回同一位置后，旧结果 5|6 不得恢复；
        # 边界右移到 6|7，并以一个全新的 add 通知出现。
        events = idx.upsert("a", 6, 3)
        consumer.apply(events)
        state[("a", 6)] = Fraction(3)
        self.assertMatchesOracle(idx, state, consumer)
        self.assertNotIn(gid, consumer.points)
        self.assertIn((("a", 6), ("a", 7)), consumer.points)
        kinds = [(e.kind, e.point.gap) for e in events]
        self.assertNotIn(("add", gid), kinds)

    def test_duplicate_event_does_not_increase_sample_count(self):
        idx = ChangePointIndex(1, 2, 1)
        consumer = Consumer()
        state = {}
        consumer.apply(idx.upsert("a", 0, 9))
        state[("a", 0)] = Fraction(9)
        consumer.apply(idx.upsert("a", 1, 9))
        state[("a", 1)] = Fraction(9)
        consumer.apply(idx.upsert("a", 2, 0))
        state[("a", 2)] = Fraction(0)
        self.assertMatchesOracle(idx, state, consumer)
        self.assertEqual(len(idx), 3)

        # 同 (source, seq) 重复提交同值：空操作、不触及任何候选点。
        events = idx.upsert("a", 1, 9)
        self.assertEqual(events, [])
        self.assertEqual(idx.last_touched, 0)
        self.assertEqual(len(idx), 3)

        # 重复提交不同值属于更正，样本数不变但结果重算。
        consumer.apply(idx.upsert("a", 1, 0))
        state[("a", 1)] = Fraction(0)
        self.assertMatchesOracle(idx, state, consumer)

    def test_correct_history_only_touches_local_region(self):
        idx = ChangePointIndex(3, 3, 3)
        consumer = Consumer()
        state = {}
        for seq in range(30):
            value = 10 if seq < 15 else 0
            consumer.apply(idx.upsert("a", seq, value))
            state[("a", seq)] = Fraction(value)
        before = snapshot_map(idx)

        # 更正一条远离所有变化点的历史值，触及 id 必须局限在其位置附近。
        pos = 1
        consumer.apply(idx.upsert("a", pos, 10))
        state[("a", pos)] = Fraction(10)
        touched = idx.last_touched_ids
        affected_seqs = set()
        for left, right in touched:
            affected_seqs.add(left[1])
            affected_seqs.add(right[1])
        self.assertTrue(all(abs(int(seq) - pos) <= 3 for seq in affected_seqs))
        self.assertLessEqual(len(touched), idx.left_min + idx.right_min)
        self.assertMatchesOracle(idx, state, consumer)
        self.assertEqual(snapshot_map(idx), before)

    def test_batched_input_matches_oracle(self):
        idx = ChangePointIndex(2, 3, Fraction(5, 2))
        consumer = Consumer()
        state = {}
        ops = []
        for seq in range(40):
            value = 8 if seq < 20 else 0
            ops.append(("upsert", "a", seq, value))
            state[("a", seq)] = Fraction(value)
        events = idx.apply(ops)
        consumer.apply(events)
        self.assertMatchesOracle(idx, state, consumer)

        # 混合批次：插入分数序号、更正、撤回。
        more = [
            ("upsert", "a", Fraction(1, 2), 8),
            ("upsert", "a", 20, 8),       # 更正
            ("withdraw", "a", 25),
            ("upsert", "b", 10, 2),       # 不同 source，排在 a 之前
            ("upsert", "a", Fraction(1, 2), 0),  # 更正分数序号
        ]
        state[("a", Fraction(1, 2))] = Fraction(0)
        state[("a", 20)] = Fraction(8)
        del state[("a", 25)]
        state[("b", 10)] = Fraction(2)
        consumer.apply(idx.apply(more))
        self.assertMatchesOracle(idx, state, consumer)

    def test_random_operations_against_oracle(self):
        rng = random.Random(20260922)
        for seed in range(6):
            rng.seed(1000 + seed)
            left_min = rng.randint(1, 4)
            right_min = rng.randint(1, 4)
            threshold = Fraction(rng.randint(1, 5))
            idx = ChangePointIndex(left_min, right_min, threshold)
            consumer = Consumer()
            state = {}

            bound = 2 * (left_min + right_min) + 12
            total_events = Counter()
            for step in range(400):
                action = rng.random()
                source = "s%d" % rng.randint(0, 2)
                seq = rng.randint(0, bound)
                if action < 0.75:
                    value = rng.choice([0, 1, 2, 8, 9, 10])
                    events = idx.upsert(source, seq, value)
                    state[(source, seq)] = Fraction(value)
                else:
                    events = idx.withdraw(source, seq)
                    state.pop((source, seq), None)
                consumer.apply(events)
                for ev in events:
                    total_events[(ev.kind, ev.point.gap)] += 1
                if step % 13 == 0:
                    self.assertMatchesOracle(idx, state, consumer)
            self.assertMatchesOracle(idx, state, consumer)
            self.assertEqual(consumer.points,
                             brute_force(state, left_min, right_min, threshold))

    def test_touched_count_bounded_by_window(self):
        # 用 oracle 的间隙总数对比：全量重算线性增长，单次增量恒定。
        for n in (50, 200, 800):
            idx = ChangePointIndex(4, 4, Fraction(10**6))
            for seq in range(n):
                idx.upsert("a", seq, seq % 5)
            pos = n // 2
            idx.upsert("a", pos, 100)
            self.assertLessEqual(idx.last_touched,
                                 idx.left_min + idx.right_min)
            idx.upsert("a", Fraction(pos) + Fraction(1, 2), 100)
            self.assertLessEqual(idx.last_touched,
                                 2 * (idx.left_min + idx.right_min))


if __name__ == "__main__":
    unittest.main(verbosity=2)
