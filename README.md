# TEID 隧道映射组件

`netsec` 是一个 C++20 用户面网关隧道映射表。它把上下行 TEID、对端地址、用户、
承载和控制面会话绑定在一起，只允许“方向 + TEID + 地址 + 活跃会话”全部匹配的报文
进入对应承载。

## 安全语义

- 上行报文校验 `TEID + 源地址`，下行报文校验 `TEID + 目的地址`，两个方向使用独立索引。
- TEID 为 `0`、未分配 TEID、会话已释放的 TEID 均丢弃。
- 同一方向重复分配 TEID 时注册失败，该 TEID 进入冲突隔离状态；在其原会话释放前，
  所有携带该 TEID 的报文都丢弃，避免歧义转发。
- 会话释放会同时移除上下行映射。`SessionHandle` 带代次，旧句柄无法删除后来复用
  相同 TEID 或控制面会话 ID 的新会话。
- 控制面会话 ID、用户 ID、承载 ID 和地址发生变化时不会复用已有绑定。

## 使用

```cpp
#include "netsec.hpp"

netsec::TunnelMap tunnels;

netsec::SessionConfig config;
config.control_session_id = 10;
config.user_id = 20;
config.bearer_id = 30;
config.uplink_teid = 0x100;
config.uplink_address = "192.0.2.10";   // 上行报文源地址
config.downlink_teid = 0x200;
config.downlink_address = "198.51.100.20";  // 下行报文目的地址

const auto session = tunnels.create_session(config);
if (!session) {
    // 注册失败：参数非法、控制面会话重复或 TEID 冲突。
}

netsec::Packet packet;
packet.direction = netsec::Direction::Uplink;
packet.teid = 0x100;
packet.source_address = "192.0.2.10";

const auto result = tunnels.forward(packet);
if (result) {
    // result->user_id / bearer_id / control_session_id 是唯一目标。
    // result.reason 可区分 unknown-teid、conflict-teid、address-mismatch 等丢弃原因。
}

tunnels.release_session(*session);  // 释放后代次句柄失效，TEID 可安全重新分配。
```

`dispatch(packet, sink)` 仅在验证成功时调用一次 sink；验证失败时返回 `false` 且不调用。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖合法上下行、TEID 猜测、跨会话地址注入、释放后复用、冲突隔离以及
“报文只进入一个所属承载”的场景。
