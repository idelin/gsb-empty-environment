# netsec：远程桌面认证协商组件

面向 C++20 远程桌面服务端的可导入认证协商库。它把认证类型协商、策略判定、证书固定、协议版本校验、CredSSP 会话票据和口令封装放在同一个可验证的绑定中，默认拒绝降级，并确保口令只通过加密且带完整性校验的信封传输。

## 安全模型

- `CredSsp` 是网络级认证（NLA）方法；`Tls` 与 `RdpStandard` 属于弱认证方法。
- `ServerPolicy::require_nla = true` 时，客户端未提供 `CredSsp` 直接返回 `RejectedNlaRequired`。
- `ServerPolicy::reject_downgrade = true` 时，即使策略允许弱方法，只要客户端声明支持 `CredSsp`，服务端也不会选择更弱的方法。
- 协商结果 `NegotiationBinding` 通过 MAC 绑定以下内容：
  - 服务端身份与完整策略摘要（`policy_digest`，包含认证方法、版本范围、证书固定值、隔离网段和票据/绑定时效）；
  - 实际出示的叶证书 SHA-256 指纹；
  - 请求和协商后的协议版本；
  - 客户端实际提供的认证方法集合与双方 nonce；
  - 会话 ID、CredSSP 票据、签发/过期时间。
- MAC 密钥由传输层导出密钥（TLS exporter 或等价的本连接密钥材料）经 HKDF-SHA256 派生。客户端用自己一侧的 exporter 调用 `verify_binding`，因此中间人替换证书、改写方法集合或改写版本都会导致校验失败。
- 客户端还会校验 `binding.offered_methods` 是否等于自己实际发送的方法集合。即使服务端策略允许隔离网络中的弱认证，MITM 在到达服务端前剥掉 `CredSsp` 也会被客户端识别为 `RejectedDowngrade`。
- 弱认证必须同时满足：
  - `allow_weak = true`；
  - 认证方法出现在 `allowed_methods`；
  - 客户端来源地址命中一个 `isolated = true` 的 CIDR；
  - 客户端自己未要求 NLA。
  弱认证成功和失败都会写入审计事件（`WeakAuthAccepted` / `WeakAuthRejected`）。
- `CredSsp` 成功协商后签发票据；票据 MAC 覆盖会话 ID，服务端只允许票据用于其绑定的会话 ID 和证书。重复使用客户端 nonce 会被拒绝。
- 口令从不进入明文通道：`CredentialPayload` 由 HKDF-SHA256 派生密钥，ChaCha20 加密，HMAC-SHA256 做 encrypt-then-MAC，并对会话 ID、双方 nonce 和序号做 AAD 绑定。`serialize_binding`、`serialize_credentials` 和审计日志都不包含用户名或口令。

## 服务端接入

```cpp
#include "netsec.hpp"

netsec::ServerPolicy policy;
policy.server_id = "rdp.corp.example";
policy.policy_revision = "2026-11";
policy.require_nla = true;
policy.reject_downgrade = true;
policy.allowed_methods = {
    netsec::AuthMethod::CredSsp,
    netsec::AuthMethod::Tls,
    netsec::AuthMethod::RdpStandard,
};
policy.pinned_certificate_sha256 = {
    netsec::fingerprint_certificate(leaf_certificate_der),
};
policy.minimum_version = {10, 6};
policy.maximum_version = {10, 12};
policy.binding_lifetime = std::chrono::seconds(60);
policy.ticket_lifetime = std::chrono::seconds(600);

netsec::Authenticator authenticator(policy);

netsec::TransportContext transport{
    peer_address,             // 由 socket 层提供，不使用客户端自报地址
    leaf_certificate_der,     // TLS 实际出示的叶证书
    tls_exporter_bytes,       // 每个连接唯一，至少 32 字节
};

netsec::NegotiationResult negotiated =
    authenticator.negotiate(client_hello, transport);
if (!negotiated) {
  close_connection(negotiated.status);
  return;
}

netsec::CredentialOpenResult opened = authenticator.open_credentials(
    *negotiated.binding, sealed_credentials, tls_exporter_bytes);
if (!opened) {
  close_connection(opened.status);
  return;
}
```

