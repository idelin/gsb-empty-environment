# 外部镜像层下载时泄露仓库令牌

镜像清单可为层列出外部URL，客户端下载时不能把私有仓库令牌带到第三方，也不能接受与清单摘要不符的响应。给Go镜像宿主实现可导入的层获取计划包：清单身份由宿主固定，层含sha256压缩摘要、大小、媒体类型及0至4个HTTPS URL；无URL时只从清单所属仓库按摘要取。外部URL禁止userinfo、片段、非默认或未登记端口、IP字面量及重定向到不同的可注册域；每一跳都由宿主URL策略复核。仓库Bearer令牌只发给精确仓库源且audience和scope包含该摘要的pull，外部请求只能带宿主明确登记的公共Accept与User-Agent，且不得带Authorization、Cookie、客户端证书或仓库自定义头。响应先流入隔离哈希器，压缩字节数恰等于声明大小且摘要一致后才发布；失败可试下一个URL，但任何前缀均不可见。并发请求同摘要可共享已验证blob，不能共享带凭据的在途请求。用本地HTTP替身记录每跳首部，测试跨站/同站重定向、错误摘要、长度截断、两个清单同摘要和所有URL失败，核对令牌从未离开仓库源。

## 包结构

- `Digest`、`Layer`、`ManifestRef` 和 `LayerPlan` 描述宿主固定的清单身份与层来源。
- `BuildLayerPlan` / `BuildManifestPlans` 在计划阶段校验摘要、大小、媒体类型、URL 数量和宿主 URL 策略。层列出 URL 时，计划只包含这些 URL；没有 URL 时，计划只包含清单所属仓库的精确 blob URL。
- `ExternalURLPolicy` 校验外部 HTTPS URL：禁止 userinfo、片段、IP 字面量、未登记非默认端口，并在每一跳要求同一可注册域。
- `Fetcher` 手工处理重定向，为每一跳重新执行 URL 策略和请求头构造。仓库 Bearer 令牌只在精确仓库 blob URL 上、且令牌 audience 和包含该层摘要的 `pull` scope 都合法时才附加。
- 重定向到清单所属仓库的精确 origin 时，该 origin 按宿主固定源处理，不要求作为外部 origin 登记；但只有精确 blob URL 才附加仓库令牌和仓库请求头，其他同源路径仍走无凭据的公共请求。
- 外部请求使用单独加固的 HTTP transport：清空 cookie jar、客户端证书、代理凭据和自动压缩，仅发送宿主登记的 `Accept` / `User-Agent`。
- 响应流写入隔离的临时文件并同步计算 sha256；只有压缩字节数恰好等于声明大小且摘要匹配后才原子发布到摘要缓存。失败的前缀不会暴露给调用方，也绝不会进入缓存。
- 已验证 blob 按摘要跨清单共享；带凭据的网络请求不做 singleflight，避免共享在途凭据。

## 使用

```go
policy := netsec.NewExternalURLPolicy()
if err := policy.RegisterOrigin("https://cdn.example.test:8443"); err != nil {
    // reject configuration
}

manifest := netsec.ManifestRef{
    Repository: netsec.Repository{Origin: "https://registry.example.test", Name: "team/app"},
    Digest:     manifestDigest,
}
plan, err := netsec.BuildLayerPlan(policy, manifest, netsec.Layer{
    Digest:    layerDigest,
    Size:      compressedSize,
    MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
    URLs:      []string{"https://cdn.example.test:8443/layers/abc"},
})
if err != nil {
    // reject the manifest/layer descriptor
}

fetcher, err := netsec.NewFetcher(netsec.FetcherConfig{
    Policy:           policy,
    TokenProvider:    tokenProvider,
    RepositoryClient: registryClient, // may carry registry mTLS material
    ExternalClient:   publicClient,   // must be an *http.Transport-backed client
    PublicAccept:     "application/vnd.oci.image.layer.v1.tar+gzip",
    PublicUserAgent:  "image-host/1.0",
})
if err != nil {
    // reject configuration
}
defer fetcher.Close()

reader, err := fetcher.Fetch(ctx, plan)
```

`RepositoryClient` is never used for external URLs. `ExternalClient` must be
backed by `*http.Transport`; `NewFetcher` fails closed for opaque transports
because client certificates cannot be proven absent. A non-default external
port must be registered for the exact host before planning or following a
redirect.
