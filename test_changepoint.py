import random
import unittest
from fractions import Fraction

from changepoint import (
    ChangePointIndex, ChangePointConsumer, full_recompute,
)


def make_index(window=3, min_samples=3, threshold=3):
    return ChangePointIndex(window, min_samples, Fraction(threshold))


def feed_consts(idx, left_val, n_left, right_val, n_right, source='s'):
    """依次喂入 n_left 个 left_val、n_right 个 right_val，返回键列表。"""
    keys = []
    for i in range(n_left):
        idx.add(source, i, left_val)
        keys.append((source, i))
    for i in range(n_left, n_left + n_right):
        idx.add(source, i, right_val)
        keys.append((source, i))
    return keys


def assert_matches_oracle(testcase, idx, events, window, min_samples, threshold):
    oracle = full_recompute(events, window, min_samples, threshold)
    cps = idx.change_points()
    testcase.assertEqual(set(cps), set(oracle),
                         'change-point boundary set mismatch vs oracle')
    for boundary, ev in cps.items():
        o = oracle[boundary]
        testcase.assertEqual(ev['index'], o['index'])
        testcase.assertEqual(ev['left'], o['left'])
        testcase.assertEqual(ev['right'], o['right'])
        testcase.assertEqual(ev['left_mean'], o['left_mean'])
        testcase.assertEqual(ev['right_mean'], o['right_mean'])
        testcase.assertEqual(ev['diff'], o['diff'])
    return oracle


class PlateauTest(unittest.TestCase):
    def test_constants_1_and_5_threshold_3(self):
        # 左常量 1、右常量 5、阈值 3：必须找到分界（下标 20 处 diff=4）。
        idx = make_index(window=5, min_samples=5, threshold=3)
        keys = feed_consts(idx, 1, 20, 5, 20)
        cps = idx.change_points()
        indices = sorted(ev['index'] for ev in cps.values())
        self.assertEqual(indices, [19, 20, 21])  # 分界附近的检出簇
        center = next(ev for ev in cps.values() if ev['index'] == 20)
        self.assertEqual(center['boundary'], (keys[19], keys[20]))
        self.assertEqual(center['left_mean'], Fraction(1))
        self.assertEqual(center['right_mean'], Fraction(5))
        self.assertEqual(center['diff'], Fraction(4))
        # 左右证据集合确实来自分界两侧的样本
        self.assertEqual(set(center['left']), set(keys[15:20]))
        self.assertEqual(set(center['right']), set(keys[20:25]))


class ThresholdTest(unittest.TestCase):
    def test_diff_exactly_equal_to_threshold_passes(self):
        # 均值差恰等于阈值 3，也算达标。
        idx = make_index(window=3, min_samples=3, threshold=3)
        feed_consts(idx, 1, 3, 4, 3)
        cps = idx.change_points()
        self.assertEqual(len(cps), 1)
        ev = next(iter(cps.values()))
        self.assertEqual(ev['index'], 3)
        self.assertEqual(ev['diff'], Fraction(3))

    def test_rational_exactness(self):
        # 有理数阈值：1/3 恰达标；差 1e-9 不到则不达标（浮点难以保证的边界）。
        idx = make_index(window=3, min_samples=3, threshold=Fraction(1, 3))
        feed_consts(idx, 0, 3, Fraction(1, 3), 3)
        self.assertEqual(len(idx.change_points()), 1)

        idx2 = make_index(window=3, min_samples=3, threshold=Fraction(1, 3))
        just_below = Fraction(1, 3) - Fraction(1, 10 ** 9)
        feed_consts(idx2, 0, 3, just_below, 3)
        self.assertEqual(idx2.change_points(), {})


class MinSamplesTest(unittest.TestCase):
    def test_just_enough_samples(self):
        # 两侧各 min_samples=3 个样本，刚够：应检出。
        idx = make_index(window=3, min_samples=3, threshold=3)
        feed_consts(idx, 0, 3, 4, 3)
        self.assertEqual(len(idx.change_points()), 1)

    def test_one_sample_short(self):
        # 右侧只有 2 个样本，差一个：任何候选点都不达标。
        idx = make_index(window=3, min_samples=3, threshold=3)
        feed_consts(idx, 0, 3, 4, 2)
        self.assertEqual(idx.change_points(), {})
        # 左侧差一个同理。
        idx2 = make_index(window=3, min_samples=3, threshold=3)
        feed_consts(idx2, 0, 2, 4, 3)
        self.assertEqual(idx2.change_points(), {})


class RetractTest(unittest.TestCase):
    def test_retract_near_candidate_loses_support(self):
        # 候选点附近撤回采样，样本数不足 -> 变化点失去支持。
        idx = make_index(window=3, min_samples=3, threshold=3)
        keys = feed_consts(idx, 0, 3, 6, 3)
        consumer = ChangePointConsumer()
        cps = idx.change_points()
        boundary = next(iter(cps))
        consumer.apply([_delta('add', boundary, cps[boundary])])
        self.assertEqual(len(consumer.active), 1)

        deltas = idx.retract(*keys[3])  # 撤回右侧窗口第一个样本
        self.assertEqual(idx.change_points(), {})
        kinds = [(d.kind, d.boundary) for d in deltas]
        self.assertIn(('retract', boundary), kinds)
        consumer.apply(deltas)
        # 消费方必须能明确撤掉旧结果。
        self.assertEqual(consumer.active, {})

    def test_correction_of_history_loses_support(self):
        # 更正一条历史记录使均值差跌破阈值 -> 旧结果被撤销。
        idx = make_index(window=3, min_samples=3, threshold=3)
        keys = feed_consts(idx, 0, 3, 3, 3)
        self.assertEqual(len(idx.change_points()), 1)
        deltas = idx.correct(*keys[3], 0)  # 右侧均值 3 -> 2，diff 2 < 3
        self.assertEqual(idx.change_points(), {})
        self.assertEqual([d.kind for d in deltas], ['retract'])

    def test_retract_unknown_event_is_noop(self):
        idx = make_index()
        self.assertEqual(idx.retract('nobody', 1), [])


