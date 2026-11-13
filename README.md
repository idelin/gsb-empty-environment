# JARM 响应封装

包 `netsec`（import path `example.org/netsec/q0096`）实现 JWT Secured
Authorization Response Mode（JARM），仅依赖 Go 标准库，签名算法为 HS256。

## 安全语义

- `response_mode` 为 `jwt`，或以 `.jwt` 结尾的复合模式（如 `query.jwt`、
  `form_post.jwt`、`fragment.jwt`）时，授权结果只取自 `response` 参数内
  已签名 JWT 的有效载荷。
- 外层查询参数仅作传输元数据；与 JWT 声明同名的外层参数（包括 `code`、
  `state`）一律忽略，不会进入授权结果。
- 接受 JWT 前必须同时满足：HMAC 签名密钥匹配、`iss` 匹配、`aud` 包含本
  客户端、`exp` 未过期（可配置时钟偏移宽限）、`state` 与期望一致；
  `alg: none` 及其它算法一律拒绝。
- 非 JWT 模式（如 `query`、`form_post`、`fragment`）按普通查询参数读取，
  不做签名校验。

## 授权服务签发

```go
signer := &netsec.Signer{
    Key:      sharedSecret,
    Issuer:   "https://auth.example.com",
    Audience: "client-123",
}
token, err := signer.SignResponse(map[string]string{
    "code":  "inner-jwt-code",
    "state": "state-abc",
}, time.Time{}) // 零值使用默认 1 分钟 TTL

// 或直接拼好回调 URL（token 放在 response 参数）
redirect, _ := url.Parse("https://client.example.com/cb")
callbackURL, err := signer.SignCallbackURL(redirect, params, time.Time{})
```

## 依赖方校验

```go
verifier := &netsec.Verifier{
    Key:           sharedSecret,
    Issuer:        "https://auth.example.com",
    Audience:      "client-123",
    ExpectedState: "state-abc",
    Leeway:        5 * time.Second,
}

// mode 为发起授权时请求的 response_mode
resp, err := verifier.ParseCallback(callbackURL, "jwt")
if err != nil {
    // 签名 / iss / aud / exp / state 任一不匹配都会返回错误
    return err
}
code := resp.Code() // 始终是 JWT 内的 code，外层同名参数被忽略
```

## 关键测试场景

`TestJWTModeIgnoresOuterCodeAndState` 在回调外层放置 `code=outer-attacker-code`
与 JWT 内的 `code=inner-jwt-code`，断言解析结果只取 JWT 内的值。
