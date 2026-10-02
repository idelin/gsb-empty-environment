# reflector — EXPLAIN ANALYZE 估算误差反思器

优化器闭环校验器：将优化器事前估算的 Cost/Card 与物理执行器跑出的真实
行数/耗时逐算子对齐比对，输出偏差倍率与告警，并给出归因诊断与修复建议。

## 能力

- 按算子 ID 将估算计划树（EXPLAIN）与实际执行统计（EXPLAIN ANALYZE）逐节点对齐
- 计算每个算子的行数偏差倍率与方向（underestimate / overestimate）
- 阈值化告警：默认 4 倍 WARN、10 倍 CRITICAL（可通过 `Options` 配置）
- 归因诊断：Scan 严重低估 → 直方图失效，建议 `ANALYZE TABLE` 重新收集统计；
  Filter → 谓词相关性；Join → 连接顺序/方式次优
- 缺失实际统计的算子列入 `MissingActual`

## 运行样例

```sh
go run cmd/demo/main.go
```

模拟含数据倾斜的查询（`users.city='Beijing'` 实际 50000 行，直方图失效
导致估算仅 100 行），输出：

```
==== 优化器闭环校验报告 ====
[OK] Aggregate est=1 act=1 dev=1.0x (accurate)
[CRITICAL] HashJoin est=100 act=50000 dev=500.0x (underestimate)
    ...
[CRITICAL] Scan(users) est=100 act=50000 dev=500.0x (underestimate)
    诊断: Scan(users) 估算行数 100 与真实行数 50000 偏差 500 倍：列直方图未能刻画数据倾斜，统计信息已失效
    建议: 直方图失效：对表 users 重新收集统计信息（ANALYZE TABLE users）
[OK] Scan(orders) est=1000000 act=1050000 dev=1.1x (accurate)
==== 总体结论: CRITICAL ====
```

## 测试

```sh
go test ./...
```

覆盖：Scan(users) 100 vs 50000 偏差超标并提示直方图失效、准确节点不误报、
Join 偏差传导、缺失实际统计、告警阈值边界、报告汇总与排序。
