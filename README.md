# 迁移脚本规划器 (migrateplanner)

按读写对象与前置状态自动编排数据库迁移步骤的 Go 工具。

## 核心概念

- **Op（操作）**：声明 `reads`/`writes`（读写对象，如 `table:users`、`col:users.email`）、
  `requires`/`provides`（前置/建立的状态）、`needs_write_stop`（是否需停写）、
  `rollback`（回滚语句，为空即不可逆）。
- **Step（步骤）**：计划中的一个执行单元，组内操作互不冲突、可并行。
- **Plan（计划）**：有序步骤序列，附带停写窗口数与不可逆边界列表。

## 调度规则

1. 由 `requires`/`provides` 推导依赖边；冲突（写集与对方读/写集相交）但无依赖的
   操作按输入顺序补边，保证结果确定；依赖成环则报错。
2. ASAP 拓扑分层，同层冲突的操作顺延到下一层。
3. 在依赖允许的最晚层之前，把停写操作聚拢到已有停写层，**最小化停写窗口数**。
4. 合并相邻可合并的步骤；总步数超过 **20** 步报错。
5. 无回滚语句的操作所在步骤标记为**不可逆边界**。
6. 规划结果会再用 `ValidateSequence` 自检，确保
   「新增列 → 填充数据 → 建立约束」之类的顺序约束不可能被错序。

## 使用

```sh
go run ./cmd/planner plan.json   # 或从 stdin 读取
go test ./...
```

输入 JSON：

```json
{
  "preexisting": ["table:users"],
  "ops": [
    {"id": "add-email", "kind": "add_column",
     "reads": ["table:users"], "writes": ["col:users.email"],
     "provides": ["col:users.email:exists"],
     "rollback": "ALTER TABLE users DROP COLUMN email"}
  ]
}
```

Go API：`planner.Build(ops, preexisting)` 返回 `*Plan`；
`planner.ExpandContract(table, column)` 生成「加列 → 填充 → 加约束」三操作序列；
`planner.ValidateSequence(ops, preexisting)` 独立校验任意执行序列的顺序合法性。
