"""Runnable demo: external merge sort under a tiny memory budget.

Run with:  python3.11 demo.py
"""

import tempfile

from external_sort import (
    Batch,
    Column,
    ColumnType,
    ExternalSorter,
    SortCancelled,
    SortKey,
)

NAN = float("nan")

SCHEMA = (
    Column("id", ColumnType.INT),
    Column("score", ColumnType.FLOAT),
    Column("name", ColumnType.STRING),
)

BATCHES = [
    [(1, 9.5, "amy"), (2, None, "bob"), (3, 7.0, "cy")],
    [(4, 9.5, "dan"), (5, NAN, "eve"), (6, 7.0, None)],
    [(7, None, "gil"), (8, 9.5, "hal"), (9, 8.25, "ivy")],
]


def show(title, rows):
    print(f"\n== {title} ==")
    for row in rows:
        print("  ", row)


def main():
    keys = [
        SortKey("score", ascending=False, nulls_first=False),  # 高分在前, null 最后
        SortKey("name", ascending=True, nulls_first=True),     # 同分按名字升序
    ]

    with tempfile.TemporaryDirectory() as tmp:
        # 预算只有 300 字节 < 一个批次 -> 必然溢写多个临时归并段再归并。
        sorter = ExternalSorter(SCHEMA, keys, mem_budget=300, temp_dir=tmp)
        for rows in BATCHES:
            sorter.add_batch(Batch(SCHEMA, rows))
        result = sorter.sort_to_list()
        show("sorted (score DESC nulls last, name ASC nulls first)", result)
        print("temp dir after sort:", __import__("os").listdir(tmp) or "empty")

        # 取消后可以立刻复用同一个 sorter。
        sorter.add_batch(Batch(SCHEMA, BATCHES[0]))
        sorter.cancel()
        try:
            sorter.sort_to_list()
        except SortCancelled as exc:
            print("\ncancelled as expected:", exc)
        sorter.add_batch(Batch(SCHEMA, [(10, 1.0, "zed"), (11, 2.0, "yan")]))
        show("reused after cancel (same keys)", sorter.sort_to_list())


if __name__ == "__main__":
    main()
