# 密封元数据地址访问

工作负载允许访问受控元数据代理，但普通业务请求不能借重定向进入元数据通道。写供Go网络宿主调用的通道能力包，能力只由可信启动器创建，绑定进程实例与目标路径前缀；每次请求同时验证能力和实际拨号地址，重定向必须重新取得能力。用本地伪端点测试路径规范化、凭据头跨跳及进程代次复用。

## 结构

- `capability.go`：`Launcher` 独占 HMAC 密钥与进程实例状态，负责 `Mint` 签发与 `Restart` 代次轮换；`Verifier` 与启动器共享实例状态，校验令牌的签名、代次、有效期与路径前缀。
- `channel.go`：`Channel` 实现 `http.RoundTripper`，每次请求同时校验能力（`WithCapability` 注入上下文）与实际拨号地址（`guardedDial` 核对 `RemoteAddr`）；`Client` 挂接重定向策略，进入元数据通道的重定向必须经 `RedirectApprover` 重新取得能力，跨源跳转剥离凭据头。
- `pseudo_test.go`：内存管道伪网络（主机名解析 + 地址路由），替代沙箱中不可用的 TCP 监听。

## 用法

```go
launcher, _ := netsec.NewLauncher()          // 仅可信启动器持有
channel := netsec.NewChannel(netsec.Config{
    EndpointHost:   "metadata.internal:7600",
    ProtectedAddrs: []string{"10.0.0.1:7600"},
}, launcher.Verifier())

client := channel.Client(func(ctx context.Context, target *url.URL, via []*http.Request) (*netsec.Capability, error) {
    return launcher.Mint("/meta/", time.Minute) // 重定向时重新取得能力
})

cap, _ := launcher.Mint("/meta/", time.Minute)
req, _ := http.NewRequest(http.MethodGet, "http://metadata.internal:7600/meta/data", nil)
resp, err := client.Do(req.WithContext(netsec.WithCapability(req.Context(), cap)))
```

## 测试

`go test ./...` 覆盖：路径规范化（点段/重复分隔符/编码穿越）、伪造与过期令牌、凭据头跨跳剥离、重定向进入元数据通道的能力重取、业务域名重绑定到元数据地址的拨号守卫、进程代次复用失效。
