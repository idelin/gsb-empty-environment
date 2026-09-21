"""TwoLevelTimeIndex 测试：以直接扫描为独立参考。"""

import random
import unittest

from two_level_index import QueryResult, TwoLevelTimeIndex


def scan_reference(events: dict, lo: int, hi: int) -> float:
    """独立参考实现：直接扫描原始事件。"""
    return sum(v for t, v in events.values() if lo <= t < hi)


class RangeQueryTest(unittest.TestCase):
    def setUp(self) -> None:
        self.index = TwoLevelTimeIndex(fine_width=5, coarse_width=20)
        self.model = {}
        times = [-7, -1, 0, 3, 4, 5, 9, 10, 14, 19, 20, 22, 23, 25, 31]
        for i, t in enumerate(times):
            value = (i + 1) * 10
            self.index.add(event_id=i, time=t, value=value)
            self.model[i] = (t, value)

    def test_range_3_23(self) -> None:
        result = self.index.query(3, 23)
        self.assertEqual(result.total, scan_reference(self.model, 3, 23))
        # [3,23)：粗桶 [0,20) 与 [20,40) 都不完整，下钻细层；
        # 细桶 [5,10) [10,15) [15,20) 完整 -> 3 个聚合节点；
        # 残窗 [3,5) 与 [20,23) 回扫原始事件：3,4,20,22 共 4 条。
        self.assertEqual(result.aggregate_nodes, 3)
        self.assertEqual(result.edge_events, 4)

    def test_boundary_event_moved_out_of_range(self) -> None:
        before = self.index.query(3, 23).total
        moved_value = self.model[3][1]  # 位于时间 3、紧贴下界的事件
        self.index.update(event_id=3, time=40, value=moved_value)
        self.model[3] = (40, moved_value)

        after = self.index.query(3, 23)
        self.assertEqual(after.total, before - moved_value)
        self.assertEqual(after.total, scan_reference(self.model, 3, 23))

        # 原始记录已修订
        self.assertEqual(self.index._events[3], (40, moved_value))
        # 两层聚合同步修订：细桶 0 减少、细桶 8 出现；粗桶 0 减少、粗桶 2 出现
        self.assertEqual(self.index._fine[0], 80)          # 仅剩时间 0、4 的事件
        self.assertEqual(self.index._fine[8], moved_value)
        self.assertEqual(self.index._coarse[0], sum(
            v for t, v in self.model.values() if 0 <= t < 20))
        self.assertEqual(self.index._coarse[2], moved_value)
        # 成员表也修订：事件 3 从细桶 0 移到细桶 8
        self.assertNotIn(3, self.index._fine_members[0])
        self.assertIn(3, self.index._fine_members[8])

    def test_retract(self) -> None:
        value = self.model[10][1]  # 时间 19 的事件
        self.index.retract(10)
        del self.model[10]
        self.assertEqual(self.index.query(0, 20).total,
                         scan_reference(self.model, 0, 20))
        self.assertNotIn(10, self.index._events)
        self.assertNotIn(10, self.index._fine_members[3])
        # 撤回后两层查询结果仍与扫描一致
        for lo, hi in [(-10, 40), (3, 23), (19, 20), (-1, 0)]:
            self.assertEqual(self.index.query(lo, hi).total,
                             scan_reference(self.model, lo, hi))
        self.assertEqual(value, 110)  #  Sanity：撤回的是预期那条

    def test_empty_range(self) -> None:
        for lo, hi in [(10, 10), (15, 10), (100, 200)]:
            result = self.index.query(lo, hi)
            self.assertEqual(result, QueryResult(0, 0, 0))
            self.assertEqual(result.total, scan_reference(self.model, lo, hi))

    def test_exact_full_windows(self) -> None:
        # 恰好一个完整粗窗 [0,20)：只读 1 个粗聚合节点，无边缘事件
        result = self.index.query(0, 20)
        self.assertEqual(result.total, scan_reference(self.model, 0, 20))
        self.assertEqual(result.aggregate_nodes, 1)
        self.assertEqual(result.edge_events, 0)
        # 恰好两个完整细窗 [5,15)
        result = self.index.query(5, 15)
        self.assertEqual(result.total, scan_reference(self.model, 5, 15))
        self.assertEqual(result.aggregate_nodes, 2)
        self.assertEqual(result.edge_events, 0)

    def test_negative_times(self) -> None:
        # 负时间按向下取整定位：-7 在细桶 -2（[-10,-5)），-1 在细桶 -1
        self.assertEqual(-7 // 5, -2)
        for lo, hi in [(-10, 0), (-8, 4), (-7, -1), (-20, 0), (-25, 35)]:
            self.assertEqual(self.index.query(lo, hi).total,
                             scan_reference(self.model, lo, hi))
        # 恰好覆盖负粗窗 [-20,0)：1 个粗聚合节点
        result = self.index.query(-20, 0)
        self.assertEqual(result.total, scan_reference(self.model, -20, 0))
        self.assertEqual(result.aggregate_nodes, 1)
        self.assertEqual(result.edge_events, 0)

    def test_coarse_window_with_empty_fine_windows(self) -> None:
        index = TwoLevelTimeIndex(fine_width=5, coarse_width=20)
        model = {}
        # 粗桶 [20,40) 内只有细桶 4（[20,25)）和细桶 7（[35,40)）有数据
        for i, t in enumerate([21, 23, 35]):
            index.add(event_id=i, time=t, value=t * 100)
            model[i] = (t, t * 100)
        # 完整粗窗：空细桶不影响粗聚合
        result = index.query(20, 40)
        self.assertEqual(result.total, scan_reference(model, 20, 40))
        self.assertEqual(result.aggregate_nodes, 1)
        self.assertEqual(result.edge_events, 0)
        # 残窗下钻：空细桶读不到事件也不出错
        for lo, hi in [(22, 40), (20, 36), (24, 34), (25, 35)]:
            self.assertEqual(index.query(lo, hi).total,
                             scan_reference(model, lo, hi))
        # 空细桶 5、6 不产生聚合条目
        self.assertNotIn(5, index._fine)
        self.assertNotIn(6, index._fine)


class RandomizedCrossCheckTest(unittest.TestCase):
    def test_random_ops_against_scan(self) -> None:
        rng = random.Random(20260921)
        index = TwoLevelTimeIndex(fine_width=5, coarse_width=20)
        model = {}
        live_ids = []
        next_id = 0
        for _ in range(3000):
            action = rng.random()
            if action < 0.45 or not live_ids:
                event_id = next_id
                next_id += 1
                time = rng.randrange(-200, 200)
                value = rng.randrange(1, 1000)
                index.add(event_id, time, value)
                model[event_id] = (time, value)
                live_ids.append(event_id)
            elif action < 0.75:
                event_id = rng.choice(live_ids)
                time = rng.randrange(-200, 200)
                value = rng.randrange(1, 1000)
                index.update(event_id, time, value)
                model[event_id] = (time, value)
            else:
                event_id = rng.choice(live_ids)
                index.retract(event_id)
                del model[event_id]
                live_ids.remove(event_id)
        for _ in range(500):
            lo = rng.randrange(-220, 220)
            hi = rng.randrange(-220, 220)
            if lo > hi:
                lo, hi = hi, lo
            self.assertEqual(index.query(lo, hi).total,
                             scan_reference(model, lo, hi))


class ManyBucketsStatsTest(unittest.TestCase):
    def test_spanning_many_buckets_reports_stats(self) -> None:
        index = TwoLevelTimeIndex(fine_width=5, coarse_width=20)
        model = {}
        for t in range(1000):
            index.add(event_id=t, time=t, value=t)
            model[t] = (t, t)
        result = index.query(7, 993)
        self.assertEqual(result.total, scan_reference(model, 7, 993))
        # 48 个完整粗桶 [20,980) + 4 个完整细桶
        # ([10,15) [15,20) [980,985) [985,990)）= 52 个聚合节点；
        # 边缘残窗 [7,10) 与 [990,993) 各 3 条原始事件。
        self.assertEqual(result.aggregate_nodes, 52)
        self.assertEqual(result.edge_events, 6)
        print(f"\n跨桶查询 [7, 993)：总和={result.total}，"
              f"聚合节点={result.aggregate_nodes}，"
              f"边缘原始事件={result.edge_events}")


if __name__ == "__main__":
    unittest.main(verbosity=2)
