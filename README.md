# Kerberos PAC签名校验

服务端收到Kerberos票据时，攻击者可伪造或拼接PAC，把低权限用户的组SID换成管理员。为Go服务认证包实现PAC校验，供GSS接受调用：缓冲区按偏移与长度严格解析，类型重复或重叠拒绝，服务器与KDC签名都先把签名字段清零再计算，缺少KDC签名的PAC不得采信其SID。用重复缓冲、改组SID、缺KDC签名和合法PAC验证。

## 用法

```go
validator := &netsec.Validator{
    ServerKey: serviceKey, // KDC与服务共享的长期密钥
    KDCKey:    kdcKey,     // krbtgt/权益服务密钥
}

info, err := validator.Accept(pacBytes)
if err != nil {
    // 认证失败：不要使用PAC中的任何SID
}
// 只有在服务器签名和KDC签名都通过后才会返回info
groups := info.AllGroupSIDs()
```

## 校验规则

- 按 `PAC_INFO_BUFFER` 的偏移和长度解析，检查整数溢出、越界及指向头部/描述符表的偏移。
- 同类型缓冲区重复、任意两个缓冲区字节区间重叠时拒绝。
- 服务器签名覆盖整个PAC，计算前把服务器和KDC两个 `PAC_SIGNATURE_DATA` 的 `Signature` 字段清零。
- KDC签名必须存在并通过KDC密钥校验；支持对服务器签名值、完整服务器校验和缓冲区、整PAC清零形式三种KDC签名输入。
- `Accept`/`Validate` 只有在两个签名都验证成功后才解析 `KERB_VALIDATION_INFO`；`PAC.LogonInfo` 在未验证时返回 `ErrUnverifiedPAC`。
- 解析输入会复制到私有缓冲区，调用方随后修改原始切片不会影响已验证的PAC。

默认支持 checksum/signature 类型 7、9、10、12、13、14、15、16、19、20（含 RFC 3961 n-fold 与 RFC 8009 KDF）。其他部署可通过 `Validator.Checksum` 注入 Kerberos 加密提供方。

## 测试

```sh
go test ./...
```

测试覆盖合法PAC、重复缓冲区、重叠缓冲区、越界/溢出、改组SID、仅重算服务器签名（KDC拒绝）、缺少KDC签名及错误密钥等场景。
