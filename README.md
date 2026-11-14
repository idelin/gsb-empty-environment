# netsec：宽表查询授权组件

面向 C++20 数据库服务的可导入查询授权库。它不执行查询，而是在查询下发给存储层之前
解析查询计划、收紧扫描范围、注入租户/角色约束，并在结果返回后逐行复核分区归属。

## 威胁模型

宽表引擎允许 `ALLOW FILTERING`。攻击者提交低基数条件（例如 `category = 'ops'`）触发
全分区扫描，虽然过滤条件本身只匹配少量行，但存储层已经读到了其他租户的分区。
仅在返回结果上加租户过滤条件无法阻止这类越权读取。

组件按以下顺序阻断：

1. 查询必须包含针对**服务端配置的授权分区键**的等值条件（`=` 或 `IN`）。
2. 从条件树推导“所有匹配行可能落在哪些分区”，谓词只能收窄，不能扩大该集合。
3. 推导出的分区集合必须是授权分区的子集；否则拒绝。
4. 全表扫描、跨分区扫描、二级索引回查、聚合分别受角色策略控制。
5. 授权通过后生成收紧后的 `BoundPlan`：重写扫描节点并注入分区谓词，而不是只加行过滤。
6. 存储层返回的每一行都通过 `ResultGuard` 复核分区归属；授权变更会使在途结果守卫失效。

## 核心 API

```cpp
#include "netsec.hpp"

netsec::AuthzEngine engine;
engine.set_table_partition_key("events", "tenant_id");
engine.grant("tenant-a", "reader", {"tenant-a"});

netsec::QueryPlan plan;
plan.table = "events";
plan.scan_type = netsec::ScanType::SinglePartition;
plan.where = netsec::Condition::all({
    netsec::Condition::eq("tenant_id", netsec::Value("tenant-a")),
    netsec::Condition::eq("category", netsec::Value("ops")),
});

netsec::AuthContext identity{"tenant-a", "reader"};
auto decision = engine.authorize(plan, identity);
if (!decision) {
    // decision.code / decision.reason
}

// 必须执行 decision.plan.query，而不是原始 plan。
auto guard = engine.guard(decision);
for (const auto& row : storage_rows) {
    if (!guard.accept(row)) {
        // 丢弃越过授权分区的行
    }
}
```

`DbRow` 的分区证据优先级为：

1. `DbRow::partition`（存储适配器直接给出的分区元数据）；
2. `tenant_id` 列（允许 `table.tenant_id` 这类限定列名）。

普通查询缺少任一分区证据时按失败关闭处理并丢弃该行。单分区聚合允许结果行不带分区键，
因为聚合范围已经由 `BoundPlan` 固定到一个授权分区。

## 授权流程

`AuthzEngine::authorize` 返回 `AuthorizationResult`：

- `allowed == false`：拒绝，`code` 给出稳定原因码。
- `allowed == true`：`plan` 是 `BoundPlan`，其中：
  - `query.scan_type` 已收紧；
  - `query.scan_partition` / `query.requested_partitions` 只包含授权分区；
  - `query.where` 为 `原条件 AND 注入的分区条件`；
  - `identity` 记录服务注入的租户与角色；
  - `authorization_epoch` 用于结果复核时的授权变更检测。

调用方不得信任计划自带的租户字段。组件使用 `AuthContext` 和
`set_partition_key_column` / `set_table_partition_key` 作为权威输入。计划中的
`partition_key_column` 与权威配置不一致时直接拒绝。

`PartitionScope` 分析规则：

- `pk = value`、`pk IN (...)`：有限分区集合。
- `AND`：对有限集合取交集；`pk = a AND category = x` 仍绑定到 `{a}`。
- `OR`：有限集合并集；只要有一个分支不绑定分区键，结果就是“未绑定”，拒绝。
- `NOT`、`pk != ...`、范围比较：不产生分区绑定；除非同一 `AND` 中另有余等值条件。
- 条件不可满足、格式错误或嵌套过深：拒绝。

因此 `PK = tenant-a OR category = ops` 不能把扫描范围扩大到 `tenant-a` 之外。

## 默认策略

`RolePolicy` 默认值：

| 策略 | 默认 | 含义 |
| --- | --- | --- |
| `allow_full_table_scan` | `false` | 全表扫描；即使允许也会被改写成授权分区扫描 |
| `allow_cross_partition_scan` | `false` | 一次查询跨多个分区 |
| `allow_secondary_index` | `false` | 二级索引回查 |
| `allow_aggregation` | `true` | 聚合 |
| `allow_cross_partition_aggregation` | `false` | 跨分区聚合 |
| `allow_filtering` | `true` | 在已绑定分区内使用 `ALLOW FILTERING` |
| `max_partitions_per_query` | `1` | 单次查询分区预算 |

跨分区聚合还必须把分区键列入 `GROUP BY`，否则拒绝。按角色可用
`set_role_policy(role, policy)` 覆盖，`"*"` 角色可配置通配策略。授权按
`(tenant, role, partition)` 存储，支持 `grant`、`set_grant`、`revoke` 和
`remove_principal`。

主要拒绝原因码：`MissingPartitionEquality`、`UnauthorizedPartition`、
`FullTableScanDenied`、`CrossPartitionDenied`、`SecondaryIndexDenied`、
`AggregationDenied`、`PredicateCannotBeBounded`、`FilteringDenied`、
`AuthorizationChanged`、`InvalidPlan`、`NoGrant`、`MissingIdentity`。

## 结果复核与授权变更

`ResultGuard` 在每次 `accept`/`filter` 时检查：

- 当前授权 epoch 与决策时一致；
- 决策涉及的每个分区仍在当前 `(tenant, role)` 授权集合中；
- 行的分区值属于该决策收紧后的分区集合。

任何授权或策略变更都会递增 epoch。撤销租户后，旧守卫 `valid()` 变为 `false`，
所有行都被丢弃；重新授权后必须重新调用 `authorize` 生成新守卫。这是失败关闭语义，
避免决策与执行之间的授权变更竞态。

## JSON 查询计划

`parse_plan_json` / `parse_query_plan` 支持无第三方依赖的 JSON 子集：

```json
{
  "table": "events",
  "operation": "select",
  "scan": "single_partition",
  "partition_key": "tenant_id",
  "partition": "tenant-a",
  "allow_filtering": true,
  "where": {
    "and": [
      {"eq": ["tenant_id", "tenant-a"]},
      {"eq": ["category", "ops"]}
    ]
  }
}
```

条件语法：`eq`、`in`、`lt`、`le`、`gt`、`ge`、`ne`、`and`、`or`、`not`，
也可用 `{"column": "...", "op": "eq", "values": [...]}`。`predicates` 数组按 `AND`
组合。`plan_to_json` 可序列化计划用于审计日志。

## 构建与验证

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build --output-on-failure
```

测试覆盖：全表低基数过滤攻击、二级索引回查、跨租户聚合泄露、合法单分区查询、
`OR` 条件扩权、跨分区策略、分区键计划篡改、`ALLOW FILTERING` 策略、角色切换、
授权撤销/恢复，以及 JSON 计划解析与回放。
