# KTLS 换钥边界协调组件

供 C++20 TLS 终止器调用的可导入换钥协调器。组件把用户态
`traffic secret` 代次、KTLS 内核记录序号和未决记录绑定到同一个
`(connection, generation, sequence)`。发送和接收方向完全独立。

## 驱动接口

实现 `netsec::Driver`：

- `read()`：回读内核当前 traffic generation 和下一 record sequence。
- `barrier()`：完成栅栏，返回成功时该方向所有旧记录已离开内核/DMA。
- `install()`：原子安装 key/IV、traffic generation 和目标 record sequence。
  不得暴露中间态。
- 返回 `pending` 时，通过 `on_barrier_complete()` /
  `on_install_complete()` 回调协调器。

`abort()` 用于失败关闭。所有回调必须带原始 `ConnectionId`，只接受与当前
连接代次完全匹配的 token。

## 使用流程

```cpp
netsec::KeyUpdateCoordinator coordinator(driver);
auto conn = netsec::ConnectionId{fd, connection_generation};
coordinator.open(conn, initial_send, initial_receive);

// 发送记录
auto out = coordinator.allocate_send(conn);
encrypt_and_send(out->traffic_generation, out->sequence, ciphertext);
coordinator.complete_record(*out);   // 内核/DMA 完成

// 接收记录；第三个参数是 KTLS 实际使用的密钥代次
auto in = coordinator.begin_receive(conn, wire_sequence, observed_generation);
kernel_decrypt(in->traffic_generation, in->sequence, ciphertext);
coordinator.complete_record(*in);

// KeyUpdate 记录本身必须先 allocate_send()/begin_receive() 登记。
coordinator.begin_key_update(conn, netsec::Direction::send, next_secret);
```

`begin_key_update()` 立即冻结对应方向。新记录在冻结期间返回
`ErrorCode::frozen`。提交顺序固定为：

1. 等待该方向所有旧代未决记录完成；
2. 调用内核完成栅栏；
3. 回读旧 traffic generation 与精确下一序号，必须一致；
4. 原子安装新 key/IV 和规范起始序号；
5. 再次回读新 generation/序号并校验；
6. 解冻并从新代次继续。

TLS 1.3 默认在新 key 安装时把 record sequence 重置为 0。其他记录层可用
`Options::update_sequence_policy = continue_sequence`，或在
`TrafficSecret::first_record_sequence` 中显式指定起始序号。

## 安全边界

- traffic secret generation 只能按 `current + 1` 前进；重复或跳代的
  KeyUpdate 直接失败关闭。
- 每张 `RecordTicket` 绑定连接 fd、连接代次、traffic generation、精确序号；
  迟到完成、重复完成、跨连接回调不会改变当前代次。
- 同一 fd 重开要求连接代次严格递增；旧 ticket 和旧异步 token 均被拒绝。
- 接收时提供 `observed_traffic_generation`，可在序号因换钥重置后仍拒绝
  旧代密文冒充新代记录。
- 接收序号必须精确连续；回退视为 replay，跳跃视为 gap，均失败关闭。
- 序号耗尽后下一次分配立即终止；不会发生 64 位回绕。
- 驱动回读不一致、栅栏失败、安装失败或安装后回读不一致都会调用
  `abort()` 并进入终态，所有后续操作返回 `ErrorCode::failed`。

协调器本身是事件循环驱动的；调用方需串行化 API 调用，异步结果通过上述
完成回调重新进入。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build -j
ctest --test-dir build --output-on-failure
```

`tests/netsec_tests.cpp` 使用可暂停驱动覆盖：边界记录、旧完成迟到、
半双工与双向同时更新、重复 KeyUpdate、异步安装/栅栏失败、回读不一致、
fd 复用、序号耗尽、合法连续记录，以及旧代密文重放。测试同时用
`(traffic_generation, sequence)` 账本核对每份密文只属于一个密钥代次。
