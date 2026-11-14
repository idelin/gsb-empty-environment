# SSH 证书会话约束组件（C++20）

堡垒机用 SSH CA 签发带 `force-command` 的证书，但约束若只在首次认证时
生效，连接复用（multiplexing）或重新协商（rekey）后就会被丢弃，攻击者
可借新通道执行任意命令。本组件把证书约束绑定到整条会话生命周期：

- 解析 SSH 证书（Ed25519 v01 wire 格式）的 critical options 与
  extensions；**未知 critical 选项一律拒绝**，未知 extension 仅记录不拒绝。
- `force-command`、`source-address`、`permit-open`、`permit-listen` 及
  `permit-*` 转发授权在**每次通道打开和每次 rekey 时重新评估**。
- CA、主体、有效期与撤销列表（revocation）在带代次（generation）的
  `TrustStore` 上核对；主机密钥轮换不影响用户授权。

## 集成方式

```cmake
add_subdirectory(path/to/netsec)
target_link_libraries(your_ssh_server PRIVATE netsec)
```

```cpp
#include "netsec.hpp"

auto store = std::make_shared<netsec::TrustStore>();
store->addTrustedCa({caKeyBlob, /*allowedPrincipals=*/{}});

// 生产环境注入真实的 Ed25519 验签实现；默认仅做结构校验。
netsec::ConstraintEngine engine(store, &yourCryptoVerifier);

auto auth = engine.authenticate(certBlob, "alice", clientIp, nowSeconds);
if (!auth.decision.allowed) { /* 拒绝连接 */ }
auto session = auth.session;

// 每个新通道（含复用连接上的通道）：
netsec::ChannelRequest ch{netsec::ChannelType::Session};
ch.requestedCommand = clientRequestedCommand;
auto decision = session->openChannel(ch, nowSeconds);
// decision.allowed 为真时执行 decision.effectiveCommand，
// 客户端原始命令仅作为 originalCommand 保留（如 SSH_ORIGINAL_COMMAND）。

// 重新协商：主机密钥变化被显式忽略，约束按最新代次重新核对。
auto rekey = session->onRekey(newHostKeyBlob, nowSeconds);
if (!rekey.allowed) { /* 拆除整条传输 */ }
```

## 安全语义

- **强制命令不可绕过**：无论会话通道请求的是 shell、exec 还是带命令替换
  （`;`、`$()`、反引号、管道）的字符串，实际执行的始终是
  `force-command`；请求内容不会被拼接进执行命令。
- **转发默认拒绝**：`direct-tcpip` / `tcpip-forward` 需
  `permit-port-forwarding` 且目标命中 `permit-open` / `permit-listen`
  白名单；白名单存在时未命中即拒绝。agent、X11、PTY 同理按授权放行。
- **代次化撤销**：`revoke()` / CA 增删都会提升 `generation`。已建立的会话
  在下一次通道打开或 rekey 时检测到证书被撤销 / CA 被移除，会话立即失效，
  后续所有通道请求都被拒绝。
- **失败关闭**：畸形证书、未知 critical 选项、空主体列表、无法解析的来源
  地址都在建立会话前拒绝。
- **验签边界**：组件不含密码学代码，通过 `ICertificateVerifier` 接入服务端
  的 Ed25519 实现；未注入时使用 `StructuralSignatureVerifier`（仅校验签名
  blob 结构），适用于传输层已独立证明证书真实性的场景。

## 测试

`tests/test_constraints.cpp` 覆盖命令替换、端口转发白名单、多路复用、
rekey、未知 critical 选项、合法受限命令、撤销后新连接与存量连接、
CA 轮换、来源地址 CIDR（IPv4/IPv6）、有效期与验签失败等场景：

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```
