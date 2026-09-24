"""运行样例：分批次输入，演示 ROWS/RANGE 帧上的 SUM 与 RANK。

运行：python3.11 example.py
"""

from window import (
    CURRENT_ROW,
    PRECEDING,
    UNBOUNDED_PRECEDING,
    Frame,
    OrderKey,
    WindowEngine,
)

NAN = float("nan")


def show(title, result):
    print(f"\n== {title} ==")
    print(f"{'row':<28}{'sum':>12}{'rank':>6}")
    for r in result:
        print(f"{str(r['row']):<28}{str(r['sum']):>12}{r['rank']:>6}")


def main():
    schema = [("grp", "str"), ("k", "int"), ("v", "int")]

    # 1) RANGE UNBOUNDED PRECEDING .. CURRENT ROW：peer 组整体进入帧。
    #    分区 'a' 横跨三个批次，验证分块边界不截断分区。
    engine = WindowEngine(
        schema=schema,
        partition_by=["grp"],
        order_by=[OrderKey("k")],
        sum_column="v",
        frame=Frame("range", UNBOUNDED_PRECEDING, CURRENT_ROW),
    )
    engine.add_batch([("a", 1, 10), ("b", 1, 100)])
    engine.add_batch([("a", 1, 20), ("a", 2, 30)])
    engine.add_batch([("b", 2, 200)])
    show("RANGE UNBOUNDED PRECEDING .. CURRENT ROW（分区跨批次）", engine.compute())

    # 2) 同样的数据用 ROWS CURRENT ROW：帧只含当前行自身。
    engine = WindowEngine(
        schema=schema,
        partition_by=["grp"],
        order_by=[OrderKey("k")],
        sum_column="v",
        frame=Frame("rows", CURRENT_ROW, CURRENT_ROW),
    )
    engine.add_batch([("a", 1, 10), ("a", 1, 20), ("a", 2, 30)])
    show("ROWS CURRENT ROW（帧只含自身）", engine.compute())

    # 3) NULL 与 NaN：NULL 排序由 nulls_first 显式控制且不参与求和；
    #    NaN 是普通值，排在数值之后、参与求和。
    fschema = [("grp", "str"), ("k", "float"), ("v", "float")]
    engine = WindowEngine(
        schema=fschema,
        partition_by=["grp"],
        order_by=[OrderKey("k", nulls_first=False)],
        sum_column="v",
        frame=Frame("rows", UNBOUNDED_PRECEDING, CURRENT_ROW),
    )
    engine.add_batch(
        [("a", None, 1.0), ("a", NAN, 2.0), ("a", 1.0, None), ("a", 2.0, 4.0)]
    )
    show("NULL vs NaN（nulls_first=False）", engine.compute())

    # 4) ROWS 1 PRECEDING .. CURRENT ROW + 大整数精确求和。
    big = 2**80 + 12345
    engine = WindowEngine(
        schema=schema,
        partition_by=["grp"],
        order_by=[OrderKey("k")],
        sum_column="v",
        frame=Frame("rows", PRECEDING(1), CURRENT_ROW),
    )
    engine.add_batch([("a", 1, big), ("a", 2, big), ("a", 3, 7)])
    show("ROWS 1 PRECEDING .. CURRENT ROW（大整数精确求和）", engine.compute())


if __name__ == "__main__":
    main()
