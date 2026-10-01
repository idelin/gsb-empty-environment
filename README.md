# 容错副本放置证书（Fault-Tolerant Replica Placement Certificate）

纯 Python 3.11 标准库实现。服务器属于机架（rack）与可用区（zone），具有容量
（capacity，最多容纳的副本数）与单副本成本（cost）。每个数据分片需在 **不同**
服务器上放置 `r` 个副本，使得任一指定故障集合整体失效后，仍至少有 `q` 个副本
存活（即位于该故障集合之外）。目标：最小化总放置成本。

求解器采用**穷举枚举**：

- 输出 `OPTIMAL` 证书时，最优性由穷举保证（证书中附枚举统计）；
- 不可行时输出 `INFEASIBLE` 及**可复核的穷举统计**（每个分片检查过的组合数、
  被故障规则拒绝的数量、容量剪枝数量等），绝不输出假证书；
- `verify` 子命令可独立复核证书，`--check-optimality` 会重新穷举以确认
  最优性 / 不可行性。

## 运行

```bash
python3.11 replica_placement.py solve  examples/instance1.json -o cert.json
python3.11 replica_placement.py verify examples/instance1.json cert.json --check-optimality
```

`solve` 在找到最优解时退出码为 0，不可行时为 1，输入非法时为 2。

## 输入格式（JSON）

```json
{
  "servers": [
    {"id": "s1", "rack": "rk1", "zone": "z1", "capacity": 2, "cost": 1}
  ],
  "fault_sets": [
    {"id": "fs1", "servers": ["s1", "s2"]}
  ],
  "derive_fault_sets": ["rack", "zone"],
  "r": 2,
  "q": 1,
  "shards": ["shard-a"]
}
```

| 字段 | 说明 |
| --- | --- |
| `servers[].id` | 唯一服务器标识（非空字符串） |
| `servers[].rack` / `zone` | 所属机架 / 可用区（可缺省，供派生故障集合使用） |
| `servers[].capacity` | 非负整数，该服务器最多容纳的副本总数（跨分片累计） |
| `servers[].cost` | 非负数值，放置一个副本的成本 |
| `fault_sets[]` | 显式指定的故障集合：`id` + 成员服务器列表 |
| `derive_fault_sets` | 可选，`"rack"` / `"zone"`，按机架/可用区自动各生成一个故障集合（id 形如 `rack:rk1`） |
| `r` | 每分片副本数，正整数 |
| `q` | 任一故障集合失效后至少存活的副本数，`0 <= q <= r` |
| `shards` | 可选，分片名列表，默认 `["shard-0"]`；多分片共享服务器容量 |

约束：同一分片的 `r` 个副本必须位于不同服务器；对每个故障集合 `F`，
分片位于 `F` 之外的副本数必须 `>= q`；每台服务器承载的副本总数不超过其容量。

## 输出格式（证书 JSON）

### 最优（`status = "OPTIMAL"`）

```json
{
  "format_version": 1,
  "status": "OPTIMAL",
  "r": 2,
  "q": 1,
  "total_cost": 11,
  "assignment": {"shard-a": ["s1", "s3"]},
  "witnesses": {"shard-a": {"fs1": ["s3"]}},
  "capacity_usage": {"s1": 1, "s2": 0, "s3": 1},
  "fault_sets": {"fs1": ["s1", "s2"]},
  "exhaustive_stats": {
    "per_shard": {"shard-a": {"servers": 3, "combinations_examined": 3,
                               "rejected_by_fault_rule": 1, "accepted": 2}},
    "joint": {"leaf_assignments_evaluated": 2,
              "branches_pruned_by_capacity": 0,
              "branches_pruned_by_bound": 0}
  },
  "optimality": "proven by exhaustive enumeration of all fault-valid placements"
}
```

- `assignment`：每个分片放置副本的服务器列表；
- `witnesses`：故障见证——每个分片、每个故障集合失效后存活的副本列表，
  其长度均 `>= q`，可直接逐项核对；
- `capacity_usage`：每台服务器实际承载的副本数，均不超过其容量；
- `exhaustive_stats`：穷举统计，可用 `--check-optimality` 复现。

### 不可行（`status = "INFEASIBLE"`）

包含 `reason`（人类可读原因）与完整的 `exhaustive_stats`：
每个分片被检查的 `C(n, r)` 组合数、被故障规则拒绝数、接受数，
以及联合搜索的容量剪枝计数。这些数字可由 `verify --check-optimality`
重新穷举复核，因此不可行结论是可验证的，而非假证书。

## 验收边界示例

`r=2, q=1`，服务器 `s1`、`s2` 同属故障集合 `fs1`：把两个副本放在
`s1`、`s2` 上时，`fs1` 失效后存活副本为 0，不满足 `q=1`；合法解必须
至少选择一台 `fs1` 之外的服务器（如 `s3`）。见 `examples/instance1.json`
与 `test_replica_placement.py` 中的对应测试。

## 测试

```bash
python3.11 -m unittest test_replica_placement -v
```

所有示例数据（`examples/`）均为合成数据。

## 复杂度说明

穷举枚举的复杂度为 `O(C(n, r))` 每分片，多分片联合搜索为其笛卡尔积
（带容量与定界剪枝）。适用于中小规模实例；大规模实例需要更强的算法
（本实现刻意选择穷举以换取可证明的最优性与可复核的不可行性）。
