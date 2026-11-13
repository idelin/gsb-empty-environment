# 未键控的方案头

缓存键漏掉方案转发头时，一次明文响应会污染https用户。该头若被源站使用，就必须进入缓存键；未列入键的转发头一律不转给源站。Vary里明确列出的头仍参与键。缓存层调用可导入的Go库做这次裁决。只改这个头的值，核对是否共用一条缓存。

## 用法

```go
policy := netsec.DefaultPolicy() // X-Forwarded-Proto 入键并允许转发

// vary 取自命中响应（或即将存储的响应）的 Vary 头。
vary := []string{"Accept-Encoding"}
key := policy.Key(req, vary)
upstream := policy.PrepareRequest(req, vary)
```

- `DefaultPolicy` 将 `X-Forwarded-Proto` 纳入键，并只向源站转发该入键的转发头；`X-Forwarded-For`、`Forwarded` 等未入键的转发头会被删除。
- 源站若不使用方案头，可用 `netsec.NewPolicy(netsec.HeaderXForwardedProto, false)`，此时该头不会转发。
- 响应 `Vary` 中列出的任何头都会自动加入键；若其中包含转发头，该头同时获准转发。
- `Vary: *` 通过 `Decision.CanStore` / `VaryAllowsStorage` 明确判为不可共享缓存。

只变更 `X-Forwarded-Proto` 的值时，`http` 与 `https` 会生成不同的缓存键，不会共用同一条缓存记录；大小写和首尾空白会归一化。
