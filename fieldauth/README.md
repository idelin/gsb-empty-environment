# fieldauth — 字段级更新授权库

对一次**多字段批量更新**做**原子授权校验**：要么整批允许，要么整体拒绝，
绝不部分生效。纯函数式校验，不触碰存储；调用方仅在 `Decision.Allowed`
为 true 时提交 `Decision.New`。

## 核心概念

### 双快照条件（Old / New）

每条规则的条件显式声明读取哪个快照：

- `Old`：更新前的**已提交**值。授权类条件（“谁有权改”）必须读 Old。
- `New`：应用本次更新（含派生重算）后的**待提交**值。适合结果一致性、
  跨字段不变量（如 `budget <= limit`）。

```go
Rule{
    Field: "email",
    Name:  "email-admin-only",
    Conds: []Cond{{
        View: Old, // 关键：读旧快照，未提交的新身份不参与授权
        Desc: "only an admin (committed state) may change email",
        Func: func(rec Record, a Actor) bool { return rec["role"] == "admin" },
    }},
}
```

### 防自我提权

同一批次里 `{role: "admin", email: "x"}` 不会用“新身份”通过校验：
`email` 的守卫条件读 Old 快照，看到的仍是提交前的 `role: "user"`，
整批被拒绝。见 `TestSelfEscalationBatchDenied`。

### 派生字段参与检查

`Deriver` 声明派生逻辑，引擎按依赖拓扑序（成环则构建报错）在校验前
自动重算；派生字段的值变化同样触发其守卫规则——改 `first` 联动
`display_name`，没有相应权限照样被拒。无法通过“只改源字段”绕过
派生字段的守卫。

### 最小拒绝解释

拒绝时 `Decision.Minimal` 给出 `Update.Set` 的**极小子集**：该子集单独
提交仍被拒绝，且再删任意一个赋值即变为允许（删除法求 MUS）。
无关赋值被剔除，组合冲突（如 `budget` 与 `limit` 需同时出现才违规）
则完整保留，并附带该子集产生的具体违反项。

## 使用

```go
e, _ := fieldauth.NewEngine(rules, derivers)
d, _ := e.Check(actor, oldRecord, fieldauth.Update{Set: patch})
if d.Allowed {
    store.Commit(d.New) // 原子提交整批结果
} else {
    log.Println(d.Violations, d.Minimal) // 拒绝原因 + 最小解释
}
```

## 测试

```sh
GOCACHE=/private/tmp/gocache go test ./...
```

覆盖：自我提权拒绝、已提交管理员放行、整批原子性、派生字段重算与
检查、New 快照不变量、最小解释（单字段与组合两种形态）、输入不可变、
派生环检测。
