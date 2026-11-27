# WebAuthn 注册证明验证包

为 Go 认证宿主实现可导入的 WebAuthn Level 2/3 注册证明（Attestation）验证包。纯标准库实现，
无任何网络访问或第三方依赖：仅使用 `crypto/x509`、`crypto/ecdsa`、`crypto/sha256`、
`crypto/sha512`、`encoding/asn1`、`encoding/json` 与内置确定性 CBOR 子集解析器。

## 入口

```go
result, err := webauthn.VerifyRegistration(attestationObject, clientDataJSON, challenge, policy)
```

## 文件结构

- `webauthn/cbor.go`：确定性 CBOR 子集解析器。仅接受最小编码、定长容器；拒绝不定长、浮点、Tag、非规范 map 键序与尾随字节。
- `webauthn/authenticator.go`：`authenticatorData` 二进制严格解析与 EC2 COSE 公钥（ES256/ES384/ES512，P-256/P-384/P-521）解析，含曲线点校验与坐标长度校验。
- `webauthn/clientdata.go`：`CollectedClientData` 校验，重复 JSON 键检测、`type`/`challenge`/`origin` 精确匹配、`crossOrigin=true` 硬拒。
- `webauthn/packed.go`：`packed` Full/Self 与证书元数据、AAGUID 扩展、信任链校验。
- `webauthn/attestation.go`：`Policy`、结果类型、`none` 策略与顶层编排（`rpIdHash`、UP/AT/UV 策略、签名载荷 `authData || SHA256(clientDataJSON)`）。
- `webauthn/signature.go`：仅接受 ASN.1 DER 的 ECDSA 验签（拒绝 IEEE P1363 裸 `r||s` 形式）。
- `webauthn/errors.go`：可 `errors.Is` 分类的哨兵错误。

## 用法

```go
import "formal.local/w28-fb65666288/webauthn"

roots := x509.NewCertPool()
roots.AddCert(enterpriseFIDORoot)

policy := &webauthn.Policy{
    RPID:                    "login.example.com",
    Origin:                  "https://login.example.com",
    RequireUserVerification: true,
    AllowNoneAttestation:    false, // 企业硬件证明流程硬拒 none
    TrustAnchors:            roots,
    AllowedAAGUIDs:          []webauthn.AAGUID{approvedModelAAGUID},
}

result, err := webauthn.VerifyRegistration(attestationObject, clientDataJSON, issuedChallenge, policy)
```

## 安全校验要点

- 标志位：强制 `UP=1`、`AT=1`；`UV` 由策略开关强制；`BE=0 && BS=1` 直接拒绝；保留位（bit 1/7）拒绝。
- Full 证明证书：`Version==3`、`IsCA==false`、`Subject.OU` 精确含 `Authenticator Attestation`、
  OID `1.3.6.1.4.1.45724.1.1.4` 扩展为 OCTET STRING 且 16 字节内容精确等于 `authData.aaguid`；
  必须链到注入的信任根，且 AAGUID 命中白名单，否则失败即关闭（fail-closed）。
- 签名算法以 `attStmt.alg` 为准，并要求与凭据 COSE 算法一致；证书 `SignatureAlgorithm`
  仅描述 CA 如何签发该证书，不作为证明签名算法依据。

## 测试

`go test ./...` 覆盖正向流程与对抗用例：非法标志组合、AAGUID 扩展错配、跨源注册、
自签证书冒充 Full Attestation、证书版本/基本约束/OU 违规、信任链与白名单缺失、
签名篡改与非 DER 编码、CBOR 非规范编码等。
