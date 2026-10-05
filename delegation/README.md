# delegation — 授权链校验库

委托图 + 时点校验。子授权可以继续再授权，但其动作、资源、有效期
必须被父授权严格覆盖（attenuation）；撤销上游立即使整棵后代子树
失效，且历史时点查询结果保持不变。

## 核心语义

- **代次（generation）**：同名授权重发自动生成新代次
  （`ID + Generation` 唯一标识一个授权）。子授权固定引用父的某个
  代次，旧代次被撤销后重发不会复活旧的子授权。
- **委托图**：一个授权可引用多个父授权（权限取交集），全部父链
  合法时授权才可用。
- **撤销即时效**：`Revoke` 记录时间戳，任何 `t >= RevokedAt` 的
  查询都会沿链判定后代失效；`t < RevokedAt` 的历史查询不受影响。
- **最短失效原因链**：`Validate` 以 BFS 向上搜索，返回从被查询
  授权到最近失败祖先的最短链（`Verdict.Chain`），并给出
  `ReasonCode` 与可读 `Detail`。

## 失效原因

`ReasonGrantNotFound` / `ReasonParentMissing`（父链缺失）、
`ReasonRevoked`、`ReasonNotYetValid` / `ReasonExpired`、
`ReasonActionExceedsParent` / `ReasonResourceExceedsParent` /
`ReasonValidityExceedsParent`（子超出父）、`ReasonCyclicDelegation`。

## 用法

```go
s := delegation.NewStore()
root, _ := s.Issue(delegation.Grant{
    ID: "cap", Issuer: "root", Subject: "alice",
    Actions: []string{"read"}, Resources: []string{"fs:/data/*"},
    NotBefore: from, NotAfter: to,
})
child, _ := s.Issue(delegation.Grant{
    ID: "cap-ro", Issuer: "alice", Subject: "bob",
    Actions: []string{"read"}, Resources: []string{"fs:/data/*"},
    NotBefore: from, NotAfter: to,
    Parents: []delegation.GrantRef{root.Ref()},
})

v := s.Validate(child.Ref(), time.Now())
if !v.Valid {
    fmt.Println(v) // invalid: grant revoked (...) via cap-ro#1 -> cap#1
}
```

父集合中的 `"*"` 为通配符，表示允许任意动作/资源。

## 测试

```sh
go test ./...
```
