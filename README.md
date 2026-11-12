# 证明路径校验器（Go）

这个包供解析宿主调用，用来校验“已验签”的别名跳和不存在证明，并输出可审计的完整授权路径。包本身不做密码学验签；它检查的是验签之后仍可能被缓存解析器弄错的部分。

## 核心约束

- 每个 `Proof` 必须声明 `Zone`。校验器用显式配置的有限区树计算该名字真正的授权区（最长后缀匹配），并要求两者完全相等。攻击者控制的区不能用自己的否定证明覆盖另一区。
- 每个别名跳都必须由别名 owner 名字的授权区证明。父区不能替已委派的子区签名，跨区伪造的中间跳会被拒绝。
- 有效期采用半开区间 `[ValidFrom, ValidUntil)`。过期或尚未生效的跳不会被使用；如果它在链条中间，整条路径返回 `Unknown`。
- `NXDomain` 和 `NoData` 只作用于路径终点名字。`NoData` 还只作用于终点名字的查询类型。`Result.CacheKey()` 永远不会只以起点名字作键。
- 别名循环、缺证、区树外名字、签名区不匹配、类型不匹配、证据冲突都返回 `Unknown`，不会返回否定答案。
- `Enumerate` 独立枚举所有受信路径并保留每条路径的终态；`Resolve` 只有在所有可用的受信路径给出一致答案时才返回确定结果。

## 使用

```go
res := taskpkg.NewResolver(
    []string{"example.com", "sub.example.com", "attacker.test", "victim.test"},
    now,
)

got := res.Resolve(
    taskpkg.Query{Name: "a.example.com", Type: taskpkg.TypeA},
    []taskpkg.Proof{
        {
            ID:         "alias-1",
            Kind:       taskpkg.ProofAlias,
            Name:       "a.example.com",
            Target:     "b.example.com",
            Zone:       "example.com",
            Type:       taskpkg.TypeCNAME,
            ValidFrom:  from,
            ValidUntil: until,
        },
        {
            ID:         "nodata-1",
            Kind:       taskpkg.ProofNoData,
            Name:       "b.example.com",
            Zone:       "example.com",
            Type:       taskpkg.TypeA,
            ValidFrom:  from,
            ValidUntil: until,
        },
    },
)

if got.Status != taskpkg.StatusNoData {
    // 缺证、过期、跨区伪造、循环或冲突时 Status 为 Unknown。
}
scope, _ := got.NegativeScope()
key, _ := got.CacheKey()
// scope.Name == "b.example.com"（终点），而不是 "a.example.com"（起点）
```

需要审计全部候选路径时使用 `res.Enumerate(query, proofs)`。返回的 `Report` 包含 `Paths`、`Rejected` 和 `Ambiguities`；被拒绝的证明带有 `zone-mismatch`、`expired`、`not-yet-valid`、`type-mismatch` 等诊断码。

## 字段语义

- `Proof.Type`：该证明所证明的记录类型。别名使用 `CNAME`；`NoData` 使用缺失的类型；`NXDomain` 留空表示名字级否定，填写具体类型则只限该查询类型。
- `Proof.QType`：可选的查询类型绑定，空值或 `*` 表示不限制。别名上的 `QType=CNAME` 也视为不限制，因为 CNAME 本身适用于所有非 CNAME 查询。
- `Proof.Value`：正向终点记录的载荷，仅 `ProofAnswer` 使用。
- `Verifier.Zones`：必须显式包含需要的区（包括根区 `"."`，如果确实信任根区）。没有隐式根区，避免任意名字默认获得权威。

零值 `ValidFrom` / `ValidUntil` 表示该端无界。生产环境若要求每个证明都必须有明确到期时间，调用方应在传入前自行拒绝零值。

## 测试覆盖

`verifier_test.go` 用固定时间与有限名称树覆盖：

- 攻击者区伪造 `www.victim.test` 的 `NXDomain` / `NoData`，以及直接查询和经别名到达两种入口；
- 中间别名由错误区签名、父区替委派子区签名；
- 过期的中间别名继续指向合法终点否定；
- 两节点、三节点和自环别名；
- `NoData` / `NXDomain` 的类型与终点作用域，以及负缓存键；
- 多条受信路径并存时的独立枚举、冲突检测，以及每条路径的授权区逐步核验。
