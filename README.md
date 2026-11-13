# 紧凑 JWS 与分离载荷验证

`example.org/netsec/q0156` 提供可直接导入的 Go JWS 验证库。

## 分离载荷规则

- 分离模式必须由调用方显式声明：`VerifyDetached`、`WithDetachedPayload`，或
  `VerifyOptions{Detached: true, DetachedPayload: ...}`。
- 未声明分离时，`Verify` 只使用紧凑票自身的载荷段验证，绝不读取外部载荷。
- 声明分离后，紧凑票的载荷段必须为空；只要票内带有非空载荷段，就返回
  `ErrEmbeddedPayload`，即使该段内容与外部载荷相同。
- 外部载荷按 JWS `b64` 头参与签名输入：默认先做 base64url 编码；
  `b64:false` 时直接使用原始字节（RFC 7797）。
- 调用方提供外部类型时，头部的 `cty` 或其他载荷类型提示必须与之一致，
  否则返回 `ErrContentTypeMismatch`；没有类型提示则返回
  `ErrMissingContentType`。
- 外部载荷未声明分离时不会自动启用分离模式，相关输入返回
  `ErrDetachedNotDeclared`。

## 用法

```go
token, err := netsec.Verify(compact, hmacKey)
if err != nil {
    return err
}
use(token.Payload)
```

```go
token, err := netsec.VerifyDetached(
    compact,
    externalPayload,
    "application/json", // 外部载荷类型
    hmacKey,
)
if err != nil {
    return err
}
```

也可以使用选项形式：

```go
token, err := netsec.Verify(
    compact,
    key,
    netsec.WithDetachedPayloadType(externalPayload, "application/json"),
    netsec.WithAlgorithms("HS256", "ES256"),
)
```

## 支持的算法

`HS256`、`HS384`、`HS512`、`RS256`、`RS384`、`RS512`、`PS256`、`PS384`、
`PS512`、`ES256`、`ES384`、`ES512`、`EdDSA`。

`alg:none` 不支持。HMAC 校验会拒绝 PEM 或非对称密钥材料，避免算法混淆。

## 其他 API

- `Parse` / `ParseCompact`：只解析，不验证。
- `VerifyPayload`：验证并只返回载荷。
- `Validate`：验证并只返回错误。
- `Sign` / `SignDetached`：生成测试或调用方需要的紧凑 JWS。
- `KeyFunc`：根据 `kid` 等头部参数选择验证密钥。
- `ErrMalformed`、`ErrInvalidSignature`、`ErrEmbeddedPayload` 等错误可用
  `errors.Is` 判断。
