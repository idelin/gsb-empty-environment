# 客户端断言受众注入与重放

## 包结构

`clientassertion` 是零外部依赖的可导入包（JWT compact 序列化自行解析）：

- `Config`：`Issuer`（本服务器标识）、`AllowedAlgs`（算法白名单）、`Verifier`（签名回调）、`Store`（可选，默认内存实现）、`Now`（可选，测试用时钟）。
- `Validator.Authenticate(ctx, Request)`：返回认证通过的 `client_id`；失败时用 `errors.Is` 匹配 `errors.go` 中的哨兵错误。
- `MemoryStore`：按 `client_id` 分区的 jti 防重放存储，惰性清理过期条目，`-race` 安全；多实例部署需自行实现 `Store` 接口（如 Redis 原子 SET NX PX）。

## 用法

```go
import "example.org/netsec/q0638/clientassertion"

v, err := clientassertion.New(clientassertion.Config{
    Issuer:      "https://as.example.com",
    AllowedAlgs: []string{"RS256", "ES256", "PS256"},
    Verifier: func(ctx context.Context, clientID string, h clientassertion.Header,
        signingInput, signature []byte) error {
        // 按 clientID 取注册公钥（JWKS），按 h.Alg/h.Kid 验签
        return verifyWithRegisteredKey(clientID, h, signingInput, signature)
    },
})
if err != nil { /* 配置非法 */ }

func tokenHandler(w http.ResponseWriter, r *http.Request) {
    r.ParseForm()
    clientID, err := v.Authenticate(r.Context(), clientassertion.RequestFromForm(r.PostForm))
    if err != nil {
        // errors.Is(err, clientassertion.ErrReplayedJTI) 等
        http.Error(w, "invalid_client", http.StatusUnauthorized)
        return
    }
    _ = clientID
}
```

## 校验规则

- 双重凭据（`client_secret` 与 `client_assertion` 同时出现）直接拒绝，且先于 JWT 解析。
- `client_assertion_type` 必须是 `urn:ietf:params:oauth:client-assertion-type:jwt-bearer`。
- `alg` 必须命中调用方白名单；`none` 与 `HS*` 即使被误加入白名单也强制剔除。
- `iss`、`sub` 均须逐字等于请求的 `client_id`。
- `aud` 必须是单个 JSON 字符串且等于 `Issuer`；数组（含正确值）、`null`、令牌端点地址等一律拒绝。
- `exp`、`iat` 必填；`exp-iat <= 300s`；`iat` 超前当前时间不得超过 30s；已过期断言拒绝（无宽限期）。
- `jti` 必填非空；在 `exp` 之前按 `client_id` 分区只接受一次，过期后同 jti 可复用。
- 签名只通过 `Verifier` 回调验证，回调失败统一包装为 `ErrSignatureRejected`。

授权服务器用 private_key_jwt 认证客户端时，如果断言的 aud 允许写成多个端点中的任意一个，攻击者就能把客户端给某个端点签的断言拿到另一个端点重放。为Go授权服务器写可导入的客户端断言校验包，签名由回调验证：alg 取调用方白名单（拒绝 none 与 HS 系列）；iss 与 sub 都必须等于 client_id；aud 必须是单个字符串，且逐字等于本服务器的 issuer 标识，数组形式一律拒绝，即使数组里有正确值；exp 必填，且 exp 减 iat 不超过 300 秒，iat 不得比当前时间晚 30 秒以上；jti 必填，在 exp 之前只接受一次，按 client_id 分区；同一请求同时带 client_secret 与 client_assertion 时拒绝；client_assertion_type 须为 urn:ietf:params:oauth:client-assertion-type:jwt-bearer。用 aud 数组含正确值、aud 为令牌端点地址、jti 重放、有效期过长和双重凭据验证。
