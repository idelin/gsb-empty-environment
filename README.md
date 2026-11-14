# C++20 CRL 范围与撤销证据组件

`netsec` 提供 DER 解析、CRL 范围判定、签发者授权、base/delta 链接和撤销状态计算。它不会把“不适用的 CRL”当成未撤销证明：其他 CA 的 CRL、分发点不匹配、证书类型不匹配或签发者未获委托时，结果固定为 `RevocationStatus::Unknown`，而不是 `Good` 或 `Revoked`。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

库目标为 `netsec`，公开头文件为 `netsec.hpp`。CMake 使用 C++20；macOS 上使用系统 `Security.framework` 验证 RSA/ECDSA 签名，其他平台在未接入签名后端时按失败关闭处理。

## 主要 API

```cpp
#include "netsec.hpp"

netsec::Bytes leaf = ...;
netsec::Bytes issuer = ...;
netsec::Bytes crl = ...;

netsec::ValidationContext context;
context.now = std::chrono::system_clock::now();
// 显式委托的 CRL 签发证书（可选）
context.trusted_crl_signer_certificates.push_back(delegated_signer_der);

netsec::CrlValidationResult result =
    netsec::check_revocation(leaf, issuer, crl, context);

if (result.status == netsec::RevocationStatus::Revoked) {
    // result.revocation_entry 给出序列号、撤销时间和原因
} else if (result.status == netsec::RevocationStatus::Good) {
    // 只有在 CRL 的签发者、范围、新鲜度和签名全部有效时才成立
} else {
    // Unknown：没有可用的撤销证据，调用方必须失败关闭
}
```

需要同时导入多张 CRL 时：

```cpp
netsec::CrlScopeComponent store(context);
store.import_crl(base_crl_der);
store.import_crl(delta_crl_der);
auto result = store.check(leaf, issuer);
```

`parse_crl()` 和 `parse_certificate()` 可用于检查解析后的字段，例如：

```cpp
auto crl = netsec::parse_crl(crl_der);
crl->scope.distribution_point;       // issuingDistributionPoint 名称
crl->scope.only_contains_user_certs;
crl->scope.only_contains_ca_certs;
crl->scope.indirect_crl;
crl->crl_number;
crl->base_crl_number;                // 仅 delta CRL
```

## 校验规则

- CRL 签发者必须是被校验证书的直接签发者，或在 `ValidationContext` 中显式信任、或由证书 CRLDP 的 `cRLIssuer` 指明并可链到证书签发者的委托签发者。CRL 签名和签发证书的 `cRLSign` 用法均会校验。
- 非间接 CRL 只能覆盖由 CRL 签发者签发的证书；间接 CRL 还要求条目的 `certificateIssuer` 与被校验证书的 issuer 一致。
- `issuingDistributionPoint` 的 fullName / nameRelativeToCRLIssuer 与证书 `cRLDistributionPoints` 必须匹配；`onlyContainsUserCerts`、`onlyContainsCACerts`、`onlyContainsAttributeCerts` 与证书类型不一致时拒绝。
- 只覆盖部分原因（`onlySomeReasons`）的 CRL 在未命中撤销条目时只能得到 `Unknown`，不能证明 `Good`。
- delta CRL 必须带 `cRLNumber` 和 `deltaCRLIndicator`，并且与 base CRL 的 issuer、scope 一致；delta 序号必须从 base 序号加一开始连续衔接。缺 base、缺中间 delta、base 号不一致或 scope 不一致均失败关闭。
- `nextUpdate` 已过、`thisUpdate` 在未来、未知关键扩展（CRL 条目扩展同样检查）和签名验证失败都会拒绝该证据。
- 撤销条目中的 `removeFromCRL` 会在 delta 链中移除先前的 hold；未过撤销时间或不属于当前 CRL 原因范围的条目不会误报。

## 回归测试

`tests/test_netsec.cpp` 覆盖：

- 另一 CA 签发、序列号相同的 CRL 不能撤销本证书；
- 分发点错配、CA-only/user-only 类型错配；
- 同范围 CRL 的 `Good` 与 `Revoked` 结论；
- delta 无 base、delta 序号缺口、delta scope 不一致；
- 合法的 base + delta 链接与 `removeFromCRL` 语义；
- 过期、未知关键扩展、签名损坏；
- 显式信任和 `cRLIssuer` 委托链下的间接 CRL；
- `CrlScopeComponent` 导入后复用。

测试夹具由 `tests/gen_fixtures.cpp` 生成；仓库中已包含稳定 DER 夹具，因此 `ctest` 不依赖生成步骤。
