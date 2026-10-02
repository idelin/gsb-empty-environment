# planreflector — EXPLAIN ANALYZE 估算误差反思器（优化器闭环校验器）

纯 Go 标准库实现。将优化器事前估算的 Cost / Cardinality 与物理执行器
EXPLAIN ANALYZE 跑出的真实行数 / 耗时**逐算子对齐比对**，输出偏差倍率
（Q-Error）与分级告警，并给出诊断与调优建议。

## 组件

| 文件 | 职责 |
|---|---|
| `plan.go` | 估算计划树（`EstNode`）与实际执行树（`ActNode`）定义 |
| `qerror.go` | Q-Error 偏差倍率计算与告警分级（>=4x WARN，>=10x CRITICAL） |
| `compare.go` | 按算子 ID 逐节点对齐比对、诊断建议生成、报告渲染 |
| `executor.go` | 迷你内存执行器（Scan/Filter/Aggregate）+ 基于统计信息的优化器 |
| `main.go` | 运行样例：数据倾斜 + 统计信息失效场景 |

## 运行样例

```sh
go run .
```

场景：`users` 表上次 ANALYZE 时仅 100 行，之后批量导入 50000 行倾斜数据
（49000 行 `region='CN'`）。优化器仍按失效统计信息估算，执行
`SELECT region, COUNT(*) FROM users WHERE age >= 30 GROUP BY region`：

```
==== 优化器闭环校验报告 (EXPLAIN ANALYZE vs 优化器估算) ====
ID   算子                 估算行数     真实行数   偏差倍率   估算Cost  真实耗时(ms) 级别
1    Scan(users)             100        50000    500.0x      11.0        0.566 CRITICAL
2    Filter                   50        40000    800.0x      12.0        4.318 CRITICAL
3    Aggregate                10            2      5.0x      14.5        6.023 WARN

---- 告警 (3 条) ----
[CRITICAL] Scan(users): 行数低估 500.0 倍（估算 100 vs 真实 50000）；表 users 的
统计信息/直方图已失效，优化器拿到的行数严重偏小。建议执行 ANALYZE TABLE users
重新收集统计信息（含直方图）后重试。
...
```

## 测试

```sh
go test -v ./...
```

覆盖：Q-Error 计算、告警分级、**验收场景**（`TestSkewedQueryAcceptance`：
精准定位 `Scan(users)` 估算 100 vs 真实 50000 偏差超标，提示直方图失效并
建议重新收集统计信息）、统计准确时无误报、计划树结构不匹配检测、全算子覆盖。
