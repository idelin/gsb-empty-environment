# kb-jwt绑定披露

持有者证明换一套披露后仍能附在原来的kb-jwt上。kb-jwt的typ必须是kb+jwt，sd_hash等于规范化披露字节的SHA-256，cnf里的jkt等于证明密钥。披露不变且jkt匹配的证明接受。可导入的Go库供验证组件调用。增删一条披露后再用原证明。

## 用法

验证组件直接导入本模块（`example.org/netsec/q0116`，包名 `netsec`）：

```go
import netsec "example.org/netsec/q0116"

// presentation 为 <issuer-jwt>~<disclosure>~...~<kb-jwt>
v, err := netsec.VerifyKeyBinding(presentation)
```

校验规则（全部通过才接受）：

- kb-jwt 头 `typ` 必须等于 `kb+jwt`，`alg` 为 `EdDSA`；
- `sd_hash` 必须等于规范化披露字节（issuer JWT 与每条披露均以 `~` 结尾拼接）的 SHA-256 的 base64url，因此增删或改动任何一条披露后，原证明必然失败（`ErrSDHashMismatch`）；
- issuer JWT 的 `cnf.jkt` 必须等于证明密钥的 RFC 7638 JWK thumbprint，否则返回 `ErrJKTMismatch`；
- kb-jwt 签名必须由证明密钥有效签发。

持有者侧用 `SignKeyBinding` 针对当前披露集合签发新证明，`AssemblePresentation` 组装呈现；证明密钥可经 kb-jwt 头内嵌 JWK 自包含解析，或调用 `VerifyKeyBindingWithKey` 带外传入。签发方 JWT 本身的签名校验不在本库范围内，由调用方按其信任链处理。
