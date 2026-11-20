# 宽表查询授权组件

面向 C++20 宽表数据库服务的查询授权组件。它不依赖具体存储或 RPC 框架，
以单头文件库的形式被服务导入，目标名为 `wide_table::query_auth`。

安装后可直接使用 CMake 包：

```cmake
find_package(wide_table_query_auth CONFIG REQUIRED)
target_link_libraries(your_service PRIVATE wide_table::query_auth)
```

## 防护目标

组件在查询计划进入执行器之前完成授权，并在结果返回调用方之前复核行来源。
默认策略是拒绝优先：

- 查询计划必须包含至少一个位于**必选合取条件**中的分区键等值条件。
- 等值条件必须匹配当前租户/角色被授权的精确分区。
- `OR`、`NOT` 默认拒绝；藏在 `OR` 里的键等值不会被当作读取约束。
- 全表扫描、Token 范围扫描、跨分区读取、二级索引及其回查、聚合能力均由
  `TablePolicy` 显式授予。
- 过滤条件只能在同一顶层合取中收窄已授权的读取范围。
- 租户和角色由认证上下文注入计划，调用方在计划中填写的值只用于冲突检测，
  永远不会被信任。
- 返回行必须带有分区/租户来源；结果复核会丢弃或整体拒绝越权行。
- 策略更新会递增授权代次，旧授权令牌立即失效，已排队的查询无法重放。

## 快速集成

```cpp
#include <widetable/query_auth.hpp>

namespace auth = wide::query_auth;

auth::TablePolicy events;
events.partition_key_columns = {"tenant_id", "bucket"};
events.tenant_column = "tenant_id";
events.authorized_partitions = {
    {{"tenant_id", auth::Value("tenant-a")}, {"bucket", auth::Value(1)}},
};

auth::AuthPolicy policy;
auth::RoleGrant grant;
grant.tenant_id = "tenant-a";
grant.role = "reader";
grant.tables.emplace("events", events);
policy.grants.push_back(std::move(grant));

auth::QueryAuthorizer authorizer(policy);

auth::QueryPlan plan;
plan.table = "events";
plan.access_path = auth::AccessPath::Partition;
plan.where = auth::Condition::all({
    auth::Condition::eq("bucket", auth::Value(1)),
});

const auto decision = authorizer.authorize(plan, {"tenant-a", "reader"});
if (!decision.allowed()) {
    // 拒绝执行：decision.code / decision.message
    return;
}

// 执行 decision.authorized.effective_plan。
std::vector<auth::ResultRow> storage_rows = execute(decision.authorized);

// 存储层提供 source_partition / tenant_id 来源，复核后再返回客户端。
const auto review = authorizer.recheck(decision.authorized,
                                       std::move(storage_rows));
return review.rows;
```

租户等值条件会由 `authorize()` 注入到
`effective_plan.where`，因此示例中只写 `bucket = 1` 也能绑定到
`(tenant-a, 1)` 这一精确分区。

## 查询计划 JSON

服务可在解析 CQL/AST 后使用 `parse_query_plan()` 生成 `QueryPlan`。解析器
无第三方依赖，并对外部输入做深度、类型、重复键和取值校验。

```json
{
  "kind": "select",
  "table": "events",
  "access_path": "partition",
  "partition_key": {"tenant_id": "tenant-a", "bucket": 1},
  "where": {"and": [
    {"column": "region", "op": "eq", "value": "us"}
  ]}
}
```

支持 `partition`、`secondary_index`、`full_scan`、`token_range` 访问路径，
以及 `and` / `or` / `not` 条件和 `count` / `sum` / `avg` / `min` / `max`
聚合。`tenant_id` 或 `role` 若出现在计划中，必须与认证上下文一致，否则返回
`identity_mismatch`。

## 策略要点

`TablePolicy` 中与攻击面直接相关的开关：

| 字段 | 默认值 | 作用 |
| --- | --- | --- |
| `allow_full_scan` | `false` | 允许全表扫描，同时还需 `allow_cross_partition_scan` |
| `allow_token_range` | `false` | 允许 Token 范围扫描 |
| `allow_cross_partition_scan` | `false` | 允许一次查询触及多个分区 |
| `allow_secondary_index` | `false` | 允许二级索引访问 |
| `allow_secondary_index_backlookup` | `false` | 允许索引键回查基表 |
| `require_full_index_binding` | `true` | 索引列必须全部有等值条件，避免索引全扫 |
| `allow_aggregation` | `false` | 允许聚合 |
| `allow_cross_partition_aggregation` | `false` | 允许跨分区聚合 |
| `allow_disjunctive_filters` | `false` | 允许 `OR` / `NOT` 过滤 |
| `require_all_partition_key_components` | `false` | 是否强制绑定全部组合分区键 |
| `max_partitions_per_query` | `1` | 单次查询可触及的授权分区上限 |

即使授予了全表扫描能力，`recheck()` 仍会用令牌中的精确授权分区集复核每一行；
`ResultReviewMode::DropUnauthorized` 只输出授权行，
`ResultReviewMode::FailClosed` 在任何越权行出现时返回空结果。

## 测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：

- 低基数过滤 + `ALLOW FILTERING` 的全表扫描攻击；
- 二级索引访问与索引回查策略；
- 无分区约束/跨分区聚合泄露；
- 合法精确分区查询及混入非法租户/分区的结果复核；
- 分区撤销、角色撤销后的新查询拒绝和旧令牌失效。
