# IPv4映射地址绕过了内网黑名单

出站代理按点分十进制判断内网地址，攻击者用`::ffff:169.254.169.254`或IPv4兼容写法访问云元数据，黑名单未命中。请实现供Go出站代理调用的可导入地址归类组件。解析后统一转换为可比对的地址族与范围，IPv4映射、兼容、6to4、Teredo与带zone的IPv6都按其实际可达目标归类；链路本地、回环、私网、组播与保留段逐段判定。域名解析结果同样归类，重定向逐跳重校验。用映射地址、6to4、zone后缀、十进制与八进制写法、合法公网地址和重定向验证实际目标与判定一致。

## 组件

包 `example.org/netsec/q0301` 提供可导入的 `netsec` 组件：

- `ParseAddress` / `Classify`：解析地址、`host:port`、URL 中的地址字面量、zone、传统十进制/八进制/十六进制 IPv4，以及嵌入 IPv4 的 IPv6 形式。
- `Result.Address`（别名 `Normalized`）：去除 zone 并解包映射/兼容/6to4/Teredo/NAT64/ISATAP 后的实际目标，可直接与 `netip.Prefix` 比较。
- `Result.DialAddr`：实际交给网络栈的拨号地址；6to4/Teredo/NAT64 等仍以 IPv6 拨号，而归类使用 `Address` 的嵌入目标。
- `Result.Category`（别名 `Scope`）：`public`、`link-local`、`loopback`、`private`、`unique-local`、`site-local`、`shared`、`multicast`、`documentation`、`benchmark`、`discard`、`reserved`、`unspecified`。
- `Result.Mechanism`：记录被解包的转换形式，便于审计和日志。
- `Resolver.Resolve`：对每个 DNS 答案执行同样的归类。
- `Resolver.CheckHost` / `CheckURL`：任一 DNS 答案非公网即失败关闭。
- `Resolver.CheckRedirect`：作为 `http.Client.CheckRedirect` 使用时，对初始请求和每一跳重新解析、归类。
- `Resolver.NewHTTPClient`：使用固定目标的拨号器，检查 DNS 后直接拨已校验的公网 IP，避免检查与拨号之间发生 DNS 重绑定。
- `Resolver.WrapRoundTripper`：为自定义 Transport 加上请求前的主机校验，代理模式下也会检查原始目标而不是只检查代理地址。

非法输入按 `ErrInvalidAddress` 返回；非公网目标返回包裹 `ErrBlocked` 的 `*BlockedError`，可用 `errors.Is(err, netsec.ErrBlocked)` 判断。

## 使用

```go
r, err := netsec.ParseAddress("::ffff:169.254.169.254")
if err != nil {
    // 解析失败，按不可信处理
}
// r.Address == 169.254.169.254
// r.Family  == netsec.FamilyIPv4
// r.Category == netsec.CategoryLinkLocal
// r.Public == false

ctx := context.Background()
resolver := netsec.NewResolver()
if err := resolver.CheckURL(ctx, "http://169.254.169.254/latest/meta-data/"); errors.Is(err, netsec.ErrBlocked) {
    // 拒绝
}

client := resolver.NewHTTPClient() // 重定向逐跳复检，并固定拨号目标
```

## 验证

```sh
go test ./...
```

测试覆盖映射、兼容、6to4、Teredo、NAT64、zone、十进制/八进制/十六进制写法、合法公网地址、DNS 混合答案及重定向逐跳校验。

说明：NAT64 解包内置了 `64:ff9b::/96` 与本地用途 `64:ff9b:1::/48`。若网络使用自定义 NAT64/6rd 前缀，需要在代理策略层额外配置这些前缀，因为仅凭地址文本无法知道任意网络的自定义嵌入位置。
