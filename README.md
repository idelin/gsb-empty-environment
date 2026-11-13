# PassRole加创建资源的提权路径

拥有 PassRole 和创建函数权限的主体，可以让代码以更高权限角色运行。本包（`package netsec`，模块
`example.org/netsec/q0810`）提供可导入的提权路径求解器。

## 语义

- 输入入口主体的策略集合与全部角色（角色自带策略和信任策略），输出经
  `iam:PassRole` + `lambda:CreateFunction` + `lambda:InvokeFunction` 可到达的角色。
- 每一跳要求当前身份同时具备 `lambda:CreateFunction` 与 `lambda:InvokeFunction`，
  允许把下一个角色 Pass 给 `lambda.amazonaws.com`，且该角色信任 Lambda 服务主体。
- 显式 `Deny` 优先于任何 `Allow`；策略按语句逐条求值，Deny 命中即拒绝。
- Action 大小写不敏感，Action 与 Resource 均支持 `*`、`?` 通配。
- `iam:PassedToService` 只对所列服务生效：限定为 Lambda 时仅 Lambda 传递可用，
  限定为其他服务时不影响 Lambda 传递；服务值同样支持通配。
- 最多传递 3 级角色（`MaxPassDepth = 3`），每个角色按最短深度记录一次，
  结果按角色 ARN 排序，并返回 `Depth` 与完整 `Path`。

## 类型与入口

- `Policy` / `Statement` / `Conditions`：身份策略，含 Allow/Deny、通配 Action/Resource
  与 `PassedToService` 条件。
- `TrustPolicy` / `TrustStatement` / `Role`：角色信任策略与角色自身策略。
- `ReachableRoles(entryPolicies, roles) []Reachability`：BFS 求可达角色集合。
- `CanPassRole`、`CanCreateAndInvokeLambda`、`Role.TrustsService`：单步判定，
  均可在自定义分析流程中直接调用。

## 示例

```go
entry := []netsec.Policy{
    {Statements: []netsec.Statement{{
        Effect:    netsec.EffectAllow,
        Actions:   []string{"lambda:*"},
        Resources: []string{"*"},
    }}},
    {Statements: []netsec.Statement{{
        Effect:    netsec.EffectAllow,
        Actions:   []string{netsec.ActionPassRole},
        Resources: []string{"*"},
    }}},
}

admin := netsec.Role{
    ARN:   "arn:aws:iam::123456789012:role/Admin",
    Trust: netsec.TrustPolicy{Statements: []netsec.TrustStatement{{
        Effect: netsec.EffectAllow, Services: []string{netsec.ServiceLambda},
    }}},
}

for _, reachable := range netsec.ReachableRoles(entry, []netsec.Role{admin}) {
    // reachable.RoleARN / reachable.Depth / reachable.Path
    _ = reachable
}
```

## 验证

`go test ./...` 覆盖通配 Resource、显式 Deny 覆盖、信任策略不含 Lambda、
两级/三级传递与深度上限、`iam:PassedToService` 服务限定及环去重。
