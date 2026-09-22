"""运行样例：在本地合成的关系表上执行两阶段聚合（不调用任何 SQL 引擎）。

表结构（多重集合，允许重复行；None 表示 NULL）：
    sales(region TEXT, channel TEXT, amount REAL, rep TEXT)

分片方式：按行号取模切成 3 个分片，另加一个空分片，
用于演示“空分片不贡献 AVG 分母”。
"""

from __future__ import annotations

from aggregation import (
    AVG,
    COUNT,
    COUNT_DISTINCT,
    COUNT_STAR,
    MAX,
    MIN,
    SUM,
    AggSpec,
    distributed_aggregate,
    finalize,
    local_phase,
)

# 合成关系表：包含 NULL、重复行（多重集合语义）。
SALES = [
    {"region": "east", "channel": "online", "amount": 120.0, "rep": "amy"},
    {"region": "east", "channel": "store", "amount": None, "rep": "amy"},   # NULL amount
    {"region": "east", "channel": "online", "amount": 120.0, "rep": "amy"},  # 重复行
    {"region": "west", "channel": "store", "amount": 80.0, "rep": "bob"},
    {"region": "west", "channel": "online", "amount": 200.0, "rep": "cy"},
    {"region": "west", "channel": "store", "amount": 80.0, "rep": "bob"},   # 重复行
    {"region": "north", "channel": "online", "amount": None, "rep": None},  # 双 NULL
    {"region": "north", "channel": "store", "amount": 50.0, "rep": "cy"},
    {"region": "south", "channel": "online", "amount": 300.0, "rep": "dan"},
    {"region": "south", "channel": "store", "amount": None, "rep": "amy"},
]

SPECS = [
    AggSpec(COUNT_STAR),
    AggSpec(COUNT, "amount"),
    AggSpec(SUM, "amount"),
    AggSpec(AVG, "amount"),
    AggSpec(MIN, "amount"),
    AggSpec(MAX, "amount"),
    AggSpec(COUNT_DISTINCT, "rep"),
]


def shard(rows, n_shards):
    return [rows[i::n_shards] for i in range(n_shards)]


def main() -> None:
    shards = shard(SALES, 3)
    shards.append([])  # 空分片

    print("== 分片（含一个空分片）==")
    for i, s in enumerate(shards):
        print(f"  shard {i}: {len(s)} 行")

    print("\n== 本地阶段：各分片中间状态 ==")
    per_shard = []
    for i, s in enumerate(shards):
        partials = local_phase(s, SPECS)
        per_shard.append(partials)
        avg_state = partials[3]
        print(f"  shard {i}: AVG(amount) 状态 = (sum={avg_state.total}, "
              f"count={avg_state.count})")

    results = distributed_aggregate(shards, SPECS)
    labels = ["COUNT(*)", "COUNT(amount)", "SUM(amount)", "AVG(amount)",
              "MIN(amount)", "MAX(amount)", "COUNT(DISTINCT rep)"]

    print("\n== 全局阶段：合并后最终结果 ==")
    for label, value in zip(labels, results):
        print(f"  {label:<22} = {value}")

    # 反例：对分片均值再取平均是错的。
    shard_means = [
        p[3].total / p[3].count for p in per_shard if p[3].count > 0
    ]
    naive = sum(shard_means) / len(shard_means)
    print("\n== 对照：平均“分片均值”（错误做法）==")
    print(f"  各分片均值 = {[round(m, 4) for m in shard_means]}")
    print(f"  错误结果   = {naive:.4f}  (正确 AVG = {results[3]:.4f})")

    # 全 NULL 列：SUM 返回 None（NULL），与空表语义一致。
    null_only = distributed_aggregate(
        [[{"x": None}, {"x": None}], []], [AggSpec(SUM, "x")]
    )
    print("\n== NULL 语义 ==")
    print(f"  全 NULL 列的 SUM = {null_only[0]}  (SQL 中为 NULL，非 0)")


if __name__ == "__main__":
    main()
