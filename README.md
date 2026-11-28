金融与政务 OAuth 2.0 授权服务器在强制启用 JWT-Secured Authorization Request（JAR，RFC 9101）保护前端授权请求参数不被浏览器篡改时，若同时接受未签名的明文查询参数降级、允许客户端在查询串中携带与 `request` JWT 内部冲突的 `redirect_uri`/`scope`/`state` 并优先采用外层未签名值、未校验 `request` 对象保护头 `typ="oauth-authz-req+jwt"`、或漏检 `iss == client_id`、`aud == authorization_server_issuer`、`exp`/`nbf`/`jti` 防重放，攻击者可将普通 JWT 冒充授权请求对象或通过外层参数覆盖劫持回调地址。为 Go 认证安全宿主实现可导入的 RFC 9101 JAR 授权请求严格解析与防降级验证包，严禁联网（仅用标准库 `crypto/ecdsa`/`crypto/rsa`/`crypto/sha256`）。组件严格实现：（1）强模式防降级门禁：当客户端或服务端策略要求 `require_signed_request_object=true` 时，硬拒绝缺失 `request` 参数的明文授权请求，硬拒绝同时传递 `request` 与 `request_uri`；（2）外层与内层参数防混淆：校验查询串中的 `client_id`（若提供）必须与 `request` JWT 载荷内的 `client_id` 精确一致；**严禁**采信任何外层未签名的 `redirect_uri`/`scope`/`state`/`nonce`/`code_challenge`（若外层与内层值冲突一律硬拒，且有效业务参数只能严格取自已验签的 `request` JWT 内部）；（3）JWT 密码学与声明校验：仅允许白名单非对称 `alg`（硬拒绝 `none` 与对称算法）、强制 `typ="oauth-authz-req+jwt"`（防与 ID Token/Access Token 类型混淆）、强制校验 `iss == client_id`、`aud` 精确匹配本授权服务器 Issuer、`exp`/`nbf` 有效且有效期不超过策略上限（如 300 秒）、`jti` 原子防重放，并硬拒绝内嵌递归 `request`/`request_uri` 声明。附明文降级拦截、外层 `redirect_uri` 覆盖冲突、非 `oauth-authz-req+jwt` 混淆与 `jti` 重放测试。

## 实现：`jar` 包

可导入包路径 `formal.local/w28-0d0ffd9ddb/jar`，零第三方依赖，全部密码学
（RSA PKCS#1 v1.5 / RSA-PSS / ECDSA、SHA-256/384/512、base64url、JWT 编解码）
仅使用 Go 标准库，不发起任何网络请求；`request_uri` 不在进程内拉取。

### 文件结构

- `jar/validator.go`：策略 `Policy`、入口 `ParseQuery`/`ParseHTTPRequest`、防降级门禁、外层/内层防混淆、时间窗与有效期校验。
- `jar/jwtsupport.go`：紧凑 JWT 解析、非对称 `alg` 白名单与验签（硬拒 `none`/`HS*`）。
- `jar/claims.go`：`aud` 多形态解析、载荷原始键保留（检测嵌套 `request`/`request_uri`）、严格字符串声明提取。
- `jar/client.go`：`Client`/`ClientStore` 抽象与按 `kid` 索引公钥的内存注册表。
- `jar/replay.go`：`ReplayCache` 接口与线程安全、懒过期的内存 `jti` 防重放实现（可替换为 Redis SET NX）。
- `jar/errors.go`：哨兵错误（`errors.Is` 可匹配）与 `*ValidationError` 包装。
- `jar/*_test.go`：13 组安全测试，覆盖全部攻击面与跨算法正/负路径。

### 强制安全语义

1. **防降级门禁**：服务端或客户端任一策略要求签名时，无 `request` 的明文请求返回 `ErrPlaintextDowngrade`；`request` 与 `request_uri` 同时出现返回 `ErrBothRequestAndRequestURI`，单独 `request_uri` 返回 `ErrRequestURINotSupported`；重复查询参数返回 `ErrDuplicateParameter`。
2. **防参数混淆**：外层 `client_id` 必须与 JWT 内层精确一致；除传输参数（`request`/`request_uri`）外，外层出现的任何键都必须在已验签内层存在且值完全相等，否则 `ErrOuterInnerConflict`。可信结果 `AuthorizationRequest.Params` 及便捷字段只取自 JWT 内层。
3. **JWT 校验**：`alg` 必须在非对称白名单（默认 `RS256`/`PS256`/`ES256`）内，密钥族不匹配即验签失败；`typ` 必须精确为 `oauth-authz-req+jwt`（区分大小写）；`iss == client_id`；`aud` 必须为单值且等于本 AS Issuer；`exp`/`nbf` 带 leeway 校验，`exp-nbf`（或 `exp-iat`）不得超过 `MaxLifetime`（默认 300s）；`jti` 非空且经原子 `Reserve` 防重放；载荷内嵌 `request`/`request_uri` 返回 `ErrNestedRequest`。

### 宿主接入示例

```go
registry := jar.NewRegistry()
client := jar.NewRegisteredClient("gov-client-001", true) // 客户端强制 JAR
client.AddKey("kid-rsa", rsaPublicKey)
registry.Register(client)

validator := jar.NewValidator(jar.Policy{
    Issuer:                     "https://as.example.gov.cn",
    RequireSignedRequestObject: true,
    MaxLifetime:                300 * time.Second,
    Leeway:                     30 * time.Second,
}, registry, jar.NewMemoryReplayCache())

authzReq, err := validator.ParseHTTPRequest(httpReq)
// err 可用 errors.Is 匹配 jar.ErrPlaintextDowngrade / ErrOuterInnerConflict /
// ErrInvalidTyp / ErrAlgorithmNotAllowed / ErrReplayedJTI 等哨兵。
// authzReq.RedirectURI、Scope、State、Nonce、CodeChallenge 均来自验签后的内层载荷。
```

### 运行测试

```sh
go test -race -count=1 ./...
go vet ./...
```
