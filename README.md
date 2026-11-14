# 生物识别模板完整性组件

供 C++20 生物识别服务导入的模板封装与校验库。它解决的攻击是：攻击者替换
模板存储中的记录，使受害者账户在比对时读取到攻击者注册的模板，随后用自己的
指纹通过验证。

组件把模板身份和策略绑定到认证加密信封中，校验通过后才解密到锁定内存。身份
比对与活体判定采用“双门限”联合决策，高指纹相似度不能覆盖活体失败。

## 安全属性

- **身份绑定**：用户、设备、注册会话、算法版本、模板代际、签发时间、密钥 ID
  和随机 nonce 均进入认证数据；任何字段被替换都会导致 MAC 校验失败。
- **先验证后解密**：使用 HMAC-SHA256 做 encrypt-then-MAC。只有常量时间比较
  标签成功后才会解密，失败结果不包含明文或可信元数据。
- **密钥绑定**：每个信封绑定密钥 ID；加密与 MAC 密钥由调用方提供的 32 字节
  主密钥通过域分离派生。密钥 ID 相同但密钥错误同样会被 MAC 拒绝。
- **受保护明文**：解密直接写入 `mmap` 分配的页对齐缓冲区，两侧带不可访问守卫
  页，数据页通过 `mlock` 固定。缓冲区只移动、不复制，析构时覆盖并释放。
- **跨用户/设备/会话拒绝**：期望身份来自可信认证路径，而不是待校验记录中的
  明文元数据。
- **旧模板拒绝**：可信服务可传入每个用户的注册代际高水位
  (`expected_generation`/`minimum_generation`)，以及最大模板年龄和时钟偏移。
- **算法降级拒绝**：算法版本参与 MAC；策略还要求最低算法版本，可额外要求精确
  版本。
- **联合认证**：只有指纹分数达到阈值且活体结果 `passed`、活体分数达到阈值时
  才接受。NaN、无穷值、越界分数和非法策略一律失败关闭。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build --output-on-failure
```

测试覆盖：

- 模板替换和密文/头部篡改；
- 跨用户、跨设备、跨注册会话；
- 错误密钥和错误密钥 ID；
- 旧代际、过期模板、未来时间；
- 算法降级模板与算法字段篡改；
- 合法模板往返、受保护内存状态；
- 指纹阈值和活体结果的联合决策；
- 截断、尾随垃圾等畸形导入。

## 导入方式

作为子目录使用：

```cmake
add_subdirectory(path/to/netsec)
target_link_libraries(biometric_service PRIVATE netsec::netsec)
```

安装后使用：

```cmake
find_package(netsec 1 CONFIG REQUIRED)
target_link_libraries(biometric_service PRIVATE netsec::netsec)
```

## 使用示例

```cpp
#include "netsec.hpp"

namespace bio = netsec::biometric;

// 32 字节密钥应来自 KMS/HSM；这里仅演示构造方式。
auto key = bio::EncryptionKey::from_hex("key-2026-11", key_hex);

bio::TemplateRegistration registration{
    .user_id = "alice",
    .device_id = "phone-7",
    .enrollment_session_id = "enroll-1001",
    .algorithm = bio::Algorithm::fingerprint_v2,
    .generation = 7,
    .issued_at_unix = now,
};

auto sealed = bio::seal_template(template_bytes, registration, key);
if (!sealed) {
    // sealed.status 给出失败原因
}

// 持久化 sealed.envelope.encoded()。读取时先做结构导入。
auto imported = bio::import_template(stored_blob);
if (!imported) {
    return deny(imported.status);
}

// 这些期望值必须来自可信认证路径/服务状态，而不是模板记录本身。
bio::TemplateExpectation expected{
    .user_id = authenticated_user,
    .device_id = device_from_attested_channel,
    .enrollment_session_id = current_enrollment_session,
    .expected_generation = server_side_generation_high_water,
};

bio::ValidationPolicy policy{
    .now_unix = now,
    .maximum_age_seconds = 90 * 24 * 3600,
    .minimum_generation = server_side_generation_high_water,
    .minimum_algorithm = bio::Algorithm::fingerprint_v2,
    .expected_algorithm = std::nullopt,
    .allowed_clock_skew_seconds = 30,
};

auto opened = bio::open_template(imported.envelope, expected, policy, key);
if (!opened) {
    return deny(opened.status);
}

// 模板明文只在 opened.plaintext 的锁定内存中；交给比对引擎后立即离开作用域。
const double match_score = engine.compare(opened.plaintext.bytes(), probe);
auto decision = bio::decide_match(
    match_score,
    bio::LivenessResult{.passed = liveness_passed, .score = liveness_score},
    bio::MatchPolicy{.minimum_match_score = 0.90,
                     .minimum_liveness_score = 0.80});
if (!decision) {
    return deny(decision.reason);
}
```

## 运营要求

- 模板记录和高水位代际不能放在同一可替换存储路径中。代际、阈值、算法最低版本
  和当前时间必须由可信服务配置/KMS 或受保护状态提供。
- 每次注册或重注册应推进用户代际；删除或撤销模板时应同时推进高水位。
- 每个主密钥只通过密钥 ID 引用。轮换时应保留旧密钥用于受控迁移，但策略必须
  拒绝已撤销模板。
- nonce 由操作系统 CSPRNG 生成。不要复用密钥内容搭配可预测 nonce。
- 生产接入前仍应对密钥管理、进程权限、内存转储策略和密码实现进行独立安全
  评审；本组件不替代 HSM、操作系统加固或设备证明链。
