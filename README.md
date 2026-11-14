# netsec：会话绑定的媒体流认证组件

`netsec` 是一个可导入的 C++20 库，用于修复“媒体接收端只按序列号排序”导致的
伪造包覆盖与 SSRC 流切换问题。每个媒体包都带一个会话绑定的认证标签；接收端
只有在该包同时通过会话、SSRC、密钥纪元、认证标签与重放策略检查后，才会把媒体
交给播放器或排序器。

## 安全模型

- 认证输入覆盖 SSRC、16 位序列号、32 位时间戳、密钥纪元、会话 ID、负载长度和
  整个负载。任一字段被修改都会导致认证失败。
- 每个纪元的 HMAC 密钥由 256 位 SRTP 主密钥经 HKDF-SHA256 派生，并绑定
  `session_id + SSRC + key_epoch`。另一个会话或 SSRC 即使共享主密钥也不能复用
  认证标签。
- 认证标签为 HMAC-SHA256 的前 16 字节，比较过程为常数时间。
- 认证失败、格式错误、会话/SSRC 不匹配、密钥过期或重放策略拒绝的包直接丢弃，
  不会输出负载，也不会推进重放窗口或密钥纪元状态。
- 重放窗口默认 128 个序号，强制上限 4096。重复包始终丢弃；窗口内乱序可按策略
  接收，窗口外回退拒绝。
- 密钥轮换保留的旧纪元数有上限（最大 16），单纪元可认证包数默认 1,000,000、
  硬上限 16,777,216，接收端可自动接受的前向纪元跳变也有上限。显式轮换可同时
  更换主密钥并保留过渡期旧密钥。

该组件负责真实性和重放防护，不提供媒体保密性，也不是可直接与第三方 SRTP 实现
互操作的线上格式。主密钥应通过 DTLS-SRTP、信令或其它受保护通道分发；本组件只
解决收到媒体包后的来源认证与排序边界问题。

## 快速使用

```cpp
#include "netsec.hpp"

#include <array>
#include <cstdint>
#include <vector>

netsec::Key load_master_key();  // 32 bytes, delivered by secure signalling.

void receiver_example(netsec::ByteSpan wire) {
    netsec::ReceiverConfig config;
    config.session_id = 0x10203040;
    config.ssrc = 0xCAFEBABE;
    config.master_key = load_master_key();
    config.initial_epoch = 7;
    config.replay.window = 128;
    config.replay.max_forward_gap = 1024;
    config.replay.gap = netsec::GapPolicy::reject;

    netsec::Receiver receiver(config);

    // wire is the complete packet returned by Sender::seal().
    netsec::MediaPacket packet;
    const auto result = receiver.receive(wire, packet);
    if (!result) {
        // Drop it: never hand unauthenticated media to the jitter buffer or
        // let it influence the highest sequence number.
        return;
    }
    // packet.payload is safe to decode/deliver.
}

void sender_example(netsec::ByteSpan encoded_frame) {
    netsec::SenderConfig config;
    config.session_id = 0x10203040;
    config.ssrc = 0xCAFEBABE;
    config.master_key = load_master_key();
    config.initial_epoch = 7;

    netsec::Sender sender(config);
    std::vector<std::uint8_t> wire =
        sender.seal(/*sequence=*/1000, /*timestamp=*/90000, encoded_frame);
}
```

链接 CMake 目标：

```cmake
add_subdirectory(path/to/netsec)
target_link_libraries(your_media_receiver PRIVATE netsec::netsec)
```

## 序列与轮换策略

`ReplayPolicy` 控制序号处理：

- `window`：保留的序号数量，`1..4096`。
- `reorder = accept|reject`：窗口内回退是否作为乱序包接收。
- `max_forward_gap`：允许的最大前向序号步长，`1..32767`。
- `gap = reject|resync`：认证通过但步长过大时拒绝，或重同步到新序号。

序号按 16 位回绕扩展为连续计数，因此 `65535 -> 0` 不会被误判为回退。伪造的
高序号包若认证失败，既不会成为“最高序号”，也不会挤掉后续合法包。
重放窗口跨密钥纪元保持连续，因此轮换密钥不会重置序号历史；发送端在轮换前后应
继续递增 RTP 序列号。

`Sender::rotate_key()` 前进一个密钥纪元。接收端默认只自动接受至多
`max_epoch_advance` 个前向纪元；也可用 `Receiver::rotate_to()` 显式推进。
`retained_epochs` 限制轮换后仍可验包的旧纪元数量。需要更换主密钥时，发送端调用
`Sender::set_master_key()`，接收端调用 `Receiver::set_master_key()`；旧主密钥
仅在保留窗口内继续用于旧纪元。

## 构建与验证

需要 C++20 编译器：

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：

- 合法连续 RTP 风格流、负载和时间戳原样恢复。
- 负载、SSRC、序列号、时间戳、认证标签篡改全部拒绝。
- 不同主密钥、不同会话 ID、不同 SSRC 的包注入全部拒绝。
- 高序号伪造包不改变接收状态。
- 重复、窗口内乱序、窗口外回退、超大前向跳变及 resync 策略。
- 16 位序号回绕。
- 同主密钥纪元轮换、旧纪元过渡、过期纪元、新主密钥轮换和单密钥包数上限。
- RFC 4231 HMAC-SHA256 与 SHA-256 标准测试向量。
