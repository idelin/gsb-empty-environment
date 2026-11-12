# Arrow Flight 票据换端点防护

攻击者把 Arrow Flight 的 DoGet 票据换到别的端点或用户使用，或改票内查询。
本包为 Go 服务提供 GetFlightInfo 和 DoGet 共用的 HMAC-SHA256 票据，覆盖：

- 主体（`Principal`）
- 描述符（`Descriptor`，含类型、路径和命令）
- 端点位置（`Endpoint`）
- 过期时间（`ExpiresAt`）
- 分区号（`Partition`）

换端点、跨用户、改描述符/分区、过期、截断、未知格式版本和未知密钥都会拒绝。
MAC 使用 `hmac.Equal` 常量时间比较。

## 票据格式

```text
version(1) | key_id(1) | payload_len(4, big endian) | payload | HMAC-SHA256(32)
```

HMAC 输入为域分隔串、完整的 6 字节头（含版本、密钥号和载荷长度）以及载荷。
载荷中的每个字段都带长度编码，避免路径分段拼接产生歧义。解析后要求载荷长度
精确匹配，尾部多余字节和截断都会拒绝，单张票据上限 1 MiB。

## 接入方式

GetFlightInfo 和 DoGet 使用同一个 `Keyring`。签名使用 `keys[0]`，其余密钥仅在
轮换窗口内用于验证。

```go
ring, err := netsec.NewKeyring(
    netsec.Key{ID: 2, Secret: newSecret},
    netsec.Key{ID: 1, Secret: oldSecret},
)
if err != nil {
    return err
}

binding := netsec.Binding{
    Principal:  authenticatedUser,
    Descriptor: descriptor,
    Endpoint:   currentEndpoint,
    Partition:  partition,
}

// GetFlightInfo: 签发短期票据。
ticket, err := ring.Issue(binding, time.Now().Add(2*time.Minute))

// DoGet 请求只带票据：校验认证主体、当前端点、MAC 和过期时间，
// 再使用票据内已认证的描述符和分区执行查询。
claims, err := ring.VerifyRequest(ticket, caller, endpointHandlingTheRequest)
```

服务应在 DoGet 中传入实际处理请求的端点，而不是票据里记录的端点；应传入认证
得到的主体，而不是客户端自报的主体。若服务另有可信的分区/描述符上下文，可
改用 `Verify` 对完整 `Binding` 做逐字段匹配。轮换完成后移除旧密钥，旧票据即
被拒绝。

## 验证

```sh
go test ./... -race
go vet ./...
```

测试覆盖正常往返、换端点、跨用户、改描述符、改分区、逐字节载荷篡改、过期、
截断、尾部多余字节、未知版本、未知密钥和新旧密钥轮换窗口。
