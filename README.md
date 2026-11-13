# 蓝牙加密密钥长度协商下限

BR/EDR 密钥长度协商状态机，用于阻止对端通过反复提议更短密钥来降低有效熵。

## 策略

- 本地最低密钥长度为 7 字节，最高为 16 字节。
- 对端提议低于 7 字节时立即拒绝并终止链路，不钳制、不回落到更短值。
- 每次协商最多允许 3 次往返；第 4 次提议直接拒绝并终止链路。
- Secure Connections 模式只接受 16 字节，其他长度拒绝并终止链路。
- 一旦接受某个长度，协商即完成并锁定；后续任何提议都拒绝，已协商长度保持不变。

## 导入

`netsec.hpp` 与 `netsec.cpp` 组成一个 CMake 库目标 `netsec`：

```cpp
#include "netsec.hpp"

netsec::KeyLengthNegotiator negotiator(
    netsec::NegotiationMode::SecureConnections);

auto result = negotiator.propose(peer_key_bytes);
if (!result.accepted()) {
  if (result.link_terminated()) {
    terminate_link();
  }
}
```

`ProposalResult` 提供 `accepted()`、`rejected()`、`link_terminated()`、
`reason`、`round_trip` 和 `negotiated_key_bytes`。状态与结果可通过
`state()`、`round_trips_used()`、`round_trips_remaining()`、
`negotiated_key_bytes()` 和 `link_active()` 查询。

## 验证

`tests/test_netsec.cpp` 覆盖以下场景：

- 对端提议 1 字节：`BelowLocalMinimum`，链路终止。
- 逐步降级：先接受 16 字节，再提议 12、8、1 字节均被拒绝，长度保持 16。
- 第 4 轮：前 3 轮非法提议可重试，第 4 轮以
  `RoundTripLimitExceeded` 拒绝并终止。
- Secure Connections：16 字节接受；15、7 字节以
  `SecureConnectionsRequiresSixteen` 拒绝并终止。
- 边界：BR/EDR 模式接受 7 至 16 字节的所有长度。

构建并运行：

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```
