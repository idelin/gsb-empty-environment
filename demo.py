"""运行样例：多聚合查询的公共中间项去重执行。

覆盖场景：
- sum(x) 被 Q1/Q2/Q3 三处引用，只计算一次（共享节点与聚合状态）；
- sum(x) 与 sum(cast(x AS float)) 是不同节点，不合并，且 int 精确、float 近似；
- 过滤条件仅 null 处理不同的两个聚合各自保留（NaN 行两者都排除，因为 NaN != null）；
- 副作用函数 trace 出现两次，禁止复用，每行执行两次；
- null 与 NaN 区分：sum(y) 跳过 null，但 NaN 使结果为 NaN。
"""
from ir import FLOAT, INT, Builder
from exec_engine import Batch, Engine, render_plan

b = Builder()
x = b.col("x", INT)
y = b.col("y", FLOAT)

sum_x = b.agg("sum", x)
avg_x = b.agg("avg", x)
sum_y = b.agg("sum", y)
sum_x_float = b.agg("sum", b.cast(x, FLOAT))
big_sum = b.agg("sum", x, filter=b.binop("gt", x, b.lit(10)))

f_strict = b.binop("gt", y, b.lit(1.0))
f_keep_null = b.binop("or", f_strict, b.is_null(y))
sum_strict = b.agg("sum", x, filter=f_strict)
sum_keep_null = b.agg("sum", x, filter=f_keep_null)

calls = {"n": 0}


def trace(v):
    calls["n"] += 1
    return v


traced1 = b.agg("sum", b.func("trace", [x], INT, pure=False))
traced2 = b.agg("sum", b.func("trace", [x], INT, pure=False))

queries = [
    {"total": sum_x, "avg_x": avg_x, "sum_y": sum_y},
    {"total_again": sum_x, "total_as_float": sum_x_float},
    {
        "total_third": sum_x,
        "big_sum": big_sum,
        "f_strict": sum_strict,
        "f_keep_null": sum_keep_null,
        "traced1": traced1,
        "traced2": traced2,
    },
]

batches = [
    Batch({
        "x": (INT, [1, 2, 20, 5]),
        "y": (FLOAT, [0.5, float("nan"), 2.0, None]),
    }),
    Batch({
        "x": (INT, [3, 7, 30]),
        "y": (FLOAT, [1.5, None, 0.5]),
    }),
]

engine = Engine(funcs={"trace": trace})
results, counts, order = engine.run(queries, batches)

print(render_plan(order, queries, counts))
print()
for i, res in enumerate(results, 1):
    print(f"Q{i} 结果: {res}")
print(f"trace 副作用实际调用次数: {calls['n']}（2 个独立节点 x 7 行 = 14）")
