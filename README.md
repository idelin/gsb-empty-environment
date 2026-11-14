# netsec：媒体网关流身份组件

该组件解决“只看 SSRC 识别流”导致的媒体串流问题。每条流由完整身份绑定：

```
(session_id, ssrc, source.address, source.port, payload_type, key_material)
```

`netsec::StreamRegistry` 是线程安全的 C++20 可链接组件。冲突判定先按会话和来源
定位现有流，再校验载荷类型和密钥材料；仅 SSRC 相同不会被视为同一条流。

## 冲突与隔离规则

| 输入情况 | 结果 |
| --- | --- |
| 完整身份匹配且密钥一致 | `accepted`，进入该流自己的抖动缓冲 |
| 会话、SSRC、来源、载荷类型匹配，但密钥不同 | `key_mismatch`，丢弃并告警 |
| 会话、SSRC、来源匹配，但载荷类型不同 | `payload_type_mismatch`，丢弃并告警 |
| 会话、SSRC 匹配，但来源地址或端口不同 | `ssrc_conflict`，隔离来源并告警 |
| 相同 SSRC，不同会话 | 两条独立流，不冲突 |
| 未注册的流身份 | `unknown_stream`，丢弃 |

冲突来源进入隔离表 `quarantined_sources()`，载荷不会写入任何受害流的抖动缓冲，
也不会推进受害流的序列号、时间戳或统计。告警通过 `set_alert_sink()` 注册的回调
上报，同一来源的同类告警默认去重。回调在内部锁之外执行。

## 每流独立媒体状态

- 序列号按流维护 16 位回绕、已接收集合和重排窗口。精确重复或已接收序号为
  `sequence_replay`；超出 `max_sequence_gap`、或回退超出 `reorder_window` 的
  跳变为 `sequence_jump`，均丢弃。
- 时间戳按流维护 32 位回绕。向前/向后变化超过配置的时间窗为
  `timestamp_jump`，丢弃。序列与时间戳检查是成对提交的：时间戳跳变被丢弃时，
  不会提前推进序列状态。
- 每条流有独立 `std::map` 抖动缓冲，按扩展序列号排序；`drain()` 只返回该流的
  包，`pop_next()` 只弹出该流最旧的包。
- 抖动估算按 RTP 到达间隔公式 `jitter += (|D| - jitter) / 16` 独立计算。
- `stats()` 中的接收、接受、重放、跳变和缓冲计数均按流隔离；冲突包只增加隔离
  统计，不会改写受害流统计。

超过时间窗的长静音、合法重连或经信令确认的 SSRC 重新绑定，不应靠放宽检查来恢复；
调用 `reset_media_state(stream_id)` 显式开启新的媒体状态。

## API 示例

```cpp
#include "netsec.hpp"

netsec::StreamRegistry registry;
registry.set_alert_sink([](const netsec::Alert& alert) {
  // 上报 SIEM / 关闭可疑会话 / 通知运维
});

netsec::StreamBinding binding{
    .session_id = "call-123",
    .ssrc = 0x11223344,
    .source = {"10.0.0.10", 5000},
    .payload_type = 96,
    .key_material = session_key_bytes,
    .clock_rate = 90000,
};
auto registered = registry.register_stream(binding);

netsec::MediaPacket packet{
    .session_id = observed_session_id,  // 来自信令，不是 RTP 头
    .ssrc = observed_ssrc,
    .source = observed_source,          // 网络层实际源地址和端口
    .payload_type = observed_payload_type,
    .sequence_number = observed_sequence,
    .timestamp = observed_timestamp,
    .payload = std::move(decrypted_payload),
    .key_material = selected_session_key,
};

auto result = registry.ingest(packet);
if (result.accepted()) {
  auto ready = registry.drain(result.stream_id);
  // 只把 ready 交给该流对应的解码器
}
```

`key_material` 必须非空。网关应在 DTLS-SRTP/密钥选择完成后传入该会话实际使用的
密钥材料或稳定密钥标识；不要从 SSRC 推导密钥。`session_id` 和实际来源地址/端口
也应由信令与网络层提供，不能信任 RTP 载荷中的自报字段。

## 构建与接入

```bash
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

作为子目录接入：

```cmake
add_subdirectory(netsec)
target_link_libraries(media_gateway PRIVATE netsec::netsec)
```

也可用 `cmake --install` 安装头文件、静态库和 CMake 包配置，然后：

```cmake
find_package(netsec CONFIG REQUIRED)
target_link_libraries(media_gateway PRIVATE netsec::netsec)
```

## 验证覆盖

`tests/stream_identity_test.cpp` 覆盖：

- 同会话、同 SSRC、不同来源的攻击包被隔离，受害流载荷与统计不受影响；
- 已建立流的来源地址/端口变化不会迁移流状态；
- 序列号重放、序列跳变、时间戳跳变均丢弃，且跳变不产生部分提交；
- 同一 SSRC/来源切换到新会话后获得独立序列号、时间戳和抖动缓冲；
- 同一会话内多个合法流、以及跨会话复用相同 SSRC 时载荷不跨流混合；
- 注册冲突、密钥不匹配、每流独立且容量受限的抖动缓冲。

构建配置在 `CMakeLists.txt` 中强制 `CXX_STANDARD 20`。
