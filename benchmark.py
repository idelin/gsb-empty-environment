"""资源增长对比：增量执行触及的候选点数量 vs 全量重算。

每次修改 = 更正一条历史记录。增量路径只重算窗口覆盖被改样本的候选点，
理论上每次 <= 2*window 个，与数据总量 n 无关；全量重算（oracle）每次都要
评估全部 n+1 个候选点。下面用实际计数（ChangePointIndex.touched）验证。
"""

import random
from fractions import Fraction

from changepoint import ChangePointIndex, full_recompute

WINDOW, MIN_SAMPLES, THRESHOLD = 8, 4, Fraction(3)
OPS = 100


def build(n, rng):
    idx = ChangePointIndex(WINDOW, MIN_SAMPLES, THRESHOLD)
    idx.apply_batch(
        [('add', 's', i, rng.choice([0, 1, 2, 5, 8])) for i in range(n)])
    return idx


def main():
    rng = random.Random(7)
    print(f'参数: window={WINDOW}, min_samples={MIN_SAMPLES}, '
          f'threshold={THRESHOLD}, 每次修改=更正 1 条历史记录, 共 {OPS} 次')
    header = f'{"n":>6} {"增量触及候选点/次":>18} {"全量重算候选点/次":>18} {"倍数":>8}'
    print(header)
    print('-' * len(header))
    for n in (500, 1000, 2000, 4000, 8000):
        idx = build(n, rng)
        idx.touched = 0
        for _ in range(OPS):
            p = rng.randrange(n)
            idx.correct('s', p, rng.choice([0, 1, 2, 5, 8]))
        avg = idx.touched / OPS
        full = n + 1  # full_recompute 每次评估的候选点数
        print(f'{n:>6} {avg:>18.1f} {full:>18} {full / avg:>7.1f}x')
    print()
    print('结论：增量每次触及约 2*window=%d 个候选点（同值更正是 no-op，'
          '故均值略低），不随 n 增长；' % (2 * WINDOW))
    print('全量重算每次评估 n+1 个候选点，随数据量线性增长，'
          '且每个候选点的窗口求和还要再花 O(window)。')
    # 正确性抽查：增量结果与 oracle 一致
    idx = build(2000, random.Random(99))
    events = [(('s', i), idx._values[('s', i)]) for i in range(2000)]
    assert set(idx.change_points()) == set(
        full_recompute(events, WINDOW, MIN_SAMPLES, THRESHOLD))
    print('抽查: n=2000 时增量结果与全量 oracle 一致。')


if __name__ == '__main__':
    main()
