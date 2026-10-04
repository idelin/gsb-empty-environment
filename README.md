# fieldauth

字段级更新授权库：对多字段修改做**原子校验**，全部规则通过才放行，否则整体拒绝并给出**最小拒绝解释**。

## 核心概念

- **原子性**：`Authorize(actor, old, patch)` 对整批字段修改一次性校验。要么返回完整的提交后记录，要么返回 `Denial`，绝不产生部分结果。
- **旧值 / 提交后值条件**：条件通过 `OldCond`（读提交前镜像）、`NewCond`（读提交后镜像）、`BothCond`（前后对比）构造，声明即强制；拒绝解释中会标注条件属于哪一类。
- **身份取自旧值**：`RoleResolver` 永远在补丁应用**之前**以旧记录解析操作者角色。因此"先把自己改成管理员再改敏感字段"的同一批补丁，仍按旧的非管理员身份授权，被拒绝。
- **派生字段**：`Derive` 注册的字段在每次校验时按依赖拓扑序自动重算，重算结果与普通字段一样触发守卫规则；派生字段不允许直接 patch。
- **最小解释**：`Denial.Failures` 只列失败的条件；`Denial.Fields()` 给出涉及的变更字段；`Denial.MinimalFields()` 给出需要撤回的最小补丁字段集（派生字段违规会回溯到引起它的基础字段）。

## 示例

```go
e := fieldauth.NewEngine(func(actorID string, old fieldauth.Record) []string {
    if old["id"] == actorID {
        return []string{old["role"].(string)} // 角色取自旧记录
    }
    return nil
})
e.Guard(fieldauth.Rule{
    ID:     "salary-guard",
    Fields: []string{"salary"},
    Conds:  []fieldauth.Cond{fieldauth.ActorHasRole("admin")},
})

old := fieldauth.Record{"id": "alice", "role": "user", "salary": 100}

// 自我提权攻击：同一批补丁先改 role 再改 salary —— 整体拒绝。
_, denial, _ := e.Authorize("alice", old, fieldauth.Patch{"role": "admin", "salary": 5000})
fmt.Println(denial)               // 失败条件 + 需撤回的字段 [role salary]
```

运行测试：`go test ./...`
