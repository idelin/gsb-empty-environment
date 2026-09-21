"""随机对拍：增量索引 vs 独立重放模型 + 逐端点扫描基准。"""
import random
import unittest

from occupancy_peak import OccupancyTracker, brute_peak


class ReplayModel:
    """按规格独立重放操作日志，得到应有区间集合（不经过被测实现）。"""

    def __init__(self):
        self.recs = {}  # oid -> [start, end]

    def record_start(self, oid, t):
        r = self.recs.get(oid)
        if r is None:
            self.recs[oid] = [t, None]
        elif r[0] != t and not (r[1] is not None and r[1] <= t):
            r[0] = t

    def record_end(self, oid, t):
        r = self.recs.get(oid)
        if r is None:
            self.recs[oid] = [None, t]
        elif r[1] != t and not (r[0] is not None and t <= r[0]):
            r[1] = t

    def retract(self, oid):
        self.recs.pop(oid, None)

    def intervals(self):
        return {
            o: (s, e)
            for o, (s, e) in self.recs.items()
            if s is not None and e is not None
        }


class FuzzTest(unittest.TestCase):
    def test_against_brute_force(self):
        rng = random.Random(20260921)
        for _ in range(200):
            tracker = OccupancyTracker()
            model = ReplayModel()
            ids = [f"occ{i}" for i in range(6)]
            for _ in range(rng.randint(1, 50)):
                oid = rng.choice(ids)
                t = rng.randint(0, 30)
                op = rng.random()
                if op < 0.4:
                    tracker.record_start(oid, t)
                    model.record_start(oid, t)
                elif op < 0.8:
                    tracker.record_end(oid, t)
                    model.record_end(oid, t)
                else:
                    tracker.retract(oid)
                    model.retract(oid)
            # 1) 配对 / 更正 / 撤回后的区间集合与独立模型一致
            expected = model.intervals()
            self.assertEqual(tracker.intervals(), expected)
            # 2) 峰值查询与逐端点扫描基准一致（峰值、时段、贡献 ID）
            for _ in range(20):
                a = rng.randint(0, 30)
                b = rng.randint(0, 30)
                if a > b:
                    a, b = b, a
                if a == b:
                    b += 1
                got = tracker.peak(a, b)
                want = brute_peak(expected, a, b)
                self.assertEqual(got, want, f"window=[{a}, {b})")


if __name__ == "__main__":
    unittest.main()
