"""运行样例：宽表透视引擎的典型用法。

运行：python3.11 demo.py
"""

from widepivot import (
    Agg,
    Batch,
    Column,
    DataType,
    NAN,
    NULL,
    PivotEngine,
    Row,
    Schema,
)

KEY = "user_id"
CAT = "channel"
VAL = "amount"


def show(title, view):
    cols = [c.name for c in view.output_columns()]
    print(f"  {title} (数据版本={view.data_version}, 模式版本={view.schema_version})")
    print("  " + " | ".join(f"{c:>10}" for c in cols))
    for record in view.records():
        print("  " + " | ".join(f"{str(record.get(c)):>10}" for c in cols))
    print()


def main():
    schema = Schema((KEY,), CAT, VAL, DataType.INTEGER, Agg.SUM)
    events = []
    engine = PivotEngine(schema, on_event=events.append)

    print("== 批次1：A、B 两个类别 ==")
    b1 = Batch.of(
        (
            Column(KEY, DataType.STRING),
            Column(CAT, DataType.STRING),
            Column(VAL, DataType.INTEGER),
        ),
        [
            {KEY: "u1", CAT: "A", VAL: 1},
            {KEY: "u1", CAT: "B", VAL: 10},
            {KEY: "u2", CAT: "A", VAL: 2},
        ],
    )
    r1 = engine.apply_batch(b1)
    show("批次1后宽表", r1.view)
    old_view = r1.view  # 保存旧模式视图

    print("== 批次2：新增类别 C（旧行应为 NULL）；同键同类别冲突按 SUM ==")
    b2 = Batch.of(
        (
            Column(KEY, DataType.STRING),
            Column(CAT, DataType.STRING),
            Column(VAL, DataType.INTEGER),
        ),
        [
            {KEY: "u1", CAT: "A", VAL: 4},   # u1/A: 1 + 4 = 5
            {KEY: "u1", CAT: "C", VAL: 100},
            {KEY: "u2", CAT: "C", VAL: 200},
            {KEY: "u3", CAT: "B", VAL: 30},
        ],
    )
    r2 = engine.apply_batch(b2)
    show("批次2后宽表", r2.view)

    print("== 旧批次视图对齐新模式：新类别列补 NULL ==")
    aligned = engine.align(old_view)
    show("批次1视图 @ 新列序", aligned)

    print("== 大整数精确求和（远超 2^63-1）==")
    big = 10 ** 40
    engine.apply_batch(Batch.of(
        (
            Column(KEY, DataType.STRING),
            Column(CAT, DataType.STRING),
            Column(VAL, DataType.INTEGER),
        ),
        [{KEY: "u3", CAT: "B", VAL: big}],
    ))
    r_big = engine.apply_batch(Batch.of(
        (
            Column(KEY, DataType.STRING),
            Column(CAT, DataType.STRING),
            Column(VAL, DataType.INTEGER),
        ),
        [{KEY: "u3", CAT: "B", VAL: big + 5}],
    ))
    show("大整数 SUM（30 + 2*10^40 + 5）", r_big.view)

    print("== 撤回：先撤回 u2/C（u2 仍保留 A=2），再撤回 u2/A（整行消失）==")
    engine.apply_batch(Batch.of(
        (
            Column(KEY, DataType.STRING),
            Column(CAT, DataType.STRING),
            Column(VAL, DataType.INTEGER),
        ),
        [Row.retract({KEY: "u2", CAT: "C", VAL: 200})],
    ))
    r3 = engine.apply_batch(Batch.of(
        (
            Column(KEY, DataType.STRING),
            Column(CAT, DataType.STRING),
            Column(VAL, DataType.INTEGER),
        ),
        [Row.retract({KEY: "u2", CAT: "A", VAL: 2})],
    ))
    show("两次撤回后宽表（其他类别不受影响）", r3.view)

    print("== 输出模式变更事件 ==")
    for ev in events:
        print(f"  SchemaChanged(v{ev.version}): 新增 {list(ev.added_categories)} -> 全部列 {list(ev.categories)}")
    print()

    print("== NULL 与 NaN 不同（FLOAT 引擎）==")
    fschema = Schema((KEY,), CAT, "score", DataType.FLOAT, Agg.SUM)
    fengine = PivotEngine(fschema)
    fb = Batch.of(
        (
            Column(KEY, DataType.STRING),
            Column(CAT, DataType.STRING),
            Column("score", DataType.FLOAT),
        ),
        [
            {KEY: "x", CAT: "N", "score": NULL},      # 空单元格语义
            {KEY: "x", CAT: "Z", "score": NAN},        # 非有限数值语义
            {KEY: "y", CAT: "N", "score": 0.1},
            {KEY: "y", CAT: "Z", "score": 0.2},
        ],
    )
    fv = fengine.apply_batch(fb).view
    for rec in fv.records():
        n, z = rec["N"], rec["Z"]
        print(f"  {rec[KEY]}: N={n!r}  Z={z!r}  N is NULL={n is None}  Z is NaN={isinstance(z, float) and z != z}")
    print(f"  fsum 0.1 + 0.2 = {0.1 + 0.2!r}（引擎输出见上，使用 math.fsum）")


if __name__ == "__main__":
    main()
