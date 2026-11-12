# TURN中继对端与五元组授权

TURN中继允许认证用户让服务器向对端转发UDP，攻击者可请求中继到内网或云元数据地址，也可在同一五元组上换用户名接管分配。给Go TURN服务器写供Allocate、CreatePermission和ChannelBind处理调用的授权包：按RFC 8656解码XOR-PEER-ADDRESS（IPv6要用事务号参与异或），对端地址不得在调用方给出的禁止网段；五元组绑定首个长期凭据用户名，不同用户返回441，过期nonce返回438，通道号只接受0x4000到0x7FFF。用环回、元数据地址、IPv6异或、换用户和通道号越界验证。

## 用法

```go
authorizer := netsec.NewAuthorizer(netsec.Config{
    DeniedPrefixes: netsec.DefaultForbiddenPrefixes(),
})

// Allocate：认证层已经校验过长期凭据和nonce，这里只传入结果。
err := authorizer.AuthorizeAllocate(tuple, netsec.Credentials{
    Username:     username,
    Nonce:        nonce,
    NonceExpires: nonceExpires,
})

// CreatePermission：peerAttrs 是请求中每个XOR-PEER-ADDRESS的原始属性值。
err = authorizer.AuthorizeCreatePermission(tuple, creds, peerAttrs, transactionID)

// ChannelBind：channel为0x4000-0x7FFF，peerAttr为单个原始属性值。
err = authorizer.AuthorizeChannelBind(tuple, creds, channel, peerAttr, transactionID)
```

`netsec.ProtocolError` 携带需要写回 `ERROR-CODE` 的TURN错误码，可用
`netsec.ErrorCode(err)` 读取：

- `400`：属性格式错误、通道号越界、缺少用户名。
- `403`：对端地址落在调用方给出的禁止网段。
- `437`：CreatePermission/ChannelBind未找到对应分配。
- `438`：nonce已过期。
- `441`：同一五元组使用了与首个分配不同的用户名。

五元组使用`netip.AddrPort`和协议标识；IPv4-mapped IPv6地址会归一化，
避免用`::ffff:127.0.0.1`绕过IPv4禁止网段。`DecodeXORPeerAddress`严格要
求IPv4属性8字节、IPv6属性20字节，IPv6地址按RFC 5389/8656用
magic-cookie拼接96位事务号进行异或。

## 验证

```sh
go test -race ./...
```

测试覆盖RFC 5769 IPv6已知向量、IPv4/IPv6 XOR、环回与云元数据地址、IPv4-mapped
IPv6绕过、五元组换用户、过期nonce，以及`0x4000`/`0x7FFF`边界和越界通道号。
