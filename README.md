高安全金融与政务 API 网关要求客户端提交 Nested JWT（RFC 7519 Section 5.2：内层 JWS 签名 + 外层 JWE 加密，即 Sign-then-Encrypt），若网关仅按分段数判断 3 段走 JWS、5 段走 JWE 而未强制校验外层 JWE 保护头 `cty="JWT"`、允许攻击者剥离外层 JWE 直接提交明文 JWS 或剥离内层 JWS 仅提交用服务端公钥加密的伪造 JWE（Surreptitious Forwarding / 签名剥离攻击），抑或未绑定内外层 `iss`/`sub`/`aud`/`kid` 与嵌套深度上限，攻击者无需持有签名私钥即可伪造任意高权限声明。为 Go 认证安全宿主实现可导入的 Nested JWT（JWE-wrapping-JWS）严格解封装与防剥离验证包，严禁联网（仅用标准库 `crypto/aes`/`crypto/cipher`/`crypto/ecdsa`/`crypto/rsa`/`crypto/sha256`）。组件严格执行：（1）外层 JWE 强制校验：必须为 5 段紧凑序列化、策略要求嵌套模式时硬拒绝直接提交的 3 段未加密 JWS、保护头必须含大小写规范化匹配的 `cty="JWT"`、仅允许白名单加密算法（如 `RSA-OAEP-256` + `A256GCM`）且拒绝压缩炸弹 `zip="DEF"`；（2）内层 JWS 强制校验：解密外层所得明文必须严格为合法 3 段 JWS（硬拒绝把明文 JSON 直接当声明接受的签名剥离攻击，同时限制最大嵌套层数 ≤2 防递归嵌套 DoS），内层仅允许白名单非对称签名算法（硬拒绝 `none` 与对称算法）且严格按 `kid` 绑定的客户端签名公钥验签；（3）跨层一致性与防重放：校验内外层重复声明（`iss`/`sub`/`aud` 若同时出现在 JWE 保护头与内层载荷）完全一致，并基于 `(iss, jti)` 防重放。附剥离外层 JWE、剥离内层 JWS 直接加密伪造 JSON、缺失 `cty="JWT"`、三层递归嵌套 DoS 与内外层 `aud` 冲突测试。

## nestedjwt 包

可导入包位于 `nestedjwt/`，零第三方依赖、无网络访问，加密原语仅使用 Go 标准库（`crypto/aes`、`crypto/cipher`、`crypto/ecdsa`、`crypto/rsa`、`crypto/sha256`，以及配套的 `encoding/asn1`、`encoding/base64`、`encoding/json`、`sync`、`time`）。

### 强制安全策略（`DefaultPolicy`）

- 外层 JWE：必须为 5 段紧凑序列化；`RequireNested=true` 时硬拒绝 3 段裸 JWS（`ErrBareJWSRejected`）；保护头强制 `cty="JWT"`（按 RFC 7515 4.1.10 大小写不敏感匹配）；`alg`/`enc` 走白名单（默认仅 `RSA-OAEP-256` + `A256GCM`）；任何 `zip` 一律拒绝以防解压炸弹；非空 `crit` 扩展拒绝。
- 内层 JWS：JWE 明文必须严格是 3 段 JWS，直接加密裸 JSON/字符串返回 `ErrInnerNotJWS`（签名剥离 / Surreptitious Forwarding）；`alg=none` 与 `HS*` 对称算法硬拒绝；仅白名单非对称算法（默认 `ES256`/`RS256`/`PS256`），按 `kid` 解析客户端公钥验签；最大嵌套深度默认 2，三层结构返回 `ErrNestedDepth`。
- 跨层与防重放：外层保护头与内层载荷同时出现的 `iss`/`sub`/`aud` 必须一致（`aud` 按无序集合比较）；按 `kid` 绑定密钥，可选 `MatchCrossLayerKID`；基于 `(iss, jti)` 的内存防重放存储（TTL + 容量上限），并提供 `ReplayStore` 接口接入分布式存储；另含 `exp`/`nbf` 时钟偏差校验与令牌大小上限。

### 集成示例

```go
store := nestedjwt.MapKeyStore{
    DecryptionKeys: map[string]crypto.PrivateKey{"gateway-2026": serverRSAPrivateKey},
    SigningKeys:    map[string]crypto.PublicKey{"client-42": clientECDSAPublicKey},
}
verifier := nestedjwt.New(store) // 默认即为严格嵌套策略

verified, err := verifier.Verify(compactNestedJWT)
if err != nil {
    switch {
    case errors.Is(err, nestedjwt.ErrBareJWSRejected):
        // 剥离了外层 JWE
    case errors.Is(err, nestedjwt.ErrInnerNotJWS):
        // 剥离了内层 JWS，直接加密伪造 JSON
    case errors.Is(err, nestedjwt.ErrReplayed):
        // (iss, jti) 重放
    }
    return err
}
_ = verified.Claims
```

### 测试

`go test ./...` 离线运行（建议配合 `GOPROXY=off`）。测试覆盖要求的五类攻击：剥离外层 JWE、剥离内层 JWS 直接加密伪造 JSON、缺失/错误 `cty`、三层递归嵌套（`JWE(JWS(JWS))` 与 `JWE(JWE(JWS))`）、内外层 `aud` 冲突，并附加 `none`/HMAC 拒绝、白名单、GCM 篡改、密钥类型错配、跨层 `iss`/`sub`/`kid`、重放与生命周期等用例。
