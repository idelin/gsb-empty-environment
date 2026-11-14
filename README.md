# 安全启动撤销策略组件

`netsec` 提供一个可被 C++20 固件更新服务直接链接的启动策略组件，用于防止
`dbx` 降级、空撤销列表、签名替换以及恢复模式绕过撤销检查。

## 安全边界

- `db`、`dbx`、`KEK`、`PK` 均按变量独立版本号**严格单调递增**；版本相等或回退一律拒绝。
- 所有四个变量的更新包都必须包含至少一个条目，禁止通过更新清空变量。
- `dbx` 只增不减：即使攻击者把版本号调高，也不能删除已有撤销项。
- 更新包签名覆盖目标变量、版本、内容哈希、模式、出厂标志、签名者身份和规范化条目集合。
- 写入前先验证签名、哈希、授权和反回退；写入后立即回读，字段或哈希不一致时恢复旧状态并拒绝更新。
- 启动判定先检查 `image_id` 与完整签名链上的每个身份是否命中 `dbx`；命中即拒绝。
- 恢复模式只增加显式的恢复/出厂信任锚，**永不跳过 `dbx` 检查**。
- 出厂密钥安装仅允许显式策略开启、恢复模式、物理存在确认，且只能写入 `PK`/`KEK`。

## 使用方式

```cpp
#include "netsec.hpp"

netsec::MemoryVariableStore store;
netsec::HmacSha256Verifier verifier;
verifier.addKey("vendor-key", secret);

netsec::Policy policy;
policy.update_signers = {"vendor-key"};
policy.recovery_signers = {"recovery-key"};
policy.factory_signers = {"factory-key"};
policy.factory_key_ids = {"factory-root"};
policy.allow_factory_key_install = true;

netsec::SecureBootPolicy service(store, verifier, policy);

netsec::UpdatePackage package;
package.target = netsec::VariableId::dbx;
package.version = 7;
package.entries = {{"revoked-signer", {}}};
package.signer_id = "vendor-key";
package = netsec::sealUpdatePackage(std::move(package), secret);
auto result = service.applyUpdate(package, {netsec::UpdateMode::normal, false});
if (!result.ok()) {
  // result.status / result.detail 给出明确拒绝原因
}

auto decision = service.evaluateBoot({"image-digest", {"vendor-root"}});
```

生产环境应通过 `ISignatureVerifier` 注入非对称签名验证（例如 RSA/Ed25519），
并把 `IVariableStore` 实现为具备原子写入和回滚能力的认证 NVRAM 适配器。
内置的 HMAC-SHA256 实现用于自包含测试和参考存储，不应把长期私钥/密钥编译进固件。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_CXX_STANDARD=20
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：合法更新与撤销生效、`dbx` 版本回退、版本号升高但删除撤销项、空列表、
签名替换/目标替换/版本替换/载荷替换、四变量版本单调性、存储失败与回读不一致、
恢复模式与出厂密钥显式策略，以及恢复模式下已撤销组件仍不能启动。
