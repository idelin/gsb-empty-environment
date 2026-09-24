"""运行样例: 多聚合查询的公共子表达式去重执行。

场景:
  Q1: sum(x), sum(cast(x as float))      -- 类型不同, 不得合并
  Q2: sum(x), avg(x)                     -- sum(x) 第 2 次引用
  Q3: sum(x), sum(x) filter(x>1), sum(x) filter(x>100), count(y), sum(y)
                                         -- sum(x) 第 3 次引用; 过滤条件不同不得合并
  Q4: next_val(), next_val()             -- 副作用函数, 禁止复用
"""
import itertools

from cse_agg import INT, FLOAT, BOOL, Col, Lit, Cast, Func, Agg, Engine

NAN = float("nan")

# 表按带列类型的批次输入; None 表示 null, 与 NaN 不同
SCHEMA = [("x", INT), ("y", FLOAT)]
BATCHES = [
    (SCHEMA, [(1, 1.5), (2, None), (None, NAN), (2 ** 70, 2.0)]),
    (SCHEMA, [(3, 3.5), (None, None), (5, NAN)]),
]

x = Col("x", INT)
y = Col("y", FLOAT)


def gt(a, b):
    return a > b  # null 传播由引擎处理; NaN > b 按 IEEE 为 False


sum_x = Agg("sum", x, out_type=INT)
queries = [
    # Q1
    [sum_x, Agg("sum", Cast(x, FLOAT), out_type=FLOAT)],
    # Q2
    [sum_x, Agg("avg", x, out_type=INT)],
    # Q3
    [sum_x,
     Agg("sum", x, out_type=INT, filt=Func("gt", gt, [x, Lit(1, INT)], BOOL)),
     Agg("sum", x, out_type=INT, filt=Func("gt", gt, [x, Lit(100, INT)], BOOL)),
     Agg("count", y, out_type=INT),
     Agg("sum", y, out_type=FLOAT)],
    # Q4: 副作用函数出现两次, 必须各自独立计算
    [Func("next_val", itertools.count(1).__next__, [], INT, volatile=True),
     Func("next_val", itertools.count(100).__next__, [], INT, volatile=True)],
]

engine = Engine(queries)
results, counts = engine.run(BATCHES)

print(engine.graph())
print()
labels = [["sum(x)", "sum(cast(x) as float)"],
          ["sum(x)", "avg(x)"],
          ["sum(x)", "sum(x) f(x>1)", "sum(x) f(x>100)", "count(y)", "sum(y)"],
          ["next_val()#1", "next_val()#2"]]
for qi, (q, row) in enumerate(zip(labels, results), 1):
    for name, val in zip(q, row):
        if isinstance(val, list):  # 标量根: 每批次一个向量
            val = [f"batch{i}={v}" for i, v in enumerate(val)]
        print(f"Q{qi} {name:<26} = {val}")
print()
print("actual computation counts (per node, one per batch):")
for n in engine.plan.nodes:
    print(f"  n{n.id}: {n.label:<40} computed {counts[n.id]}x")

sum_x_node = engine.plan.memo[sum_x.key()]
assert counts[sum_x_node] == len(BATCHES), "sum(x) 三处引用必须只算一次(每批)"
print(f"\nOK: sum(x) referenced 3 times -> node n{sum_x_node} "
      f"computed {counts[sum_x_node]}x (= {len(BATCHES)} batches x 1)")