class DuplicateTest(unittest.TestCase):
    def test_duplicate_event_adds_no_sample(self):
        idx = make_index()
        idx.add('s', 1, 5)
        touched_before = idx.touched
        deltas = idx.add('s', 1, 5)  # 完全重复
        self.assertEqual(deltas, [])
        self.assertEqual(len(idx), 1)
        self.assertEqual(idx.touched, touched_before)

    def test_same_key_new_value_is_correction(self):
        idx = make_index(window=3, min_samples=3, threshold=3)
        keys = feed_consts(idx, 0, 3, 3, 3)
        self.assertEqual(len(idx.change_points()), 1)
        idx.add(*keys[3], 0)  # 同标识不同值 -> 更正而非新增
        self.assertEqual(len(idx), 6)
        self.assertEqual(idx.change_points(), {})


class BatchTest(unittest.TestCase):
    def test_batched_input_matches_sequential_and_oracle(self):
        ops = (
            [('add', 'a', i, 1) for i in range(10)]
            + [('add', 'a', 10 + i, 5) for i in range(10)]
            + [('correct', 'a', 3, 9), ('retract', 'a', 12),
               ('add', 'b', 0, Fraction(7, 2))]
        )
        batched = make_index(window=4, min_samples=2, threshold=3)
        deltas = batched.apply_batch(ops)
        sequential = make_index(window=4, min_samples=2, threshold=3)
        for op in ops:
            getattr(sequential, op[0])(*op[1:])
        self.assertEqual(batched.change_points(), sequential.change_points())
        # 镜像事件流：('a',3) 被更正为 9，('a',12) 被撤回，('b',0) 追加
        events = ([(('a', i), 1 if i != 3 else 9) for i in range(10)]
                  + [(('a', 10 + i), 5) for i in range(10) if 10 + i != 12]
                  + [(('b', 0), Fraction(7, 2))])
        assert_matches_oracle(self, batched, events, 4, 2, 3)
        self.assertTrue(any(d.kind == 'add' for d in deltas))


class LocalizedRecomputeTest(unittest.TestCase):
    def test_correction_touches_only_window_sized_region(self):
        n, window = 1000, 6
        idx = make_index(window=window, min_samples=3, threshold=3)
        rng = random.Random(1)
        vals = [rng.choice([0, 1, 5, 8]) for _ in range(n)]
        for i, v in enumerate(vals):
            idx.add('s', i, v)
        idx.touched = 0
        idx.correct('s', 500, 4)  # 更正中部一条历史记录
        # 只重算受影响候选区域：<= 2*window 个候选点，而非全量 n+1 个。
        self.assertLessEqual(idx.touched, 2 * window)
        self.assertLess(idx.touched, n + 1)
        vals[500] = 4
        events = [(('s', i), v) for i, v in enumerate(vals)]
        assert_matches_oracle(self, idx, events, window, 3, 3)


class FuzzTest(unittest.TestCase):
    def test_random_ops_against_oracle(self):
        rng = random.Random(20260921)
        window, min_samples, threshold = 4, 2, Fraction(2)
        idx = ChangePointIndex(window, min_samples, threshold)
        consumer = ChangePointConsumer()
        order, values = [], {}
        seq = 0
        choices = [0, 1, 5, Fraction(1, 3), Fraction(7, 2), -2]

        def mirror(op):
            nonlocal seq
            kind = op[0]
            if kind == 'add':
                key = (op[1], op[2])
                if key not in values:
                    order.append(key)
                values[key] = Fraction(op[3])
            elif kind == 'correct':
                values[(op[1], op[2])] = Fraction(op[3])
            elif kind == 'retract':
                key = (op[1], op[2])
                if key in values:
                    del values[key]
                    order.remove(key)

        for _ in range(400):
            ops = []
            for _ in range(rng.randint(1, 6)):
                r = rng.random()
                if r < 0.45 or not order:
                    op = ('add', 'src', seq, rng.choice(choices))
                    seq += 1
                elif r < 0.6:  # 重复事件
                    key = rng.choice(order)
                    op = ('add', key[0], key[1], values[key])
                elif r < 0.8:  # 更正历史记录
                    key = rng.choice(order)
                    op = ('correct', key[0], key[1], rng.choice(choices))
                else:  # 撤回
                    key = rng.choice(order)
                    op = ('retract', key[0], key[1])
                mirror(op)  # 边生成边推进镜像，保证批内操作合法
                ops.append(op)
            deltas = idx.apply_batch(ops)
            consumer.apply(deltas)
            events = [(k, values[k]) for k in order]
            oracle = assert_matches_oracle(
                self, idx, events, window, min_samples, threshold)
            # 消费方状态与 oracle 一致：失去支持的旧结果已被明确撤掉。
            self.assertEqual(set(consumer.active), set(oracle))
            for boundary, (i, ev) in consumer.active.items():
                o = oracle[boundary]
                self.assertEqual(ev['left'], o['left'])
                self.assertEqual(ev['right'], o['right'])
                self.assertEqual(ev['diff'], o['diff'])


def _delta(kind, boundary, evidence):
    from changepoint import ChangeDelta
    return ChangeDelta(kind, boundary, evidence.get('index'), evidence)


if __name__ == '__main__':
    unittest.main()
