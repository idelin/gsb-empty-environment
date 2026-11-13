# 节点镜像缓存命中的拉取凭据复核

节点上已经拉取过租户 A 的私有镜像，租户 B 的 Pod 只要写出同一个镜像引用，在 IfNotPresent 策略下就能直接用上缓存，根本不必带拉取凭据。Go节点代理需要一个可嵌入镜像拉取流程的缓存授权包，缓存记录是镜像摘要加一份已成功拉取过该摘要的凭据哈希集合：请求命中缓存时，必须用当前凭据哈希在集合里查到才放行，否则回源校验凭据，校验通过才把新哈希加入集合；匿名可拉的镜像由调用方标注并免凭据；调用方推送凭据撤销事件时，对应哈希从所有记录中移除；记录按摘要而非标签，标签漂移到新摘要后，旧摘要的授权不得延续；Always 策略永远回源。用租户 B 无凭据命中、凭据撤销后再命中、标签漂移和匿名镜像验证，并枚举并发拉取与撤销事件的交错。

## 用法

```go
auth := netsec.New()

decision, err := auth.Authorize(ctx, netsec.Request{
    Reference:  "registry.example/team/app:latest",
    Digest:     digest,                 // 由镜像存储或源解析得到，不能传标签
    Credential: credHash,               // 私有时必填，可来自 netsec.HashCredential
    Cached:     imageStore.Has(digest),  // 由节点镜像存储判断
    Policy:     netsec.IfNotPresent,
}, func(ctx context.Context, src netsec.SourceRequest) (bool, error) {
    return registry.CheckPull(ctx, src.Reference, src.Digest, src.Credential)
})
```

- `netsec.CacheHit`：使用本地摘要对应的缓存。
- `netsec.SourceRequired`：从源拉取；`Always` 以及未命中的请求会返回这个结果。
- `netsec.Denied`：不得使用缓存或继续拉取。

## 授权语义

- 授权记录只按镜像摘要建立，`Reference` 只传给源校验回调，不参与缓存键。标签漂移到新摘要后，必须针对新摘要重新校验。
- 缓存命中但没有当前凭据哈希时，先回源校验；校验通过才把哈希加入该摘要的集合，随后仍返回 `CacheHit`，因为内容已经在本地。
- 私有镜像即使缓存命中也必须提供凭据哈希；空凭据不会被当作匿名请求。匿名镜像由调用方通过 `Anonymous` 明确标注，免凭据且不记录哈希。
- `Always` 永远返回 `SourceRequired`。私有镜像每次都会调用源校验；若源明确拒绝，已有缓存授权也会被移除。
- `HashCredential` 只保存凭据的 SHA-256 摘要，包内不保存原始凭据。

## 并发与撤销

`Revoke` 使用反向索引把哈希从所有摘要记录中删除。每次撤销都会递增该哈希的代号；在撤销之前开始、尚未提交的源校验即使返回成功，也不能把旧哈希重新写回缓存。撤销之后重新发起的源校验如果仍然成功，可以建立新的授权。

`Forget` 在镜像内容被驱逐时删除摘要记录，并防止在途校验重新创建该记录。

## 验证

```sh
go test -race ./...
```

测试覆盖：

- 租户 A 拉取后，租户 B 无凭据或凭据哈希不在集合中时不能直接命中缓存。
- 缓存命中但哈希未知时回源校验；通过后使用缓存并记录哈希。
- 撤销后从所有摘要移除；撤销发生在校验前、校验中、提交前、提交后以及多路并发拉取时的交错。
- 同一标签漂移到新摘要后，旧摘要授权不生效。
- 匿名镜像免凭据，以及 `Always` 始终回源。
