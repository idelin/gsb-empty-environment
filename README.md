# NTLM MIC与AV_PAIR校验

NTLM中继攻击者会剥离AUTHENTICATE消息的MIC或篡改AV_PAIR。为Go服务认证包实现AUTHENTICATE校验，供HTTP或SMB接入层调用：MsvAvFlags含MIC位或含MsvAvTimestamp时，MIC必须存在且等于以SessionBaseKey对三条消息算的HMAC-MD5（MIC字段清零），目标SPN须匹配本服务。用删MIC、改协商消息和合法握手验证。

## 使用

传入 NEGOTIATE、CHALLENGE、AUTHENTICATE 的原始字节，以及服务端握手得到的
`SessionBaseKey` 和本服务注册的 SPN：

```go
if err := netsec.ValidateAuthenticate(negotiate, challenge, authenticate, sessionBaseKey, "HTTP/server.example.com"); err != nil {
    // 拒绝认证
}
```

需要一次拿到用户名等身份信息时，使用：

```go
result, err := netsec.ValidateAuthenticateDetailed(negotiate, challenge, authenticate, netsec.ValidationOptions{
    SessionBaseKey: sessionBaseKey,
    ServiceSPN:     "SMB/server.example.com",
    RequireMIC:     true, // 可选：即使未声明 MIC 也强制要求
})
```

HTTP 接入层可直接解码 `Authorization: NTLM <base64>`：

```go
err := netsec.ValidateAuthorizationHeader(header, negotiate, challenge, sessionBaseKey, spn)
```

校验逻辑：

- 使用收到的原始消息计算 `HMAC-MD5(SessionBaseKey, NEGOTIATE || CHALLENGE || AUTHENTICATE)`，其中 AUTHENTICATE 的 16 字节 MIC 置零，不重新序列化消息。
- CHALLENGE 或 NTLMv2 响应中的 `MsvAvFlags.MIC_PRESENT`（`0x2`）或 `MsvAvTimestamp` 任一存在时，AUTHENTICATE 必须携带 MIC。
- CHALLENGE 与 AUTHENTICATE 中出现的所有 `MsvAvTargetName` 都必须匹配配置的服务 SPN；缺失目标名默认拒绝。