客户端侧在发送口令前必须执行：

```cpp
netsec::ClientExpectations expected{
    .server_id = "rdp.corp.example",
    .policy_digest = pinned_policy_digest,
    .observed_certificate_sha256 =
        netsec::fingerprint_certificate(observed_leaf_der),
    .offered_methods = offered_methods,
    .offered_version = {10, 12},
    .client_nonce = client_nonce,
    .minimum_method = netsec::AuthMethod::CredSsp,
    .requires_nla = true,
    .transport_exporter = tls_exporter_bytes,
};
if (netsec::verify_binding(binding, expected) != netsec::Status::Ok) {
  abort_connection();
}

auto sealed = netsec::seal_credentials(
    binding, tls_exporter_bytes, credentials, sequence_number);
if (!sealed) {
  abort_connection();
} else {
  send(netsec::serialize_credentials(*sealed.envelope));
}
```

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build --output-on-failure
```

库目标为 `netsec::netsec`，也可安装后供其他 CMake 工程导入：

```sh
cmake --install build --prefix /opt/netsec
```

```cmake
find_package(netsec CONFIG REQUIRED)
target_link_libraries(rdp_server PRIVATE netsec::netsec)
```

要求 C++20。`netsec_tests` 覆盖以下场景：

| 场景 | 期望结果 |
| --- | --- |
| NLA 策略下只提供 `Tls`/`RdpStandard` | `RejectedNlaRequired` |
| 客户端支持 CredSSP 但能力被剥离 | 服务端拒绝，或客户端 `RejectedDowngrade` |
| 方法、方法集合、版本或 MAC 被篡改 | `RejectedBinding` / 对应字段校验失败 |
| 出示替换后的证书 | `RejectedCertificate` |
| 协议版本低于下限或高于上限 | `RejectedProtocolVersion` |
| 非隔离网段使用弱认证 | `RejectedWeakNetwork` |
| 未开启 `allow_weak` 使用弱认证 | `RejectedWeakAuthDisabled` |
| 显式隔离策略下使用弱认证 | 接受并记录 `WeakAuthAccepted` |
| 票据用于其他会话或错误证书 | 拒绝 |
| 口令信封被篡改或重放 | `RejectedCredentials` / `RejectedReplay` |
| 捕获绑定和信封的线格式字节 | 不含口令、用户名 |

当前测试执行结果为 `130 checks, 0 failure(s)`，并通过 ASan/UBSan；`netsec_crypto_vectors` 另外用 SHA-256/HMAC/HKDF/ChaCha20 的 RFC 向量校验了密码学原语。

## 生产注意事项

- `transport_exporter` 必须来自已完成证书校验的 TLS 连接（例如 RFC 5705 exporter），或来自等价的本连接密钥材料；不能使用固定值。TLS 连接本身还需要正常的证书链/主机名校验。
- `policy_digest` 应通过带外的配置分发或管理通道固定到客户端；客户端只接受摘要匹配的绑定。
- 弱认证虽然使用 `Tls`/`RdpStandard` 方法名，但本组件仍要求通过 `seal_credentials` 提交口令，不接受任何裸口令字段。隔离网段的判定使用服务端观测到的 IPv4 地址，不支持 IPv6 CIDR；IPv6 对端会按“非隔离”处理，弱认证因此被拒绝。
- 内置 SHA-256/HMAC/HKDF/ChaCha20 实现用于保持组件零外部依赖。生产集成建议替换为经过审计的密码学提供者（OpenSSL、BoringSSL、CommonCrypto 等），并保持相同的 transcript/信封格式。
- `open_credentials` 会把解密后的明文临时缓冲区清零，但返回给调用方的 `CredentialPayload` 仍由调用方负责尽快使用并清零。审计日志有 8192 条上限；生产环境应通过 `drain_audit_log` 持续落盘。
- 本组件负责认证协商和口令信封，不实现完整的 SPNEGO/NTLM CredSSP 状态机；服务端应在 TLS 层完成证书验证，并把协商结果接入实际的身份校验模块。
