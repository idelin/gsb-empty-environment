# gem签名剥离与重名成员

gem 的 tar 里有 metadata.gz、data.tar.gz、checksums.yaml.gz 及对应 .sig，攻击者剥掉签名让低安全策略放行，或放两份同名 data.tar.gz 使校验与解包读到不同内容。为Go镜像服务实现可导入的 gem 校验包：成员名重复即拒；HighSecurity 要求 metadata 与 data 都有有效 RSA-SHA256 签名且证书链终止于信任根，MediumSecurity 对已签名者校验但允许整体未签名，只剥掉其中一个 .sig 一律拒绝；checksums.yaml 里的 SHA512 须与实际成员一致。用剥签名、重名成员、半签和错链验证。

## 实现

纯 Go 标准库实现（无第三方依赖），包路径 `example.org/netsec/q0329`。

- `Verify(r, opts)`：统一校验入口，返回 `*Result` 与哨兵错误。
- `ReadMembers`：解析外层 tar，成员名归一化后查重，重名、未知成员、非常规文件、路径穿越一律拒绝。
- `VerifyChecksums`：gunzip 并解析固定形态的 `checksums.yaml`，逐条比对 SHA512。
- PKCS#7 分离签名用 `encoding/asn1` 手工解析，仅接受 RSA-SHA256，校验 `messageDigest` 与 `contentType` 认证属性，并要求三个成员由同一叶子证书签发。
- `HighSecurity` 要求链终止于 `TrustStore.Roots`；`MediumSecurity` 校验签名与嵌入链结构但不固定信任根，整体未签名可放行。

## 用法

```go
import netsec "example.org/netsec/q0329"

// 高安全级别：必须签名且链到信任根
res, err := netsec.Verify(reader, netsec.Options{
    Policy: netsec.HighSecurity,
    Trust:  &netsec.TrustStore{Roots: trustedRoots},
})

// 中安全级别：有签名则校验，无签名放行，剥掉任意一个 .sig 拒绝
res, err = netsec.Verify(reader, netsec.Options{Policy: netsec.MediumSecurity})
```

错误可用标准库 `errors.Is` 对照哨兵错误分类，例如 `ErrDuplicateMember`、
`ErrPartialSignature`、`ErrChecksumMismatch`、`ErrUntrustedChain`、
`ErrUnsupportedAlgorithm`、`ErrUnsigned`。

## 测试

测试在内存中生成 RSA 密钥、三级证书链、PKCS#7 分离签名与 gem tar，无需
外部工具。覆盖剥签名（单个与各种半签组合）、重名成员、校验和伪造/篡改、
错链与不可闭合链、算法降级（RSA-SHA1）、多签名者混用及畸形输入。

```sh
go test ./...        # 全部用例
go test -race ./...  # 竞态检测
```
