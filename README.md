# 容错副本放置证书（Fault-Tolerant Replica Placement Certificate）

精确求解器：为单个数据分片选择 `r` 个副本的放置服务器，单机不重复，
任一**指定故障集合**失效后仍剩至少 `q` 个副本，最小化总副本成本。
求解方法为对全部 `C(n, r)` 个候选放置的**穷举枚举**，因此：

- 返回的放置方案是**可证明最优**的（证书中附完整枚举统计）；
- 不可行时**不输出假证书**，只输出可复核的穷举统计，任何人均可用
  `verify` 子命令独立重算确认。

仅依赖 Python 3.11+ 标准库。所有示例数据均为合成数据。

## 运行

```bash
python3.11 replica_placement.py solve  SPEC.json [-o CERT.json]
python3.11 replica_placement.py verify SPEC.json CERT.json
python3.11 -m unittest test_replica_placement -v
```

退出码：`solve` — 0 最优解已输出，1 不可行（仍输出统计 JSON），2 输入非法；
`verify` — 0 证书通过复核，1 证书无效。

## 输入格式（SPEC.json）

```json
{
  "r": 2,
  "q": 1,
  "servers": [
    {"id": "s1", "rack": "r1", "zone": "z1", "capacity": 1, "cost": 1}
  ],
  "failure_sets": [
    {"name": "fs1", "servers": ["s1", "s2"]}
  ]
}
```

- `r`：副本数（整数 ≥ 1）；`q`：任一故障集合失效后须剩余的最少副本数（1 ≤ q ≤ r）。
- `servers[]`：`id` 唯一；`rack`/`zone` 为所属机架与可用区（描述性属性，
  故障域通过 `failure_sets` 显式指定）；`capacity` ≥ 0，`capacity < 1` 的服务器
  不能承载副本（单分片场景下每台服务器至多放 1 个副本）；`cost` 为单副本成本 ≥ 0。
- `failure_sets[]`：指定的故障集合，`servers` 为成员服务器 id 列表，
  必须引用已存在的服务器。约束：对每个故障集合，落在其成员上的副本数
  ≤ r − q。

## 输出格式（CERT.json）

可行时（`status: "optimal"`）：

- `placement`：选中的服务器 id（确定性字典序平局裁决）；`total_cost`：总成本。
- `failure_witnesses[]`：故障见证——每个故障集合失效后
  `replicas_lost` / `replicas_remaining` / `required_remaining` / `holds`。
- `statistics`：穷举统计，满足恒等式
  `combinations_evaluated == rejected_capacity + rejected_fault + feasible == C(n, r)`，
  即最优性的可复核证明。

不可行时（`status: "infeasible"`）：`placement`/`total_cost` 为 `null`，
不含 `failure_witnesses`，仅含上述穷举统计（含 `rejected_fault_by_set`
分故障集合计数），供独立重算复核。

`verify` 子命令从零重算：校验放置合法性（数量、去重、容量、成本）、
全部故障见证、枚举统计与独立重枚举一致、以及声称的成本确为最优
（或不可行声明属实）。任何篡改（成本、放置、统计、伪造不可行）都会被拒绝。

## 文件

- `replica_placement.py` — 求解器 + 验证器 + CLI。
- `test_replica_placement.py` — 23 个 unittest（含验收边界：r=2、q=1、
  两服务器同属一个故障集合时不可同选，必须至少选一个故障集合外服务器）。
- `examples/acceptance.json`、`examples/infeasible.json` — 合成示例输入。
