"""共享的演示映射：常速 / 暂停 / 常速 / 反向 / 倍速 五段。

  s1 [0,5)   rate  1  offset   0   -> [0, 5)
  p1 [5,10)  rate  0  offset   5   -> {5}      暂停：5 和 10 映射同一逻辑点
  s2 [10,20) rate  1  offset  -5   -> [5, 15)
  r1 [20,30) rate -1  offset  35   -> (5, 15]  反向段
  v1 [30,40) rate  2  offset -55   -> [5, 25)  二倍速段
"""
from timemap import Interval, PiecewiseAffineMap, Segment


def build_demo_map():
    return PiecewiseAffineMap(
        [
            Segment(Interval(0, 5, True, False), 1, 0, "s1", "normal"),
            Segment(Interval(5, 10, True, False), 0, 5, "p1", "pause"),
            Segment(Interval(10, 20, True, False), 1, -5, "s2", "normal"),
            Segment(Interval(20, 30, True, False), -1, 35, "r1", "reverse"),
            Segment(Interval(30, 40, True, False), 2, -55, "v1", "speed"),
        ]
    )


def build_second_map():
    """g：前半半速、后半三倍速，用于复合。"""
    return PiecewiseAffineMap(
        [
            Segment(Interval(0, 10, True, False), "1/2", 0, "g1", "speed"),
            Segment(Interval(10, 30, True, False), 3, -20, "g2", "speed"),
        ]
    )
