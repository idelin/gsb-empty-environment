# dialpolicy

为Go抓取服务实现可传给http.Client的受限Transport。DNS返回多个地址时逐个核验后只拨已验证地址，保持原主机名用于TLS；重定向、连接复用和策略热更新都不能访问禁区。用可注入解析器及本地监听器验证同名重绑定与复用旧连接时的实际对端。

## 设计

`Guard` 是唯一的策略执行点，产出可直接交给 `http.Client` 的 `*http.Transport`。

- **解析与拨号分离**：`DialContext` 先通过可注入的 `Resolver` 解析主机名，再用当前 `Policy` 逐个核验返回的 IP；只有通过核验的地址才会被拨号，多地址按顺序回退，被拒地址永不拨号。
- **保留主机名做 TLS**：DNS 在拨号函数内完成，`http.Transport` 仍按请求 URL 的主机名设置 TLS `ServerName` 并校验证书，拨号目标是 IP 不影响证书匹配。
- **重定向逐跳核验**：`CheckRedirect` 对每个跳转先校验 scheme（仅 http/https）与目标主机的全部解析结果；拨号层对最终选中的地址再独立核验一次。
- **连接复用安全**：连接池只复用已建立到已验证对端的连接；策略切换时关闭空闲连接，并通过 `connTracker` 主动关闭对端被新策略拒绝的在途连接，解除阻塞中的读写。
- **平台层兜底**：拨号器的 `Control` 钩子针对内核实际连接的具体 IP 再做一次策略判断，独立于解析路径。
- **默认拒绝**：`PrefixPolicy` 无匹配即拒绝；`BlockedPrefixes` 覆盖 loopback、链路本地、私网、CGNAT、组播等范围。

## 用法

```go
policy := dialpolicy.PrefixPolicy{
    AllowPrefixes: []netip.Prefix{dialpolicy.MustPrefix("0.0.0.0/0")},
    DenyPrefixes:  dialpolicy.BlockedPrefixes,
}
guard := dialpolicy.New(policy)
client := guard.Client() // 或 guard.Transport() 传给自定义 http.Client

// 运行时热更新：原子切换并清理受影响连接
guard.UpdatePolicy(stricterPolicy)
```

注入自定义解析器：

```go
guard := dialpolicy.New(policy, dialpolicy.WithResolver(myResolver))
```

## 测试

测试用可注入的 `Resolver` 模拟多答案 DNS 与同名重绑定，并以 `net.Pipe`
构建内存监听器（开发沙箱禁止真实端口监听），用记录对端 IP 的拨号器断言
实际连接目标。覆盖：

- 多答案只拨已验证地址、被拒地址不做回退、全部拒绝时 fail-closed
- IP 字面量 URL 同样受策略约束
- TLS 保留原主机名（SNI/证书校验），非证书 SAN 名称握手失败
- 重定向到禁区 IP 与非 HTTP scheme 被拦截，安全跳转正常
- 同名重绑定后旧连接复用同一已验证对端，新解析出的禁区地址不被拨号
- 策略热更新关闭池化连接并切断在途连接，切换后无禁区 I/O

```sh
go test -race ./...
```
