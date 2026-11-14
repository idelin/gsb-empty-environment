# 重复查询参数在共享缓存里折叠成一份

内容分发网络的多租户共享缓存按规范化URL作键，源站却按原始查询解析；攻击者提交`?role=user&role=admin`，缓存键折叠成一个值而源站取另一个，受害者命中攻击者写入的版本。请实现供Go边缘缓存调用的可导入缓存键与响应存储组件。键由冻结的外部请求目标、规范化查询（每个名称的取值按出现次序保留、重复名不折叠）、协商头白名单及站点与租户身份共同计算；带`Vary`或`Authorization`的响应按对应请求头分桶。写入前核对源站回传的键摘要与实际请求一致，`private`、`no-store`及带Set-Cookie的响应不得进入共享缓存；请求与响应的规范化版本不一致时按未命中处理。用重复参数不同次序、同名不同值、租户同名路径、响应头大小写和合法静态资源命中验证攻击者写入不会返回给另一用户。

## 实现

包提供两个可独立导入的组件：

- `KeyBuilder` 从冻结的外部请求目标计算不可变 `Key`。查询参数按名称排序；同名参数的全部取值保持出现顺序，不折叠、不去重。站点、租户、规范化版本和协商头白名单均参与长度前缀化的 SHA-256 输入。
- `Store` 保存响应头、状态和响应体。`Vary` 中的每个请求头形成独立变体；请求带 `Authorization` 时始终单独分桶。写入前必须校验源站摘要，摘要缺失、错误或版本不一致时拒绝写入。

共享缓存策略会拒绝 `private`、`no-store`、`no-cache`、`Set-Cookie`、`Vary: *`、非 GET 请求、`Range` 请求以及不可缓存的状态码。命中时返回独立的响应头和响应体副本，避免调用方修改已缓存内容。

## 用法

```go
identity := netsec.Identity{Site: "edge.example", Tenant: "tenant-42"}
builder := netsec.NewKeyBuilder(netsec.KeyConfig{
    Identity:             identity,
    NormalizationVersion: "v1",
    NegotiationHeaders:   []string{"Accept", "Accept-Encoding", "Accept-Language"},
})
store := netsec.NewStore(builder, netsec.StoreOptions{
    MaxEntries:   4096,
    MaxBodyBytes: 8 << 20,
})

key, err := builder.BuildFor(req, identity)
if err != nil {
    // Invalid request target/query: bypass the cache.
}

// The origin returns HeaderCacheKeyDigest on its response. If the digest is
// supplied out-of-band, pass it as the third Put argument instead.
resp.Header.Set(netsec.HeaderCacheKeyDigest, key.Hex())
if err := store.PutFor(req, identity, resp, ""); err != nil {
    // ErrNotCacheable, ErrKeyDigestMismatch, or another policy error.
}

hit, ok := store.GetFor(req, identity)
if ok {
    defer hit.Body.Close()
}
```

## 验证

```sh
go test ./...
go test -race ./...
```
