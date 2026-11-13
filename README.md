# TCP-AO 密钥选择与切换

攻击者伪造 TCP-AO 选项里的 RNextKeyID，想诱使对端切到尚未验证的密钥，或重放旧密钥报文。为Go BGP 传输层写可导入的 TCP-AO 密钥选择包：入站报文按 KeyID 取密钥，MAC 不通过一律丢弃；只有 MAC 验证通过的报文携带的 RNextKeyID 才能触发发送侧切换，新密钥须已登记并在发送有效期内；已过接收有效期的密钥不再接受。用伪造请求、旧密钥重放和正常切换验证。

## 包

导入路径：`example.org/netsec/q0687/tcpao`

核心类型：

- `Selector`：注册密钥、验证入站报文、维护当前发送密钥。
- `Key`：包含方向独立的 `SendKey`/`ReceiveKey`、MAC 算法及发送/接收有效期。
- `Segment`：入站 TCP-AO 选项与认证数据。
- `ComputeMAC`：按 `KeyID || RNextKeyID || MACData` 计算 MAC。

## 用法

```go
key1 := tcpao.Key{
    ID:         1,
    SendKey:    []byte("outbound secret"),
    ReceiveKey: []byte("inbound secret"),
    Algorithm:  tcpao.HMACSHA1,
    SendValidity: tcpao.Validity{
        NotBefore: now,
        NotAfter:  now.Add(24 * time.Hour),
    },
    ReceiveValidity: tcpao.Validity{
        NotBefore: now,
        NotAfter:  retiredAt,
    },
}
key2 := tcpao.Key{
    ID:         2,
    SendKey:    []byte("next outbound secret"),
    ReceiveKey: []byte("next inbound secret"),
    Algorithm:  tcpao.HMACSHA1,
    SendValidity: tcpao.Validity{
        NotBefore: now,
        NotAfter:  now.Add(24 * time.Hour),
    },
    ReceiveValidity: tcpao.Validity{
        NotBefore: now,
        NotAfter:  now.Add(24 * time.Hour),
    },
}

selector, err := tcpao.NewSelector(key1.ID, key1, key2)
if err != nil {
    return err
}

segment := tcpao.Segment{
    KeyID:      parsedKeyID,
    RNextKeyID: parsedRNextKeyID,
    MAC:        parsedMAC,
    MACData:    authenticatedTCPBytes,
}
result, err := selector.Receive(segment, now)
if err != nil {
    // Discard the segment. This includes unknown/expired keys and MAC failures.
    return err
}
if result.Switched {
    // Subsequent outbound segments use result.SendKeyID.
}
```

`MACData` 不包含 KeyID 和 RNextKeyID 两个字节；包会在计算前自动前置这两个字段，因此篡改任一选项都会导致 MAC 校验失败。实际接入 BGP 传输层时，`MACData` 应覆盖 TCP 伪首部、TCP 头部（去掉这两个 AO 字段）和载荷。

## 安全规则

- 入站先按 `KeyID` 精确取密钥；未知 KeyID 直接拒绝。
- 接收有效期采用半开区间 `[NotBefore, NotAfter)`；到达 `NotAfter` 即拒绝，旧密钥重放因此失效。
- MAC 校验使用 `hmac.Equal` 常量时间比较；任何长度或内容不匹配的报文都丢弃，且不改变发送状态。
- `RNextKeyID` 仅在 MAC 验证成功后处理；`0` 表示不请求切换。
- 请求的新密钥必须已注册且 `SendValidity` 在报文处理时刻有效，否则不切换；已验证报文仍可正常接收，`Result.RNextError` 给出拒绝原因。
- `SendKey(at)` 在每次取出发送密钥时再次检查发送有效期，过期或未生效时 fail closed。

## 测试

```sh
go test -race ./...
```

测试覆盖正常切换、伪造/篡改 RNextKeyID、无效 MAC、未登记新密钥、新密钥发送有效期外、过期接收密钥重放、未知 KeyID 以及并发接收。
