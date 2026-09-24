#!/usr/bin/env python3
"""运行样例：点-区间 / 区间-区间连接，索引连接 vs 扫描线。

直接运行：python3.11 demo.py
数据以带列类型的批次输入，覆盖重复端点、嵌套、空/零长区间、
null 与 NaN 的区别、整数精确计算。
"""
from ival import (
    RecordBatch, Schema, Table, table_from_rows,
    interval_interval_join, point_interval_join,
    sweep_interval_join, sweep_point_join,
)

IV_SCHEMA = Schema.from_pairs([("id", "int64"), ("lo", "int64"), ("hi", "int64")])
PT_SCHEMA = Schema.from_pairs([("id", "int64"), ("p", "int64")])


def show(title, result):
    print(f"--- {title}（实现: {result.method}）---")
    if not result.matches:
        print("  (无匹配)")
    for m in result.matches:
        print(f"  left_id={m.left_id}  right_id={m.right_id}  "
              f"overlap_len={m.overlap_len}")
    ls, rs = result.left_stats, result.right_stats
    print(f"  左侧: 参与 {ls.used}/{ls.total_rows} 行, "
          f"null端点 {ls.null_endpoint_rows}, NaN端点 {ls.nan_endpoint_rows}, "
          f"空/零长 {ls.empty_interval_rows}")
    print(f"  右侧: 参与 {rs.used}/{rs.total_rows} 行, "
          f"null端点 {rs.null_endpoint_rows}, NaN端点 {rs.nan_endpoint_rows}, "
          f"空/零长 {rs.empty_interval_rows}")
    print(f"  比较对: 朴素笛卡尔 {result.naive_pairs} -> "
          f"实际只检查 {result.examined_pairs}")
    print()


def main():
    print("================ 1) 点-区间连接 ================")
    # 点表：5 是边界点；两个重复的 2 演示重复端点
    points = Table(PT_SCHEMA, [RecordBatch(PT_SCHEMA, {
        "id": [1, 2, 3, 4, 5, 6],
        "p":  [0, 2, 2, 5, 7, 8],
    })])
    # 区间表：两批次输入；含嵌套 [0,9)、零长 [7,7)、null 与 NaN（float 批次另给）
    intervals = Table(IV_SCHEMA, [
        RecordBatch(IV_SCHEMA, {"id": [10, 11, 12],
                                "lo": [0, 0, 3], "hi": [5, 9, 5]}),
        RecordBatch(IV_SCHEMA, {"id": [13, 14, 15],
                                "lo": [5, 7, 9], "hi": [8, 7, None]}),
    ])
    show("点 × 区间：索引连接",
         point_interval_join(points, "id", "p", intervals, "id", "lo", "hi"))
    show("点 × 区间：扫描线（对账）",
         sweep_point_join(points, "id", "p", intervals, "id", "lo", "hi"))

    print("================ 2) 区间-区间连接 ================")
    left = Table(IV_SCHEMA, [RecordBatch(IV_SCHEMA, {
        "id": [1, 2, 3], "lo": [0, 2, 7], "hi": [7, 2, 9],
    })])  # 含嵌套目标、零长 [2,2)
    right = Table(IV_SCHEMA, [
        RecordBatch(IV_SCHEMA, {"id": [100, 101], "lo": [3, 5], "hi": [5, 8]}),
        RecordBatch(IV_SCHEMA, {"id": [102, 103, 104],
                                "lo": [0, 1, 9], "hi": [9, 2, 12]}),
    ])
    show("区间 × 区间：索引连接",
         interval_interval_join(left, "id", "lo", "hi",
                                right, "id", "lo", "hi"))
    show("区间 × 区间：扫描线（对账）",
         sweep_interval_join(left, "id", "lo", "hi",
                             right, "id", "lo", "hi"))

    print("================ 3) null vs NaN（float64 批次）================")
    nan = float("nan")
    fschema = Schema.from_pairs([("id", "int64"),
                                 ("lo", "float64"), ("hi", "float64")])
    ft = Table(fschema, [RecordBatch(fschema, {
        "id": [1, 2, 3],
        "lo": [0.0, None, 1.0],
        "hi": [5.0, 3.0, nan],
    })])
    ref = table_from_rows(
        [("id", "int64"), ("lo", "float64"), ("hi", "float64")],
        [{"id": 99, "lo": 0.0, "hi": 5.0}])
    res = interval_interval_join(ft, "id", "lo", "hi",
                                 ref, "id", "lo", "hi")
    print(f"  null 端点行: {res.left_stats.null_endpoint_rows}（跳过）")
    print(f"  NaN  端点行: {res.left_stats.nan_endpoint_rows}（跳过，与 null 不同）")
    print(f"  参与行: {res.left_stats.used}；匹配: "
          f"{[(m.left_id, m.right_id, m.overlap_len) for m in res.matches]}")
    print()

    print("================ 4) 整数精确（大整数无浮点误差）================")
    big = 10**20
    big_left = table_from_rows(
        [("id", "int64"), ("lo", "int64"), ("hi", "int64")],
        [{"id": 1, "lo": big, "hi": big + 7}])
    big_right = table_from_rows(
        [("id", "int64"), ("lo", "int64"), ("hi", "int64")],
        [{"id": 2, "lo": big + 5, "hi": big + 10}])
    m = interval_interval_join(big_left, "id", "lo", "hi",
                               big_right, "id", "lo", "hi").matches[0]
    print(f"  [{big},{big+7}) ∩ [{big+5},{big+10}) "
          f"=> overlap_len={m.overlap_len}（类型 {type(m.overlap_len).__name__}）")


if __name__ == "__main__":
    main()
