import unittest

from timemap import (
    Interval,
    IntervalSet,
    PiecewiseAffineMap,
    Segment,
    VersionedTimeline,
    WallClock,
)


def base_timeline():
    base = PiecewiseAffineMap(
        [Segment(Interval(0, 20, True, False), 1, 0, "base", "normal")]
    )
    return VersionedTimeline(base)


class TestVersionedTimeline(unittest.TestCase):
    def test_insert_pause_creates_new_version(self):
        tl = base_timeline()
        v1 = tl.insert_pause(WallClock(5), WallClock(10))
        self.assertEqual(v1, 1)
        self.assertEqual(tl.version_count, 2)

    def test_pause_collapses_endpoints(self):
        tl = base_timeline()
        tl.insert_pause(WallClock(5), WallClock(10))
        self.assertEqual(tl.logical_at(WallClock(5)), tl.logical_at(WallClock(10)))
        self.assertEqual(tl.logical_at(WallClock(5)).value, 5)
        self.assertEqual(tl.logical_at(WallClock(7)).value, 5)
        self.assertEqual(tl.logical_at(WallClock(15)).value, 10)

    def test_old_version_queries_unchanged(self):
        tl = base_timeline()
        before = tl.logical_at(WallClock(7), version=0)
        tl.insert_pause(WallClock(5), WallClock(10))
        tl.insert_pause(WallClock(12), WallClock(13))
        # 新增暂停不影响旧版本查询
        self.assertEqual(tl.logical_at(WallClock(7), version=0), before)
        self.assertEqual(tl.logical_at(WallClock(7), version=0).value, 7)
        self.assertEqual(tl.logical_at(WallClock(15), version=0).value, 15)
        # v1 只含第一个暂停
        self.assertEqual(tl.logical_at(WallClock(15), version=1).value, 10)
        # v2 含两个暂停
        self.assertEqual(tl.logical_at(WallClock(15), version=2).value, 9)

    def test_pause_preimage_boundary_choices(self):
        tl = base_timeline()
        tl.insert_pause(WallClock(5), WallClock(10))
        res = tl.preimage(IntervalSet.point(5), version=1)
        # 暂停段给出 [5,10)，后续段给出边界点 10，合并为 [5,10]
        self.assertEqual(
            res.values, IntervalSet.of(Interval(5, 10, True, True))
        )

    def test_pause_spanning_segments(self):
        base = PiecewiseAffineMap(
            [
                Segment(Interval(0, 10, True, False), 1, 0, "a", "normal"),
                Segment(Interval(10, 20, True, False), 2, -10, "b", "speed"),
            ]
        )
        tl = VersionedTimeline(base)
        tl.insert_pause(WallClock(3), WallClock(18))
        # f(3)=3，暂停 [3,18) 常量 3；b 段右部 [18,20) 平移 delta=f(18)-f(3)=23
        self.assertEqual(tl.logical_at(WallClock(2)).value, 2)
        self.assertEqual(tl.logical_at(WallClock(3)).value, 3)
        self.assertEqual(tl.logical_at(WallClock(17)).value, 3)
        self.assertEqual(tl.logical_at(WallClock(18)).value, 3)
        self.assertEqual(tl.logical_at(WallClock(19)).value, 5)

    def test_pause_outside_domain_rejected(self):
        tl = base_timeline()
        with self.assertRaises(ValueError):
            tl.insert_pause(WallClock(50), WallClock(60))
        with self.assertRaises(ValueError):
            tl.insert_pause(WallClock(10), WallClock(5))


if __name__ == "__main__":
    unittest.main()
