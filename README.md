# 工作负载联合身份模板碰撞

`example.org/netsec/q0096` 是一个可导入的 Go 包，用于把已验签的 OIDC
声明映射成有界云主体，并在发布配置前审查不同声明元组是否会发生主体碰撞。
令牌签名、过期时间和时钟偏差校验仍由宿主完成。

## 快速使用

```go
config := netsec.ProviderConfig{
    ProviderID: "git-cloud",
    Issuer:     "https://issuer.example",
    Audiences:  []string{"cloud-audience"},
    Template:   "oidc:{repository}/{ref}",
    Languages: map[netsec.ClaimName]netsec.AllowedLanguage{
        netsec.ClaimRepository: netsec.EnumLanguage("org/repo", "org/other"),
        netsec.ClaimRef:        netsec.PrefixSuffixLanguage("refs/heads/", "main", "prod"),
    },
}

policy, err := netsec.Publish(config)
if err != nil {
    return err // configuration, collision, or audit-budget failure
}

principal, err := policy.MapToken(ctx, rawToken, func(ctx context.Context, raw string) (netsec.Claims, error) {
    // The host must verify the signature and time here.
    return netsec.NewClaims(verifiedValues), nil
})
if err != nil {
    return err
}

allowed, err := policy.Authorize(ctx, principal, func(ctx context.Context, p netsec.Principal) (bool, error) {
    return checkCloudPermission(ctx, p.Name())
})
```

## 映射规则

- `iss` 必须与 `ProviderID` 对应配置中的 `Issuer` 逐字节相等，不做大小写、空白或 URL 规范化。
- `aud` 至少命中一个 `Audiences` 值。`aud` 有多个值时 `azp` 必填，且 `azp` 必须属于允许 audience 集合。
- `ClaimPatterns` 可把 `sub`、`namespace`、`serviceAccount`、`repository`、`ref` 映射到实际声明名。空字段使用同名声明。
- 模板只允许已知 `{claim}` 占位符。声明缺失、不是字符串或为空时拒绝映射。
- 值按 UTF-8 字节百分号编码：仅 ASCII 字母、数字、`-`、`.`、`_`、`~` 保留，其余字节写成大写 `%HH`。不裁剪、不改大小写、不做路径规范化、不做二次解码。
- 模板字面量原样进入主体；最终主体按 `MaxPrincipalBytes` 限制（默认 1024 字节）。

## 碰撞审查

`Languages` 中的每个声明都是审计维度。有限语言可由 `EnumLanguage` 枚举，
或由 `PrefixSuffixLanguage` 给出固定前缀和有限后缀集合。空字符串永远不是
合法值，运行时声明也必须落在其允许语言内。

`Audit` 在 `AuditBudget`（默认 1,000,000 个元组）内穷举确定性转导的有限
输入域。发现重复主体时返回按 `sub`、`namespace`、`serviceAccount`、
`repository`、`ref` 顺序排列的字典序最小碰撞对；搜索无法在预算内闭合时
返回 `ErrAuditBudget`，`Publish` 同样拒绝发布。不会因搜索未完成而假定映射
唯一。

```go
collision, err := netsec.Audit(config)
if errors.Is(err, netsec.ErrAuditBudget) {
    return err
}
if collision != nil {
    log.Printf("collision at %q: %#v vs %#v",
        collision.Principal, collision.First, collision.Second)
}
```

## 运行时绑定

`Publish` 生成或接受显式配置代次。`Principal` 是不透明值，内部绑定
`providerID`、代次和完整配置摘要；`Authorize` 只把属于当前一致代次的
主体交给权限回调。每个 `Policy` 的映射缓存独立，键还包含 provider、
代次和配置摘要，因此不会跨代复用。
