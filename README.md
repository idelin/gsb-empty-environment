# 实例元数据代理 IMDSv2 守卫

供节点代理在转发实例元数据请求前调用的 Go 包。它阻断服务端请求伪造利用
`169.254.169.254` 获取角色凭证，并将会话令牌绑定到签发时的源容器。

## 转发模式

代理转发令牌申请时，先校验请求，再把 IMDS 返回的令牌交给守卫登记：

```go
guard := netsec.NewGuard()

ttlSeconds, err := guard.ValidateTokenRequest(r) // PUT /latest/api/token
if err != nil {
    // 拒绝，不要转发
}

// 将 r 转发给 169.254.169.254，读取 IMDS 响应中的令牌。
expiresAt, err := guard.BindToken(imdsToken, containerID, time.Duration(ttlSeconds)*time.Second)

// 读取元数据前：
if err := guard.CheckMetadataRequest(r, containerID); err != nil {
    // 拒绝，不要转发
}
```

`containerID` 必须来自节点代理可信的沙箱元数据，不能取自请求头。令牌只对
签发时登记的容器有效。

`Guard.Authorize` 是自包含的便捷入口：令牌申请会由守卫本地签发令牌，适合
本地测试或守卫自身终结令牌端点；真实转发到 IMDS 时应使用上面的
`ValidateTokenRequest` + `BindToken` + `CheckMetadataRequest` 流程。

## 强制策略

- 令牌申请必须为 `PUT /latest/api/token`，且 `X-aws-ec2-metadata-token-ttl-seconds`
  为单个 `1` 到 `21600` 的十进制整数。
- 出现 `X-Forwarded-For`、`Forwarded` 或 `Via` 头（即使值为空）一律拒绝。
- 元数据读取只接受 `GET`/`HEAD`，且必须携带唯一、有效且属于当前容器的
  `X-aws-ec2-metadata-token`。
- 令牌在申请时登记到期时间，到期后拒绝，跨容器使用也拒绝。
- 元数据读取携带令牌 TTL 头一律拒绝。
- 路径中任意 `..` 段或百分号编码的 `/`（含多层编码和 `%5c`）一律拒绝。

错误均可通过 `errors.Is` 与包内导出的 `Err*` 哨兵值匹配。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖转发头、跨容器令牌、TTL 越界、GET 携带 TTL、令牌过期和路径穿越。
