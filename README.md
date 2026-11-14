# netsec — 安全更新清单组件（C++20）

供 C++20 自动更新器导入的更新清单校验组件。修复“只校验签名、不校验版本单调”
导致的旧清单重放降级问题。签名覆盖版本、平台、通道与哈希；默认拒绝任何非单调
更新，降级必须持有显式签名授权并写入审计日志。

## 威胁模型与安全保证

- 旧清单重放：候选版本必须严格高于已安装版本，旧清单即使签名合法也被拒绝。
- 静默降级：无授权的低版本清单返回 `DowngradeRejected` 并记录 `downgrade_blocked`，
  不写任何落地文件。
- 显式降级：需要独立授权方签发的 `DowngradeToken`，绑定平台、通道、当前版本、
  目标版本、有效期与 nonce；校验签名、目标匹配与有效期后才放行，并审计
  `downgrade_authorized` / `downgrade_committed`。
- 通道/平台混淆：清单平台与通道必须与已安装状态一致，否则拒绝。
- 签名覆盖：HMAC-SHA256 对规范化清单字节签名，覆盖版本、平台、通道、载荷长度、
  载荷哈希与每个分块哈希；篡改任一字段即签名失效。签名方案经
  `IManifestSigner` / `IManifestVerifier` 抽象，可替换为 Ed25519 等非对称后端。
- 分块校验：逐块校验长度与 SHA-256，拒绝断号、重复块；汇总后再校验整体载荷哈希。
- 安装后回读：先写暂存文件并回读哈希，通过后再原子 rename，失败即清理暂存文件。

## 目录结构

- `include/netsec/`
  - `version.hpp` 语义化版本与三路比较（C++20 `operator<=>`）
  - `crypto.hpp` / `encoding.hpp` 零依赖 SHA-256、HMAC-SHA256、hex、base64
  - `manifest.hpp` 清单模型、规范化序列化、签名/验签抽象与 HMAC 实现
  - `policy.hpp` 单调版本/平台/通道策略、降级授权令牌、时钟与审计接口
  - `verification.hpp` 分块校验器与安装后回读校验
  - `updater.hpp` `UpdateManager` 编排入口与 `FileInstaller` 原子安装
  - `result.hpp` 统一 `VerifyCode` 错误码与 `Outcome`
- `src/` 上述接口实现
- `tests/netsec_tests.cpp` 9 个场景、39 项断言

## 清单格式

规范化清单为确定性的行式文本，签名对完整规范字节计算，传输时附加签名行：

```
netsec-manifest:1
version:1.5.0
platform:darwin-arm64
channel:stable
payload-size:40
payload-sha256:<hex>
chunk:0:16:<hex>
chunk:1:16:<hex>
chunk:2:8:<hex>
signature:<key-id>:hmac-sha256:<base64 32 字节 MAC>
```

解析器拒绝未知字段、重复字段、断号分块、分块长度与载荷不一致等异常输入。

## 集成方式

```cmake
add_subdirectory(path/to/netsec)
target_link_libraries(your_updater PRIVATE netsec::netsec)
```

```cpp
#include "netsec/updater.hpp"

netsec::HmacManifestVerifier manifest_verifier(manifest_key);
netsec::HmacDowngradeTokenVerifier downgrade_verifier(downgrade_key);
netsec::SystemClock clock;
netsec::FileAuditSink audit("/var/log/updater/audit.jsonl");

netsec::UpdateManager updater(
    netsec::InstalledState{installed_version, "darwin-arm64", "stable"},
    manifest_verifier, downgrade_verifier, clock, audit);

netsec::FileInstaller installer(destination);
// 常规更新：内部强制单调版本，旧清单重放会被拒绝
auto result = updater.install_update(signed_wire, payload, installer);

// 显式降级：必须附带授权方签发且未过期的降级令牌
auto downgrade = updater.install_update(signed_wire, payload, installer, token_wire);
```

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试场景：SHA-256 已知向量、合法更新、旧清单重放、签名伪造/篡改、
通道与平台混淆、无授权静默降级、显式授权降级、降级令牌篡改/过期/目标不符、
分块损坏与安装后回读损坏。
