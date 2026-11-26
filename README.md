# DTLS 1.3 记录层安全组件（RFC 9147）

面向 C++17 UDP 协议宿主的可嵌入库，零外部依赖，仅使用标准库。覆盖
DTLS 1.3 记录层防重放、握手分片安全重组与无状态 Cookie DoS 防御。
底层 AEAD、nonce 掩码与 Cookie 签名通过抽象接口注入，并附带确定性的
SHA-256/HMAC 参考实现用于测试与无 TLS 栈环境下的集成验证。

> 参考 AEAD（HMAC + 派生密钥流）仅用于确定性测试，不替代生产环境的
> AES-GCM / ChaCha20-Poly1305。宿主实现 `IAead` / `INonceMasker` 即可
> 接入真实密码套件，记录层其余安全逻辑无需改动。

## 组件

- `include/dtls13/record.hpp` / `src/record.cpp`
  - 统一头（Unified Header，`001 B E S L R`）解码，固定前缀与保留位校验。
  - 8/16 位截断序号按 RFC 9147 §4.5.1 重构为 48 位完整序号，模 2^48
    处理回绕，半点（half-window）按 RFC 伪代码向更大序号裁决。
  - 每 `epoch` 独立的 64 位滑动位图防重放窗口；`inspect()` 为 const，
    只有 `commit()` 能推进窗口。
  - epoch 省略（E=0）时沿用当前 epoch；支持单数据报多记录。
- `include/dtls13/record_layer.hpp` / `src/record_layer.cpp`
  - 编排解析 → 序号重构 → 防重放 → AEAD（AAD 为序列化记录头）→ 窗口提交。
  - AEAD 认证通过前绝不推进窗口；认证失败的记录对窗口零影响。
  - 旧 epoch 明文记录在分配任何状态前即按 `StaleEpoch` 丢弃。
  - 内层 content type 取解密明文末尾字节（RFC 8446）。
- `include/dtls13/reassembly.hpp` / `src/reassembly.cpp`
  - 按 `message_seq` 维护区间合并表与逐字节 presence 位图。
  - 重叠分片逐字节比对：相同才去重合并，任意字节不一致即
    `OverlapConflict` 并投毒该槽位，后续分片一律拒绝，防止“补救式”攻击。
  - `offset + length > total_length`、`msg_type`/`total_length` 漂移分别
    触发 `FragmentOverflow` / `InconsistentHeader`。
  - 单消息字节、并发槽位、总缓存字节三重预算，全部在分配前强制执行。
- `include/dtls13/cookie.hpp` / `src/cookie.cpp` + `crypto.*`
  - 无状态 HMAC Cookie：`version|issue_time|context_hash|hmac`，
    常量时间比较，可注入时钟确定性测试过期与时钟回拨。
- `include/dtls13/crypto.hpp` / `src/crypto.cpp`
  - FIPS 180-4 SHA-256、RFC 2104 HMAC、TLS 1.3 IV XOR nonce 掩码、
    确定性参考 AEAD。

## 安全契约

- 序号空间严格按 epoch 隔离，永不跨 epoch 共用窗口或高水位。
- 防重放窗口的唯一推进点是 AEAD 认证成功之后（明文记录为结构校验之后）。
- 重组槽位一旦检测到注入即永久投毒，直到宿主显式 `discard()`。
- 所有长度字段在解引用前做边界检查， hostile 数据报不会触发越界访问
  （已通过 AddressSanitizer + UndefinedBehaviorSanitizer 验证）。

## 构建与测试

严格编译选项：`-std=c++17 -Wall -Wextra -Wconversion -Werror -Wpedantic`。

```sh
make test           # 直接用 clang++/g++ 构建并运行全部确定性测试
cmake -S . -B build # 或使用 CMake（生成静态库 + ctest 目标）
cmake --build build
ctest --test-dir build --output-on-failure
```

当前 48 个测试全部通过，覆盖：

- SHA-256/HMAC 已知答案、AEAD 密文与 AAD 篡改检测、确定性输出；
- 统一头 8/16 位序号解码、保留位/截断数据报拒绝、epoch 省略；
- 序号重构回绕边界与 48 位回绕、滑动窗口重复/过旧/回绕判定；
- AEAD 失败不推进窗口、相同截断序号在多 epoch 下独立隔离；
- 重叠字节篡改投毒、越界分片、元数据漂移、内存预算、乱序重组完成；
- Cookie 身份绑定、过期、篡改、错误密钥与超长输入拒绝。

## 集成示例

```cpp
#include "dtls13/dtls13.hpp"
using namespace dtls13;

RecordLayer layer;
layer.installEpochKeys(
    epoch,
    std::make_unique<YourProductionAead>(key),       // 实现 IAead
    std::make_unique<XorNonceMasker>(fixed_iv));     // 或自定义 INonceMasker

IngestResult result = layer.ingest(ByteView(udp_payload, udp_len));
if (result.code != ErrorCode::Ok) { /* 记录告警并丢弃数据报 */ }

HandshakeReassembler reassembler;  // 默认 256 KiB/消息、16 槽、1 MiB 总量
for (const auto& record : result.records) {
    auto added = reassembler.addRecordPayload(ByteView(record.payload));
    if (added.code == ErrorCode::OverlapConflict ||
        added.code == ErrorCode::FragmentOverflow) {
        // 触发 fatal alert，停止该握手流
    }
    if (added.completed) {
        // added.message.body 为完整握手消息，可送入转录哈希
    }
}
```
