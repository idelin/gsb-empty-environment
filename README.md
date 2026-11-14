# 云主机元数据被应用代取了

`netsec` 是供 Go 云上服务调用的出站 HTTP 策略组件，用于处理用户可影响的回调地址。它在发送请求前完成域名/端口白名单、DNS 解析结果和固定拨号地址的校验，并默认禁用代理与重定向。

```go
policy, err := netsec.New(netsec.Config{
    AllowedDomains: []string{"api.partner.example"},
    AllowedPorts:   []int{443},
    BearerToken:    os.Getenv("CALLBACK_TOKEN"),
})
if err != nil {
    return err
}

resp, err := policy.Do(req)
```

主要防护：

- 目标必须是白名单中的域名和端口；`*.example.com`、`.example.com`、`api.example.com:443` 和显式 IP 字面量也可作为白名单项。
- 所有 `169.254.0.0/16`、IPv6 链路本地、已知云元数据地址及元数据主机名始终拒绝，即使被写入白名单。URL 中的十进制、八进制和十六进制 IPv4 变体也会在解析前识别。
- 域名解析出的每个地址都会校验；通过后固定到本次解析的 IP 拨号，避免 DNS 重绑定。
- `HTTP_PROXY` 等环境变量和代理配置不会生效，`CONNECT` 请求被拒绝。
- `Host` 覆盖头被拒绝；重定向不会被跟随。
- 凭据仅在目标通过全部校验后注入，且不会发送到被拒绝的目标或代理。

显式写入白名单的 `127.0.0.1`/`::1` 和 `localhost` 可用于本机回调；解析到其他回环/内网地址需要 `Config.AllowLoopback` 或 `Config.AllowPrivate`。链路本地和元数据地址不可通过这些选项放行。
