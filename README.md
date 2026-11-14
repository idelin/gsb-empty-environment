# netsec：热迁移安全通道

`netsec` 是供 C++20 虚拟化平台导入的迁移通道组件。它解决明文迁移通道中
目标地址被中间人改写、内存页被窃取或篡改、票据重放等问题。

## 安全属性

- **双向主机认证**：源端验证目标端；目标端验证源端。双方分别使用面向对端的
  32 字节认证密钥，任一方向缺少正确密钥都无法完成握手。
- **通道加密**：握手使用临时 X25519，经 HKDF-SHA256 派生双向会话密钥；
  数据记录使用 ChaCha20-Poly1305，密文和认证标签一起传输。
- **会话绑定**：VM ID、代次、源/目标主机 ID、一次性票据、配置端点和双方
  nonce 全部进入认证 transcript 与密钥派生。
- **目标地址固定**：`MigrationRequest` 不包含目标地址。源端只使用
  `SourceConfig::target_endpoint`；目标端只接受与 `TargetConfig::endpoint`
  完全一致且通过主机认证的会话。传输层报告的任何地址都不具备权威性。
- **一次性票据**：`TicketAuthority` 生成随机票据 ID 和 32 字节票据 secret，
  并绑定源、目标、VM 和代次。目标端在验证源证明和票据证明后原子消费票据；
  并发或后续复用均失败。
- **页校验与完成确认**：每页校验覆盖 VM、代次、页序号、客户机地址和内容；
  整幅内存另有 manifest。目标端只有在全部页校验通过、manifest 一致后才提交，
  并返回加密的完成确认。源端收到匹配确认后才报告成功。
- **失败回滚**：目标端在完成前只写 staging 区域。握手失败、页缺失/重复/
  乱序、校验失败、manifest 不一致或连接中断都会丢弃 staging，不产生
  `committed` 内存镜像。

## 接口

```cpp
#include "netsec.hpp"

using namespace netsec;

auto authority = std::make_shared<TicketAuthority>();
const VmId vm{"vm-42"};
constexpr std::uint64_t generation = 7;

MigrationTicket ticket = authority->issue(
    TicketBinding{{"source-a"}, {"target-b"}, vm, generation});

SourceConfig source_cfg;
source_cfg.source_id = {"source-a"};
source_cfg.target_id = {"target-b"};
source_cfg.target_endpoint = {"10.0.0.8", 9443};
source_cfg.source_auth_key = source_key;
source_cfg.target_auth_key = target_key;

TargetConfig target_cfg;
target_cfg.source_id = {"source-a"};
target_cfg.target_id = {"target-b"};
target_cfg.endpoint = {"10.0.0.8", 9443};
target_cfg.source_auth_key = source_key;
target_cfg.target_auth_key = target_key;

MigrationSource source(source_cfg);
MigrationTarget target(target_cfg, authority);

MigrationRequest request;
request.vm_id = vm;
request.generation = generation;
request.ticket = ticket;
request.pages = pages;

MigrationResult sent = source.migrate(source_channel, request);
TargetMigrationResult received = target.accept(target_channel);
```

`MigrationChannel` 是可靠、面向消息的双工接口：一次 `send()` 对应对端一次
`receive()`。平台可将 TCP、QUIC、共享内存或 RDMA 适配到该接口；超时和连接
生命周期由适配层负责。

## 协议流程

1. 源端发送 `ClientHello`，包含主机/VM/代次/票据/配置端点、临时 X25519
   公钥、源端认证证明和票据证明。
2. 目标端验证配置端点、源端身份、票据绑定与证明，原子消费票据；返回
   带目标端认证证明的 `ServerHello`。
3. 双方派生会话密钥；源端发送加密的 key-confirmation，目标端返回加密
   `Ready`。
4. 源端逐页发送加密页记录；目标端校验序号、地址唯一性、页校验和与大小限制。
5. 源端发送整数页数和 manifest；目标端重新计算 manifest，通过后发送加密
   `Complete`。源端核对 VM、代次、页数和 manifest 后成功返回。

## 构建与验证

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：

- SHA-256、HMAC-SHA256、X25519、ChaCha20-Poly1305 已知向量；
- 合法迁移及页内容、guest 地址、manifest 一致性；
- 传输路由被改写到伪造目标时，源端不发送任何内存页；
- 一次性票据复用被目标端拒绝；
- 中间人改写 `ServerHello` 时源端拒绝，页数为零；
- 中间人篡改已部分暂存后的页记录，AEAD 拒绝且目标端回滚；
- 代次替换在联网前被票据绑定检查拒绝。

部署时应由受信任控制面签发票据，保护并定期轮换两组认证密钥，并使用
平台已有的审计、密钥托管和传输超时策略。
