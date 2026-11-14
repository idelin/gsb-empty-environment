# 芯片身份认证组件

面向 C++20 硬件认证组件的芯片身份库。它把芯片唯一标识、CA 签发的绑定证书、物理指纹、启动挑战响应、性能/行为指纹和供应链记录放进同一条信任链，并且**只信任显式登记过的芯片**。

## 信任模型

- 芯片登记记录包含证书、芯片挑战密钥、性能基线以及完整供应链链。登记时会验证 CA 签名、挑战密钥承诺、性能参数和供应链签名。
- 启动认证会检查芯片 ID 是否已登记、型号、证书是否缺失或与登记证书不一致、物理指纹承诺、挑战响应、行为探针输出和性能偏差。
- 挑战是每次认证新生成的随机数；响应由芯片使用仅登记端持有的挑战密钥计算，使用常量时间比较。
- 供应链步骤按序号、芯片 ID、前序摘要和 HMAC 摘要串成链，任何字段或链路顺序被改写都会校验失败。
- 每次认证结果写入认证器内部审计链，可通过 `verifyAuditTrail()` 检查；登记库存保留完整供应链步骤和登记证书供追溯。

默认配置同时强制供应链、行为探针和性能指纹检查。关闭这些选项会削弱信任，不应在生产启动路径使用。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：

- 合法且已登记的芯片通过认证，链头和审计记录可验证。
- 未登记的假芯片即使持有有效 CA 证书也会被拒绝。
- 芯片 ID 克隆会因物理指纹不符或挑战响应错误被拒绝。
- 型号不符、证书缺失、证书替换和异常响应分别返回对应拒绝码。
- 性能指纹偏离登记基线时拒绝。
- 被篡改或未签名的供应链记录不能登记，不能进入信任集。

## 集成方式

```cmake
find_package(netsec CONFIG REQUIRED)
target_link_libraries(your_authenticator PRIVATE netsec::netsec)
```

也可以把本目录作为 `add_subdirectory(netsec)` 使用。若需安装：`cmake --install build --prefix <prefix>`。

典型启动流程：

```cpp
#include "netsec.hpp"

netsec::ChipCertificateAuthority ca("vendor-root", ca_signing_key);
netsec::ChipRegistry registry(ca);

netsec::ChipRegistration registration;
registration.certificate = ca.issue(chip_descriptor, provisioned_key);
registration.challenge_key = provisioned_key;
registration.performance_profile = {1200.0, 5.0, 3};
registration.supply_chain = supply_chain_steps;  // 已签名的链路
if (!registry.registerChip(std::move(registration)).accepted) {
  // 终止启动或不授予设备身份
}

netsec::ChipAuthenticator authenticator(registry);
const auto result = authenticator.authenticate(chip_endpoint);
if (!result.trusted) {
  // 按 result.rejection / rejectionCodeName() 记录并拒绝启动
}
```

`ChipEndpoint` 是硬件适配层，需要实现：

- `identify()`：返回 ID、型号和芯片侧物理指纹。
- `certificate()`：返回登记时签发的证书，缺失时返回 `std::nullopt`。
- `respondToChallenge()`：用挑战密钥计算 `CHALLENGE-RESPONSE/1` 域分离 HMAC。
- `runPerformanceProbe()`：执行行为探针，返回 64 位十六进制输出、周期数和样本数。

挑战响应和设备侧探针的规范化格式和域分离标签见 `netsec.cpp` 中的 `challengeResponse` 与 `performanceProbeOutput`。生产固件应在安全元件/芯片内部执行这两项计算，主机只下发随机挑战并验证结果。

## 拒绝码

`AuthenticationResult::rejection` 可区分未登记芯片、型号不符、证书缺失/无效/绑定不符、物理指纹不符、挑战失败、性能异常、供应链不可信、格式错误和端点异常。任何拒绝结果都意味着 `trusted == false`。

## 安全边界

本实现使用 HMAC-SHA256 作为无第三方依赖的证书、挑战响应和供应链签名原语，适合作为硬件认证组件的可测试参考实现与适配层。生产环境应把 CA 私钥和芯片挑战密钥放入 HSM/安全元件，并按设备要求替换为 X.509/Ed25519/ECDSA 或厂商安全通道。性能指纹必须来自可信芯片侧测量；若适配层本身被攻破，主机侧无法单靠时间或计数器证明真实性能。
