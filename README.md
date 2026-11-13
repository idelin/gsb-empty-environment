# 租户隔离的共享前缀 KV 缓存

`example.org/netsec/q0149` 是一个可导入的 Go 包，用于修复"缓存键只含 token
序列，租户 A 命中租户 B 私有系统提示 KV"一类问题。

## 命中条件

`Identity` 完整绑定以下字段，并按带域分隔、长度前缀的规范编码比较：

- `TenantID`
- `ModelDigest`（包含量化版本摘要）
- `AdapterDigest`
- `TokenizerDigest`
- `DecodePosition`
- `TokenPrefixDigest`（`PromptDigest` 非零时把私有系统提示摘要折入）
- `SafetyPolicyGeneration`
- `KVLayoutVersion`

每个条目保存每层 K/V 块句柄、不可变 token 范围、创建代次、引用租约，以及
`HMAC-SHA256(hostKey, canonicalIdentity || tokenRange || blockDigest)`。查找时必须
同时满足规范身份完全相同、块内容摘要一致、MAC 校验通过；否则按 miss 处理。

## publicScope

跨租户共享必须显式注册，默认关闭：

```go
digest := netsec.CanonicalTokenPrefixDigest(tokens, promptDigest)
c, _ := netsec.New(netsec.Config{
    HostKey: hostKey, // >= 32 bytes
    PublicScopes: []netsec.PublicScope{{
        Name:               "public-system",
        TokenPrefixDigests: [][32]byte{digest},
    }},
})
```

只有注册过的“完整规范 token 前缀摘要”才能使用该 public scope。公开条目仍绑定
token 原文摘要、模型、适配器、tokenizer、解码位置、策略代次和 KV 布局；租户 ID
仅在 public scope 中被规范化掉。公开前缀之后的私有后缀采用写时复制，新块从父条目
结束位置开始，旧条目不可原地追加。

## 生命周期

```go
lease, ok, err := c.Lookup(ctx, req)
if ok {
    defer lease.Release() // 取消或失败路径同样只减租约
}
```

- 块 `Fill` 后不可变；池槽每次分配递增 generation，旧句柄的迟到 `Fill` 返回
  `ErrStaleBlock`/`ErrBlockNotFilling`，不能覆盖复用槽中的新条目。
- 最后一个租约归还后，块内字节先清零，槽位才标记为 free 并可复用。
- `Builder.Abort` 或取消的 `Commit` 释放本次构建的全部分配和父前缀 pin。
- 扩展通过 `Extend`/`BeginExtend` 生成新条目；父条目范围和块句柄保持不变。

## 弱键审计

`SearchWeakCacheKey` 对待审 `cacheKey` 回调做有界有限差分搜索：按 token 长度
递增，改变租户、系统提示摘要、模型/量化、适配器、tokenizer、位置、策略代次、
布局和 public scope，寻找“弱键相同、规范身份不同、且标记 KV 解码出另一租户秘密
token”的最短样例。

```go
finding, err := netsec.SearchWeakCacheKey(netsec.TokenOnlyWeakKey, netsec.AuditOptions{
    MaxTokens: 3,
})
// finding.Reason() 给出被忽略的规范身份维度
// finding.MarkedKV 可经 DecodeMarkedKV 验证泄漏的秘密 token
```

测试覆盖：同 token 不同系统提示摘要、模型量化摘要变化、适配器切换、
publicScope 边界、取消/填充竞争、槽位复用、MAC/块篡改、写时复制和最后租约清零。

运行：

```sh
go test ./...
go test -race ./...
```
