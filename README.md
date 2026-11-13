# QUIC 连接 ID 上限与退役放大防护

这是一个 C++17、无第三方依赖的 QUIC 连接 ID 管理库，供帧处理循环在收到
`NEW_CONNECTION_ID` 和 `RETIRE_CONNECTION_ID` 时调用。

## 行为

- `NEW_CONNECTION_ID` 的 CID 长度必须为 1 到 20 字节，否则返回
  `FRAME_ENCODING_ERROR`。
- `retire_prior_to > sequence_number` 返回 `FRAME_ENCODING_ERROR`。
- 同序号活动 CID 的 CID 字节或 stateless reset token 不同时返回
  `PROTOCOL_VIOLATION`；相同则按重复帧忽略。
- 先执行 `retire_prior_to` 要求的退役，再检查活动 CID 数；仍超过
  `active_connection_id_limit` 时返回 `CONNECTION_ID_LIMIT_ERROR`，且本次
  帧不提交状态。
- 原连接使用零长度 CID 时，任何 `NEW_CONNECTION_ID` 都返回
  `PROTOCOL_VIOLATION`。
- `RETIRE_CONNECTION_ID` 的序号必须小于本端下一个待发放序号；重复退役同一
  序号无操作。
- 有新的有效退役时，每个数据包最多生成一个替代 CID。累计替代 CID 数永远
  不超过有效退役数。
- 对端活动 CID 只保留仍有效的项，退役项不会形成无界墓碑集合。

## 接口

主要类型位于 `netsec.hpp`：

- `ConnectionIdManager`：维护对端活动 CID、本端已发放序号和退役状态。
- `NewConnectionIdFrame` / `RetireConnectionIdFrame`：两种输入帧。
- `ConnectionIdFrame`：二者的 `std::variant`，供整包处理。
- `ProcessResult`：错误码、重复标记、退役计数和可选的本端补发 CID。

传入一个数据包中的全部 CID 帧时使用 `process_packet`，这样即使包内有多个退役
帧也只补发一次：

```cpp
netsec::ConnectionIdManager manager(
    netsec::ConnectionIdManagerOptions{
        /*active_connection_id_limit=*/4,
        /*peer_connection_uses_zero_length_cid=*/false,
        /*local_connection_uses_zero_length_cid=*/false,
        /*connection_id_generator=*/{}});

auto result = manager.process_packet(
    {netsec::NewConnectionIdFrame{1, 0, netsec::ConnectionId{1, 2, 3}},
     netsec::RetireConnectionIdFrame{0}});
if (!result.ok()) {
  // result.error is FRAME_ENCODING_ERROR, PROTOCOL_VIOLATION, or
  // CONNECTION_ID_LIMIT_ERROR.
}
if (result.replacement) {
  // Send this as a NEW_CONNECTION_ID frame.
}
```

`process_new_connection_id`、`process_retire_connection_id` 和
`process_frame` 是单包便捷入口。真实栈应通过
`ConnectionIdManagerOptions::connection_id_generator` 注入 CSPRNG 生成器；
默认生成器只用于测试和示例。

流式帧循环可使用 `begin_packet()` 返回的 `PacketProcessor`：

```cpp
auto packet = manager.begin_packet();
for (const auto& frame : parsed_cid_frames) {
  if (packet.process(frame) != netsec::ConnectionIdError::none) {
    break;
  }
}
auto packet_result = packet.finish();  // 此时才执行至多一次补发
```

本端发送普通新 CID 时调用 `issue_connection_id()`，它会分配连续序号。初始本端
CID 固定占用序号 0。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖对端超限、同序号不同内容、`retire_prior_to` 越界、CID 长度、零长度
连接、非法与重复退役、洪泛退役以及每包补发上限。
