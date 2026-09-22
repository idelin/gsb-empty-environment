"""pullpipe 运行样例。

    python3.11 examples/demo.py            # 全部样例
    python3.11 examples/demo.py 10000000   # 自定义大表行数
"""

from __future__ import annotations

import sys
import os
import time

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from pullpipe import (
    AggDef,
    Batch,
    CancellationToken,
    CancelledError,
    CsvTable,
    EmptyInjector,
    Exploder,
    Filter,
    GeneratedTable,
    HashAggregate,
    HashJoin,
    Limit,
    OperatorError,
    Pipeline,
    Project,
    Sort,
    TableScan,
    col,
    lit,
)
from pullpipe.null import Null
from pullpipe.sample_tables import big_fact, customers, events


def section(title: str) -> None:
    print(f"\n=== {title} ===")


def demo_limit1(rows: int) -> None:
    section(f"1. LIMIT 1 不完整扫描 {rows:,} 行（batch_size=1，逐行即停）")
    table = big_fact(rows)
    scan = TableScan(table, batch_size=1)
    pipe = Pipeline(Limit(scan, 1))
    started = time.perf_counter()
    got = pipe.drain()
    elapsed = time.perf_counter() - started
    produced = table.last_cursor.rows_generated
    print("结果行:", got)
    print(f"实际生成行数: {produced}（不是 {rows:,}），耗时 {elapsed*1000:.2f} ms")
    print("scan 已关闭:", scan.closed, "| 生成器游标已关闭:", table.last_cursor.closed)
    assert produced == 1, produced
    assert scan.closed

    table2 = big_fact(rows)
    scan2 = TableScan(table2, batch_size=64)
    Pipeline(Limit(scan2, 1)).drain()
    print(f"批量读取（batch_size=64）时也最多只读第一批: "
          f"{table2.last_cursor.rows_generated} 行，随后立即关闭上游")
    assert table2.last_cursor.rows_generated <= 64


def demo_query() -> None:
    section("2. 扫描 -> 过滤 -> 哈希连接 -> 分组聚合 -> 排序 Top-3（百万行）")
    scan_o = TableScan(big_fact(1_000_000), batch_size=4096)
    flt = Filter(scan_o, col("amount") > lit(30))
    scan_c = TableScan(customers(), batch_size=256)
    join = HashJoin(flt, scan_c, col("cust_id"), col("cust_id"))
    proj = Project(join, [col("cust_id"), col("cname"), col("amount")],
                   ["cust_id", "cname", "amount"])
    agg = HashAggregate(
        proj,
        [AggDef("COUNT_ALL", name="n"), AggDef("SUM", col("amount"), "total")],
        group_keys=[col("cust_id"), col("cname")],
        group_names=["cust_id", "cname"],
    )
    top = Limit(Sort(agg, [col("total")], descs=[True]), 3)
    pipe = Pipeline(top)
    batch_count = 0
    rows_count = 0
    for batch in pipe.batches():
        batch_count += 1
        rows_count += len(batch)
        for row in batch:
            print("  ", row)
    print(f"输出行数: {rows_count}，输出批次数: {batch_count}")
    print("所有算子已关闭:", top.closed, agg.closed, join.closed, scan_c.closed, scan_o.closed)
    # cust_id=99 在维表有两行，连接后翻倍
    n99 = [r for r in pipe.drain()]  # EOF 之后再拉取，安全且为空
    assert n99 == []


def demo_null_semantics() -> None:
    section("3. NULL 与多重集合语义（events 表）")
    scan = TableScan(events(), batch_size=3)
    agg = HashAggregate(
        scan,
        [
            AggDef("COUNT_ALL", name="cnt_all"),
            AggDef("COUNT", col("v"), name="cnt_v"),
            AggDef("SUM", col("v"), name="sum_v"),
            AggDef("AVG", col("v"), name="avg_v"),
            AggDef("MIN", col("v"), name="min_v"),
            AggDef("MAX", col("v"), name="max_v"),
        ],
        group_keys=[col("g")],
        group_names=["g"],
    )
    pipe = Pipeline(agg)
    for row in pipe.drain():
        print("  ", row)
    print("  注：NULL 分组键显示为 NULL；COUNT(v) 忽略 NULL，SUM/AVG 同。")

    section("3b. WHERE v > 5：NULL 行既不匹配也不报错（Unknown 不通过）")
    scan2 = TableScan(events(), batch_size=3)
    pipe2 = Pipeline(Filter(scan2, col("v") > lit(5)))
    print("  通过行数:", len(pipe2.drain()))


