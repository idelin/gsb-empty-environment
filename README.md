# WASI出站HTTP授权与地址固定

组件经 wasi:http 的 outgoing-handler 发请求，攻击者想借它访问云元数据地址、内网服务，或借解析结果变化绕过白名单。为 Go 宿主写供 outgoing-handler 实现调用的出站授权包：按 scheme 与 authority 匹配调用方登记的允许项（主机名不分大小写，端口缺省按 scheme 补 80 或 443，IPv6 字面量带方括号）；允许域名解析出的地址若属回环、链路本地、私网或 169.254.169.254 即拒绝，连接必须使用校验过的地址而不是再解析一次；请求头 Host 须与 authority 一致，Connection、Transfer-Encoding 与 Upgrade 被拒绝；宿主不跟随重定向。用大写主机名、解析到 169.254.169.254、两次解析结果不同、Host 不一致和合法 443 请求验证。

## netsec 包

`netsec` 是供 wasi:http outgoing-handler 调用的出站授权层。

### 安全保证

- **白名单精确匹配**：按归一化后的 `scheme://host:port` 匹配登记项，无通配/后缀匹配；主机名转小写，缺省端口按 scheme 补 80（http）或 443（https），IPv6 字面量必须带方括号。
- **敏感地址拦截**：回环（`127.0.0.0/8`、`::1`）、链路本地（`169.254.0.0/16`、`fe80::/10`）、私网（`10/8`、`172.16/12`、`192.168/16`、`fc00::/7`）、未指定地址（`0.0.0.0`、`::`）以及云元数据端点 `169.254.169.254` 一律拒绝；登记阶段与解析阶段各查一次。
- **单次解析 + 地址固定**：`Check` 只解析一次主机名并校验结果；之后的拨号与 `Transport` 只连校验过的 IP 字面量，绝不重新解析，从而阻断 DNS rebinding。
- **Host 头强制一致**：`Host` 与 authority 归一化后必须相同，`Decision.Apply` 会把线上 Host 改写为规范 authority。
- **禁用逐跳/升级头**：`Connection`、`Transfer-Encoding`、`Upgrade` 直接拒绝。
- **不跟随重定向**：`Decision.Client` 用 `http.ErrUseLastResponse` 原样返回 3xx。
- **禁用环境代理**：`Transport.Proxy = nil`，避免经代理绕过白名单；TLS 以授权主机名校验证书、以固定 IP 拨号，并要求 TLS 1.2+。

### 用法

```go
authz, err := netsec.New([]netsec.Entry{
    {Scheme: "https", Authority: "api.example.com"},        // 即 https://api.example.com:443
    {Scheme: "http", Authority: "internal.example.com:8080"},
    {Scheme: "https", Authority: "[2001:db8::1]:8443"},
})
if err != nil { /* 登记项本身指向敏感地址等 */ }

decision, err := authz.Check(req.Context(), netsec.InputFromRequest(req))
if err != nil {
    // errors.Is 判定 ErrNotAllowed / ErrBlockedAddress /
    // ErrHostMismatch / ErrForbiddenHeader
}

decision.Apply(req)
resp, err := decision.Client().Do(req) // 不跟随重定向
```

### 测试

`go test ./...` 覆盖：大写主机名匹配、域名解析到 `169.254.169.254` 被拒、两次解析结果不同（DNS rebinding）时连接仍固定在首次校验地址、Host 不一致被拒、合法 443 TLS 请求（真实 TLS 握手 + 证书校验，使用内存 `net.Pipe` 以适配无 socket 绑定权限的沙箱），另含归一化、IPv6、禁头、地址族过滤与重定向等用例。
