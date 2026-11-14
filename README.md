# netsec: 有序 PCR 度量日志校验

`netsec` 是一个无外部依赖的 C++20 远程证明校验组件。它不再只比较最终
PCR，而是把策略中的参考启动链与导入的度量日志逐事件、逐 PCR bank 重放，
并将重放结果与 quote 中的 PCR 和签名绑定。

## 校验模型

- `MeasurementPolicy::expected_events` 定义唯一合法的启动链。每个事件包含
  事件类型、PCR 索引和按算法列出的参考摘要。
- `MeasurementLog` 必须与参考链长度、事件类型顺序、PCR 索引、算法列表和
  摘要逐项一致。缺失事件、额外事件、顺序调换、未知类型和摘要替换都会失败。
- `MeasurementPolicy::pcr_banks` 必须被每个事件完整覆盖；支持 SHA-1、
  SHA-256、SHA-384 和 SHA-512。初始 PCR 为零，扩展公式为
  `PCR' = H(PCR || event_digest)`。
- `verify_measurement_log` 返回每个 bank/PCR 的重放值，并可与调用方持有的
  PCR 集合逐值比较。
- `Quote` 的 canonical payload 包含 nonce、AK id、policy id、签名算法和完整
  PCR 集合。`verify_quote` 校验 nonce、policy、AK 信任映射、
  RSA PKCS#1 v1.5 签名和 PCR 值。
- AK 使用至少 2048 位的 RSA 公钥，并按 `ak_id` 固定到
  `QuotePolicy::trusted_aks`。`expected_ak_id` 非空时进一步固定唯一 AK。
- `verify_attestation` 把度量链重放和 quote 校验绑定在一起；调用方不能通过
  提交一条与策略无关但自洽的 quote 绕过参考启动链。

## 主要 API

```cpp
#include "netsec.hpp"

netsec::MeasurementPolicy measurement_policy{
    .id = "boot-chain-v1",
    .expected_events = reference_events,
    .pcr_banks = {netsec::HashAlgorithm::Sha256,
                  netsec::HashAlgorithm::Sha384},
};

netsec::QuotePolicy quote_policy{
    .expected_nonce = request_nonce,
    .policy_id = "boot-chain-v1",
    .expected_ak_id = "ak-primary",
    .trusted_aks = {{"ak-primary", provisioned_ak}},
    .pcr_selection = {},
};

netsec::MeasurementLog log = import_log_from_attestation_report(report);
netsec::Quote quote = import_quote_from_attestation_report(report);

netsec::VerificationResult result =
    netsec::verify_attestation(log, measurement_policy, quote, quote_policy);
if (!result) {
    reject_attestation(result.error, result.detail);
}
```

`MeasurementLogVerifier` 和 `AttestationVerifier` 提供可直接注入服务的持有
策略对象。`replay_measurement_log` 只对已经通过完整校验的日志返回 PCR 值。

## 失败分类

`ValidationError` 区分 `MissingEvent`、`ExtraEvent`、
`EventOrderMismatch`、`UnknownEventType`、`DigestMismatch`、
`PcrIndexMismatch`、`PcrValueMismatch`、`QuoteNonceMismatch`、
`UntrustedAk`、`QuotePolicyMismatch` 和 `QuoteSignatureInvalid` 等失败。
校验采用失败关闭策略；空日志、空策略、空 nonce、无信任 AK 或弱 RSA 密钥
都会被拒绝。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖顺序调换、事件缺失、摘要替换、未知/插入事件、PCR bank 算法错配、
PCR 值替换、nonce 重放、非授权 AK、策略替换、伪造签名，以及合法日志和
合法 quote 的完整校验。
