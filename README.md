# RFC 3161 时间戳令牌绑定组件

本组件修复“旧令牌被附到新文件上”的摘要替换问题：令牌不是只提供
`genTime`，而是把 `messageImprint` 的**算法 OID**和**摘要字节**同时绑定到
调用方给出的文件摘要。算法不同、摘要不同、nonce 不匹配、序列号/签发者不
匹配、证书链不可信或证书已撤销，都会返回失败而不是“时间有效”。

实现完全自包含（DER 解析、SHA-1/SHA-2/MD5、RSA PKCS#1 v1.5、
RSA-PSS、ECDSA P-256/P-384/P-521、X.509/CMS/CRL/OCSP 子集），无需链接
OpenSSL 或其他密码库。公开接口位于 `netsec.hpp`，使用 C++20。

## 快速使用

```cpp
#include "netsec.hpp"

auto imported = netsec::import_timestamp_token(token_der);
if (!imported) {
    // imported.code / imported.message
}

netsec::VerificationPolicy policy;
policy.expected_nonce = "0x43c3d92a0f652985";  // request nonce, replay protection
policy.expected_serial_number = "2";           // TSTInfo serialNumber
policy.expected_issuer = "CN=Test Root CA";     // signer issuer
policy.expected_tsa = "CN=Test TSA,O=Test Org,C=CN";
policy.allow_weak_message_imprint = false;      // production policy
policy.revocation = netsec::RevocationMode::Required;

netsec::TrustStore trust;
trust.add_anchor_file("/etc/tsa/root-ca.pem");

netsec::RevocationEvidence revocation;
revocation.crls.push_back(crl_der);            // DER or PEM

netsec::TimestampVerifier verifier(policy, trust);
const auto digest = netsec::Digest::compute(
    netsec::HashAlgorithm::Sha256, artifact_bytes);
const auto result = verifier.verify(imported.token, digest);
```

也可以直接校验文件：

```cpp
auto result = verifier.verify_file(imported.token, "/path/to/artifact",
                                   netsec::HashAlgorithm::Sha256);
```

`TokenBinder`、`TokenBinding`、`TimestampTokenBinder` 都是
`TimestampVerifier` 的别名；`ITimestampTokenBinding` 是供上层时间戳校验
组件注入的抽象接口。

## 逐项校验

- `messageImprint`：算法必须与文件摘要算法完全一致，摘要字节必须逐字节
  相等；长度也要符合算法。
- `nonce`：默认拒绝缺失 nonce 的令牌；设置 `expected_nonce` 后必须完全
  相等。生产环境应同时设置 `require_expected_nonce = true`。
- `serialNumber`、`issuer`、`tsa`、`policy`：按配置逐项比较。
- `genTime`：支持窗口、未来时间拒绝和时钟偏移；证书有效期按 `genTime`
  检查。
- CMS：校验 `contentType`/`messageDigest` 签名属性、ESS
  signing-certificate、SignerInfo 摘要算法和 CMS 签名本身。
- 证书链：从签名者证书构建到配置的 trust anchor，校验每级签名、CA 约束、
  keyUsage、TSA EKU 和证书有效期。
- 撤销：支持 CRL（DER/PEM）和 OCSP 响应，也可注入
  `RevocationChecker` 回调。`RevocationMode::Required` 时缺少撤销证据会
  失败。

## 默认值

- 默认要求 nonce 存在。
- 默认校验 CMS 签名和证书链；证书带 EKU 时要求包含 timeStamping，并且要求
  配置 trust anchor。
- 默认 `RevocationMode::IfProvided`：未提供撤销证据时跳过；一旦提供
  CRL/OCSP/状态回调，就对路径中的证书做失败关闭式校验。生产环境建议改为
  `Required`，即始终要求正向的 Good 状态。
- 默认允许 SHA-1 以兼容旧令牌；生产环境建议设置
  `allow_weak_message_imprint = false`。
- `TrustStore::allow_embedded_trust_anchor` 仅用于隔离测试，默认关闭。

## 测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：合法 RSA/ECDSA（P-256/P-384/P-521）及 SHA-1 兼容令牌、摘要替换、令牌复用、nonce 缺失/不匹配、
序列号/签发者/策略/时间不匹配、不可信链、签名篡改、CRL 撤销、Base64/PEM
导入以及 `ITimestampTokenBinding` 接口。
