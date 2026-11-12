# 证书CRL分发点URL类型与目标拒绝

## 组件

`netsec` 包提供供证书链吊销检查回调调用的 CRL 取回组件，把证书中的
CRL 分发点视为攻击者可控输入。

- `Fetcher`：执行单次取回，内置方案、地址、重定向与响应体限制，可通过
  `WithLookup` / `WithDialer` 注入解析器与拨号器。
- `ChainFetcher`：在一次链验证期间复用，跟踪每证 4 个、每链 12 次的预算。

## 安全策略

- 仅允许 `http://`；`ldap`、`ldaps`、`file`、`ftp`、无 scheme 及
  `https` 等一律忽略（不记为取回，也不当作吊销失败），并拒绝内嵌
  userinfo 的 URL。
- 主机解析后所有地址都必须是全局单播：除标准库定义外，额外拒绝
  RFC1918、CGNAT、链路本地（含 `169.254.169.254`）、环回、组播、保留段、
  IPv6 ULA/链路本地及带 zone 的地址。
- 拨号前（preflight）与真正拨号时各做一次解析校验，拨号器只连接校验过
  的地址，消除 DNS 重绑定（先公后私）的 TOCTOU 窗口。
- 重定向最多 3 次；每个跳转目标重新做方案与地址校验，因此无法借由
  重定向把请求导向内网或元数据服务。
- 响应体上限 8 MiB（`MaxBodyBytes`），超出返回 `ErrBodyTooLarge`。
- 每张证书最多检查前 4 个分发点（`MaxDistributionPointsPerCert`），整条
  链最多 12 次取回（`MaxFetchesPerChain`）。
- 分发点存在 `reasons` 或 `cRLIssuer` 字段（间接 CRL）时跳过，不取回。

## 链验证回调用法

```go
fetcher := netsec.NewFetcher()
chain := netsec.NewChainFetcher(fetcher)

for _, cert := range verifiedChain {
    crls, err := chain.FetchCRLForCert(ctx, cert)
    if err != nil {
        // 地址非法、超过预算、响应过大等：按策略处理（如保守拒绝）。
        return err
    }
    for _, der := range crls {
        // 用 x509.ParseRevocationList 校验并匹配序列号。
        _ = der
    }
}
```

## 测试

`go test ./...`（含 `-race`）。测试在进程内 `net.Pipe` 监听器上运行，
无需真实 socket，覆盖：ldap/file 等方案忽略、`169.254.169.254` 与私网
地址拒绝、DNS 先公后私重绑定（含 preflight/拨号 TOCTOU 与重定向场景）、
超大与恰好 8 MiB 响应、每证/每链上限、间接 CRL 跳过及重定向次数限制。
