# netsec

供 Go 边缘服务调用的客户端身份与登录限流组件。它解决的核心问题是：客户端可以伪造
`X-Forwarded-For`，如果限流器直接相信该头，同一攻击源会被计成无限多个新客户端。

## 安全模型

- 只有 `RemoteAddr` 的直接对端位于受信代理列表时，才解析转发头。
- 从转发链右侧向左查找，取最右侧的非受信跳作为客户端地址。代理在链尾追加的地址
  因而不可被客户端左侧注入内容覆盖。
- 转发链必须语法完整、每一跳都是合法地址，并且存在至少一个非受信跳；否则回退到
  直接对端地址（fail closed）。
- 同时支持 `X-Forwarded-For` 与 RFC 7239 `Forwarded`。两者都存在且解析结果不一致时，
  不使用任何声明地址。
- `X-Real-IP` 不是转发链，默认不参与解析；确有需要时可显式启用
  `netsec.WithXRealIPFallback()`。
- 客户端可控的转发头在入站中间件中全部删除，应用只能从 context 读取已验证身份。
- 身份同时包含地址、账户和设备；限流按 `action -> Policy` 分别配置阈值和滑动窗口。
- `TrustedProxies.Replace` 原子替换代理配置，解析结果不缓存，下一请求立即按新拓扑评估。

## 快速使用

```go
trusted, err := netsec.NewTrustedProxies(
    "10.0.0.0/8",
    "192.0.2.10",
)
if err != nil {
    return err
}

resolver := netsec.NewResolver(trusted)
limiter, err := netsec.NewLimiter(map[string]netsec.Policy{
    "login": {
        Window:       time.Minute,
        AddressLimit: 5,
        AccountLimit: 10,
        DeviceLimit:  10,
    },
    "password-reset": {
        Window:       10 * time.Minute,
        AddressLimit: 3,
        AccountLimit: 3,
        DeviceLimit:  3,
    },
})
if err != nil {
    return err
}

guard, err := netsec.NewGuard(resolver, limiter)
if err != nil {
    return err
}

http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
    identity, decision, err := guard.Check(r, "login")
    if err != nil {
        http.Error(w, "invalid client address", http.StatusBadRequest)
        return
    }
    if !decision.Allowed {
        w.Header().Set("Retry-After", strconv.Itoa(int(decision.RetryAfter.Seconds())))
        http.Error(w, "too many login attempts", http.StatusTooManyRequests)
        return
    }

    // identity.Address / identity.Account / identity.Device
    _ = identity
})
```

若账户来自登录表单，可显式覆盖账户维度，或提供提取器：

```go
resolver := netsec.NewResolver(trusted,
    netsec.WithAccountExtractor(func(r *http.Request) string {
        return r.FormValue("username")
    }),
    netsec.WithDeviceExtractor(func(r *http.Request) string {
        return r.Header.Get("X-Device-ID")
    }),
)
```

默认提取器只读取已经解析的 `req.PostForm` / `req.Form`，不会在中间件中主动消费请求体。

## 入站清洗

```go
handler := resolver.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    identity, ok := netsec.IdentityFromContext(r.Context())
    if !ok {
        http.Error(w, "missing identity", http.StatusInternalServerError)
        return
    }
    _ = identity
}))
```

中间件会删除 `X-Forwarded-For`、`Forwarded`、`X-Real-IP`、`X-Client-IP`、
`CF-Connecting-IP` 等客户端可控地址头。下游只使用 context 中的已验证身份。

## 动态代理配置

```go
if err := trusted.Replace("10.0.0.0/8", "2001:db8:1::/48"); err != nil {
    return err
}
// 下一次 Resolve 立即使用新列表；旧限流桶保留到窗口自然过期。
```

未知 action 默认拒绝（fail closed），避免漏配策略时静默放行。

## 验证

```sh
go test ./... -race
```

测试覆盖伪造 `X-Forwarded-For`、重复/多重转发链、`Forwarded` 与 IPv6、HTTP/1.1 /
HTTP/2 / WebSocket 共享预算、账户与设备维度、合法代理链、代理配置热变更、滑动窗口
到期以及并发限流。
