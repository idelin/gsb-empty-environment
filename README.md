# netsec：可导入的 C++20 保护范围组件

本库解决“机器范围保存用户密钥后，另一个账户调用解密接口即可取得原用户口令材料”的问题。它是一个自包含、无外部依赖的 C++20 组件，可由凭据服务直接链接或导入。

## 安全模型

每个信封（envelope）在加密前绑定以下字段，任一字段被篡改都会导致认证失败：

- 所有者 SID 与登录会话（`Subject`）
- 保护范围（`User` / `Machine`）
- 可选熵（只保存 SHA-256 承诺，绑定在认证头中）
- 信封格式版本
- 密钥代次
- 每个信封独立的 128 位撤销 ID
- 随机 salt 与 ChaCha20 nonce

解密顺序为：解析格式/版本 -> 校验密钥代次与撤销状态 -> 验证 HMAC -> 校验调用者 SID、会话与范围 -> 校验可选熵 -> ChaCha20 解密。调用者策略和熵检查均发生在解密之前。

`Machine` 范围必须同时满足：

1. `CallerContext::is_service_account == true`
2. `CallerContext::machine_scope_explicitly_declared == true`

普通用户即使把请求范围改成 `Machine` 也会被拒绝。服务账户可以为用户封装（seal）材料，但解密仍要求信封所有者的 SID 和登录会话，因此服务账户不能直接读取该用户的材料。

`CallerContext` 中的 SID、登录会话、服务账户标志和机器范围声明必须由凭据服务从已验证的登录令牌/服务配置中填充，不能直接取自客户端请求字段。

## 接口

```cpp
#include "netsec.hpp"

netsec::ProtectionContext context;  // 或导入已有密钥

netsec::CallerContext alice{
    .subject = {"S-1-5-21-...-1001", "alice-logon-session"},
    .requested_scope = netsec::ProtectionScope::User,
};

auto envelope = context.protect(alice, "password-material", "credential1");

// 使用同一 SID、登录会话、范围和熵才能取回明文。
auto plaintext = context.unprotect(alice, envelope, "credential1");
```

`Entropy` 参数也接受 `std::span<const std::uint8_t>`、字节容器、`std::string`
和字符数组；组件只保存其 SHA-256 承诺。

服务账户使用机器范围：

```cpp
netsec::CallerContext service{
    .subject = {"S-1-5-18", "service-session"},
    .requested_scope = netsec::ProtectionScope::Machine,
    .is_service_account = true,
    .machine_scope_explicitly_declared = true,
};
auto machine_envelope = context.protect(service, "machine-secret");
```

密钥代次与撤销：

```cpp
std::uint32_t next = context.rotate();          // 新信封使用新代次
context.revoke_generation(1);                   // 旧代次密文立即失效
auto info = context.inspect(envelope);
context.revoke_envelope(info.revocation_id);    // 单个信封撤销
```

导入已有密钥（例如来自操作系统保护的密钥库）：

```cpp
netsec::Key master_key = /* 32 bytes */;
netsec::ProtectionContext context(master_key, /*generation=*/7, true);
```

库不导出密钥。撤销状态和密钥环由 `ProtectionContext` 持有；服务重启时应从自己的受保护存储中恢复密钥与撤销记录。

## 信封格式

固定头采用大端序，包含 magic、版本、范围、标志、密钥代次、撤销 ID、salt、nonce、SID、登录会话、熵承诺与密文长度。密文使用 ChaCha20，认证使用独立派生密钥的 HMAC-SHA256。密钥通过 HKDF-Extract/Expand 从主密钥、salt 和完整认证头派生。

`inspect()` 只解析公开头，不验证密钥或 MAC，适合在撤销流程中读取 `RevocationId`；在 `unprotect()` 成功前不应信任其中的身份字段。

## 构建与测试

```bash
cmake -S . -B build -DCMAKE_CXX_STANDARD=20
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：

- 跨用户解密与同 SID 跨登录会话解密
- 用户/机器范围提升与降级
- 机器范围的服务账户与显式声明策略
- 可选熵替换/缺失
- 密钥轮换、旧代次撤销与新代次解密
- 信封撤销、版本篡改、头部/密文/MAC 篡改
- 导入同一密钥的合法解密，以及错误密钥拒绝
