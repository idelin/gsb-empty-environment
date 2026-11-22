# tokenmigrate

`tokenmigrate` 是一个面向 Go 令牌化宿主的租户合并/拆分迁移库。它把迁移拆成三件事：

1. `Build`：验证输入、计算承诺等值类、选择最少重发集合、生成兼容读取表和迁移证书。
2. `Plan.Lookup`：按“目标租户 + 发行域 + 代次 + 旧令牌”还原旧读取。
3. `Plan.Materialize` / `Migrate`：在副本上生成新的签名发放记录和双向分片映射；任一步失败都不修改输入。

```go
plan, err := tokenmigrate.Build(input, tokenmigrate.Options{
    At:        migrationTime,
    Verifier:  hostVerifier,
    Allocator: hostAllocator, // 只有需要重发时必填
})
if err != nil {
    return err
}

read, err := plan.Lookup(tokenmigrate.ReadRequest{
    Tenant: "merged-tenant",
    Origin: tokenmigrate.Scope{Domain: "old-tenant", Generation: 3},
    Token:  "legacy-token",
    At:     requestTime,
})
if err == tokenmigrate.ErrNotFound {
    // 不存在、已过期、域外、无权限、作用域不符都走这里。
}

result, err := tokenmigrate.Migrate(input, opts)
```

## 输入模型

- `Domains` 是有限域集合；每个域可带有效期。
- `Changes` 是带有效期的 `MergeChange` 或 `SplitChange`。`Options.At` 选择生效变更。
- `Shards` 是 `token -> commitment` 和 `commitment -> []token` 的双向索引；同一作用域内一个令牌只能绑定一个承诺。
- `Issuance` 是签名的发放记录。令牌只在 `(Domain, Generation)` 内唯一。
- `Proofs` 是承诺等值证明。只有 `Verifier.VerifyEquality` 接受、且包含两个不同签名方的证明才会合并承诺类。

拆分必须使用 `Explicit: true` 和明确的 `Records` 子集；空子集是合法的，目标作用域不会继承父域任何读取。合并可以整体迁移，也可以显式选择子集。`Mapping.Generation == 0` 表示沿用旧记录的代次。

## 最少重发与下界

对每个目标作用域和旧令牌，令同组记录数为 `N`，按已验证等值证明得到承诺类，类权重为 `w_i`。

同一目标令牌不能同时表示两个不同的承诺类。因此最多只能保留一个类，其他类中的每条记录都必须换令牌：

```text
local lower bound = N - max_i(w_i)
total lower bound = sum over scoped token groups
```

库保留权重最大的类并重发其余类，所以该下界可达。权重相同不能区分时，比较候选方案排序后的重发记录 ID 序列，选择字典序最小者；因此结果确定且可复现。

`Plan.Certificate` 包含：

- 每个令牌组的类权重、保留类、局部下界、实际重发 ID；
- 全局 `ReissueLowerBound`、`ReissueCount`、`Optimal`；
- 输入摘要和计划摘要；
- 目标作用域占用、空作用域、读取表行数；
- 签名、双向分片、目标令牌唯一性、授权读取、域外不可见、空作用域隔离、下界可达等检查项。

`Certificate.Validate` 在证书不可行或未达到下界时返回错误。

## 兼容读取与不可枚举

旧读取使用三元组 `(Tenant, Origin, Token)`。`Origin` 必须包含发行域和代次，因为令牌只在这些边界内唯一。合并后不同来源可能出现同令牌异承诺，所以不带 `Origin` 的读取不能安全猜测：

- `Plan.Lookup` 对每个获准旧读取做精确作用域还原；
- `Plan.LookupUnique` 仅在租户下恰好只有一个安全解释时成功；
- 不存在、已过期、作用域错误、跨租户、歧义全部返回同一个 `ErrNotFound`，不会返回 `forbidden` 或数量信息。

读取表内部条目只能通过 `CompatibilityEntries` 以管理面副本导出，避免宿主误把“存在但无权”暴露给租户。`Plan.Assignments`、证书和导出的表都是管理面数据，不应直接返回给租户；`Tenant` 参数必须来自已认证的读取上下文。

## 失败回滚

`Build` 和 `Materialize` 都是纯函数：

- 验证、分配令牌或重新签名失败时不返回部分结果；
- 输入 `Input` 不会被修改；
- 成功后返回新的 `Snapshot`，其中分片被规范化为每个作用域一个双向索引。

因此宿主可以先持久化计划/证书，再以事务方式替换快照；迁移服务失败时继续使用旧快照即可。

## 签名

库提供 `HMACVerifier`、`HMACIssuer`、`HMACAllocator` 作为可直接使用的 HMAC-SHA256 实现，也接受宿主的 `Verifier`、`Issuer`、`TokenAllocator` 接口。生产环境应使用密钥管理服务或不泄露记录 ID 的随机令牌源；`DeterministicAllocator` 仅供测试和可复现实验。

## 测试

```sh
go test ./...
go vet ./...
```

测试覆盖合并同令牌异承诺、等权 ID 序列择取、传递等值、多个变更落入同一目标域、拆分明确子集、空子集隔离、旧别名还原、域外统一拒绝、签名/分片校验、重发与签名失败回滚，以及 3 个承诺的全部集合划分和小映射穷举对照。
