# 角色链中的会话标签权限提升

临时角色会话可把标签传给下一跳，攻击者若覆盖受保护标签或把非传递标签变成传递标签，会获得更宽资源条件。为Go身份宿主实现角色链标签包：每跳输入当前会话标签映射、当前transitive键集合、请求新增标签、请求transitive键、目标角色固定标签、目标角色允许调用者覆盖的键集合及链深及每跳roleID。键和值是非空UTF-8且有字节上限，规范比较不做大小写折叠；同一来源重复键拒绝。下一跳先继承当前标签中键在当前transitive集合的项，再加入目标固定标签；固定标签冲突一律拒绝；请求新增标签仅可使用允许覆盖键，覆盖继承值时保留来源链。下一跳transitive集合只能是请求transitive键与“继承时已传递或本跳新加且目标允许传递”的交集，调用者不能把固定标签或原非传递标签升级。每跳生成标签来源证明，深度上限4；资源策略回调根据最终标签判断，但只接不可变映射。搜索至多4跳的候选请求，找待审实现允许而正确模型拒绝且策略从deny变allow的最短标签提升见证。覆盖大小写近似键、固定department、覆盖继承project、同一roleID成环拒绝；覆盖去掉再重加及两条链合流，普通值变化但策略不变不算安全突破。

## 实现概览

- 安全参考模型：`ApplyHop`、`NextHop`、`Session.Advance`。
- 单跳输入：`HopRequest`（别名 `HopInput`、`HopSpec`、`Request`）。
- 结果：`HopResult`，包含最终标签、transitive 集合、持久非传递污点 `NonTransitive`、每标签 `Proof` 和下一跳 `Session`。
- 不可变策略视图：`LabelView`；兼容回调 `PolicyFunc(func(map[string]string) bool)`。回调拿到的是防御性副本，无法修改会话状态。
- 待审实现：`AuditedHop` 与 `AuditedHopWithConfig`；`AuditConfig.Flaws` 可单独启用大小写折叠、固定标签覆盖、传递性升级、角色环、来源链重置、合流覆盖等缺陷。
- 差分搜索：`FindEscalation`（别名 `FindWitness`、`SearchEscalation`）使用 BFS，最多 4 跳，返回最短 `Witness`；`Scenario` 提供受限候选空间。
- 合流：`MergeSessions` 对冲突值、固定标签冲突拒绝，并对共享标签的 transitive 取交集；审计合流可通过 `MergeSessionsWithConfig` 启用缺陷。

## 关键语义

- 键比较为精确字节比较，不折叠大小写、不裁剪、不做 Unicode 规范化。
- 固定标签与继承键冲突、与请求键冲突时一律拒绝；固定标签永远不能被调用者升级为 transitive。
- 只有目标允许覆盖的精确键可被请求。覆盖继承值时追加 `OriginOverride`，完整保留历史来源链。
- 非传递键一旦出现即持久记入 `NonTransitive`/`TaintedKeys`；之后即使被删除再重加，也不能再升级为 transitive。
- 初始/新增的非传递标签同样进入污点集合，避免“删除再重加”洗白。

## 示例

```go
res, err := netsec.ApplyHop(netsec.HopRequest{
    CurrentLabels:       map[string]string{"project": "alpha"},
    CurrentTransitive:   []string{"project"},
    RequestedLabels:     map[string]string{"project": "beta"},
    RequestedTransitive: []string{"project"},
    FixedLabels:         map[string]string{"department": "eng"},
    OverrideKeys:        []string{"project"},
    Depth:               1,
    RoleID:              "role-b",
})
```

```go
w, err := netsec.FindEscalation(
    netsec.NewSession(map[string]string{"department": "guest"}),
    []netsec.HopRequest{{
        RequestedLabels: map[string]string{"department": "admin"},
        FixedLabels:     map[string]string{"department": "eng"},
        OverrideKeys:    []string{"department"},
        RoleID:          "role-b",
    }},
    netsec.PolicyFunc(func(labels map[string]string) bool {
        return labels["department"] == "admin"
    }),
)
```