def demo_empty_batch() -> None:
    section("4. 空批次 != EOF：EmptyInjector 先吐空批再透传数据")
    table = events()
    scan = TableScan(table, batch_size=2)
    root = EmptyInjector(scan)
    pipe = Pipeline(root)
    seen_empty = False
    total = 0
    for batch in pipe.batches():
        if len(batch) == 0:
            seen_empty = True
            print("  收到空 Batch（bool(batch) =", bool(batch), "），但仍继续拉取")
        total += len(batch)
    print("  收到空批次:", seen_empty, "| 最终真实行数:", total)
    assert seen_empty and total == len(table.rows)


def demo_cancel() -> None:
    section("5. 外部取消：消费若干批后 cancel，上游随即停止并释放")
    table = big_fact(1_000_000)
    scan = TableScan(table, batch_size=128)
    token = CancellationToken()
    pipe = Pipeline(scan, token)
    produced_at_cancel = 0
    try:
        for batch in pipe.batches():
            produced_at_cancel += len(batch)
            if produced_at_cancel >= 500:
                token.cancel("consumer lost interest")
    except CancelledError as exc:
        print(f"  捕获 CancelledError: {exc}")
    print("  取消前消费行数:", produced_at_cancel, "| scan 已关闭:", scan.closed)
    print("  生成器实际产出行数:", table.last_cursor.rows_generated)
    assert scan.closed
    assert table.last_cursor.rows_generated < 10_000


def demo_downstream_error() -> None:
    section("6. 下游出错 -> 关闭上游（百万行表不会继续被扫描）")
    table = big_fact(1_000_000)
    scan = TableScan(table, batch_size=128)
    root = Exploder(scan)
    pipe = Pipeline(root)
    try:
        pipe.drain()
    except OperatorError as exc:
        print(f"  捕获 OperatorError: {exc}")
        print(f"  原始异常: {type(exc.cause).__name__}: {exc.cause}")
    print("  scan 已关闭:", scan.closed, "| 生成器已关闭:", table.last_cursor.closed)
    print("  实际产出行数:", table.last_cursor.rows_generated,
          "（读了第一批即报错，远小于百万）")
    assert scan.closed
    assert table.last_cursor.rows_generated == 128
    section("6b. 重复 close 安全")
    pipe.close()
    root.close()
    scan.close()
    print("  三次额外 close 均无异常；release_count 只计一次:", scan.release_count)


def demo_csv(tmp_dir: str) -> None:
    section("7. CSV 文件表：提前停止时文件句柄随游标释放")
    import csv
    path = os.path.join(tmp_dir, "nums.csv")
    with open(path, "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["k", "v"])
        for i in range(100_000):
            w.writerow([i % 4, i])
    table = CsvTable(path, ("k", "v"))
    scan = TableScan(table, batch_size=16)
    pipe = Pipeline(Limit(scan, 5))
    rows = pipe.drain()
    print("  取到", len(rows), "行，首行:", rows[0])
    print("  CSV 游标已关闭（文件句柄已释放）:", table.last_cursor.closed)
    assert table.last_cursor.closed


if __name__ == "__main__":
    n = int(sys.argv[1]) if len(sys.argv) > 1 else 1_000_000
    import tempfile

    demo_limit1(n)
    demo_query()
    demo_null_semantics()
    demo_empty_batch()
    demo_cancel()
    demo_downstream_error()
    with tempfile.TemporaryDirectory() as td:
        demo_csv(td)
    print("\n所有样例断言通过。")
