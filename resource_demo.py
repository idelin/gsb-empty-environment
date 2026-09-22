"""增量更新 vs 全量重算的资源增长测量。

度量指标是"单次变更实际重新评估的候选点数量"：
- 全量重算需要扫描全部可行间隙，数量随样本数线性增长；
- 增量索引只触及变动位置附近 L+R 量级的间隙，与总量无关。
"""

from fractions import Fraction

from changepoint import ChangePointIndex


def _build(n, left_min, right_min):
    idx = ChangePointIndex(left_min, right_min, Fraction(10**9))
    for seq in range(n):
        idx.upsert("s", seq, seq % 7)
    return idx


def measure(left_min=5, right_min=5, sizes=(250, 500, 1000, 2000, 4000)):
    rows = []
    for n in sizes:
        # 追加构建：记录最后一次 append 触及的候选点。
        grow = ChangePointIndex(left_min, right_min, Fraction(10**9))
        append_max = 0
        for seq in range(n):
            grow.upsert("s", seq, seq % 7)
            append_max = max(append_max, grow.last_touched)

        idx = _build(n, left_min, right_min)

        # 中部更正一条历史值。
        pos = n // 3
        idx.upsert("s", pos, 100)
        update_touched = idx.last_touched

        # 中部插入一条全新事件（seq 取分数，落在两个整数序号之间）。
        insert_key = (Fraction(pos) + Fraction(1, 2))
        idx.upsert("s", insert_key, 100)
        insert_touched = idx.last_touched

        full_candidates = (n - 1) - (left_min - 1) - (right_min - 1)
        rows.append((n, max(full_candidates, 0), append_max,
                     update_touched, insert_touched,
                     idx.total_touched))
    return rows


def format_table(rows):
    header = ("样本数 n | 全量重算候选点 | 追加构建单次最多触及 | "
              "中部更正触及 | 中部插入触及 | 增量累计触及")
    lines = [header, "-" * len(header)]
    for n, full, app, upd, ins, total in rows:
        lines.append(f"{n:7d} | {full:14d} | {app:20d} | {upd:12d} | "
                     f"{ins:12d} | {total:14d}")
    lines.append("")
    lines.append("结论：全量重算成本随 n 线性增长；单次增量更正/插入触及的"
                 "候选点数量被 L+R 界住，与 n 无关。")
    return "\n".join(lines)


if __name__ == "__main__":
    print(format_table(measure()))
