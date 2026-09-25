"""运行样例：python3 demo.py"""

from window import (
    Batch,
    Column,
    CURRENT_ROW,
    Frame,
    OrderKey,
    RANK,
    SUM,
    WindowEngine,
)

COLS = [Column("g", "str"), Column("k", "float"), Column("v", "int")]
ORDER = [OrderKey("k", nulls_first=False)]  # 升序，NULLS LAST
MEASURES = [
    RANK(),
    SUM("v", Frame("rows", CURRENT_ROW, CURRENT_ROW), "rows_self"),
    SUM("v", Frame("range", CURRENT_ROW, CURRENT_ROW), "range_peer"),
    SUM("v", Frame("rows"), "rows_cum"),
    SUM("v", Frame("range"), "range_cum"),
]


def show(title, rows):
    print(f"\n== {title} ==")
    head = ("g", "k", "v", "rank", "rows_self", "range_peer", "rows_cum", "range_cum")
    print("  ".join(h.ljust(10) for h in head))
    for r in rows:
        print("  ".join(str(r.get(h)).ljust(10) for h in head))


def main():
    nan = float("nan")
    # 分区 a 跨两个分块；含重复键 1,1、NULL 键、NaN 键、大整数
    batch1 = Batch(COLS, [
        {"g": "a", "k": 1.0, "v": 10},
        {"g": "a", "k": 1.0, "v": 20},
    ])
    batch2 = Batch(COLS, [
        {"g": "a", "k": 2.0, "v": 2**63 + 1},
        {"g": "a", "k": None, "v": 40},
        {"g": "a", "k": nan, "v": 50},
        {"g": "b", "k": 1.0, "v": 5},
    ])
    batch3 = Batch(COLS, [
        {"g": "b", "k": 2.0, "v": 7},
    ])

    eng = WindowEngine(["g"], ORDER, MEASURES)
    out_a, out_b = [], []
    out_a += eng.add_batch(batch1)          # 分区 a 未闭合，暂无输出
    print(f"batch1 后立即输出 {len(out_a)} 行（分区 a 跨分块，未被截断）")
    out_a += eng.add_batch(batch2)          # 遇到分区 b，结算 a
    out_b += eng.add_batch(batch3)
    out_b += eng.finish()

    show("分区 a（键 1,1,2,NULL,NaN；v 含 2**63+1 精确整数）", out_a)
    show("分区 b（跨 batch2/batch3）", out_b)


if __name__ == "__main__":
    main()
