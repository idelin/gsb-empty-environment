# FlexRay 静态时隙所有权

`netsec::SlotOwnershipGuard`（见 `slot_ownership.hpp`，C++17 头文件库）供总线
监视回调调用，按静态时隙表裁决每帧的放行/丢弃，并维护违规、冲突与隔离状态。

## 构造输入

- 静态时隙表 `SlotConfig{ slot, owner, cycleRepetition, baseCycle }`：时隙号
  （等于合法帧 ID）、所属节点、cycle repetition 与 base cycle。
- 端口绑定表 `(PortId, NodeId)`：调用方为每帧附上判定出的发送端口，守卫据此
  解析发送节点。
- 登记的同步节点集合。

## 裁决规则（`inspect(header, senderPort, currentCycle)`）

1. 帧头合法性：帧 ID ∈ [1, 2047]、payload length ∈ [1, 254]、cycle count ≤ 63，
   否则以 `MalformedHeader` 丢弃；端口未绑定以 `UnknownPort` 丢弃。
2. 发送节点已被隔离则直接 `Quarantined` 丢弃，不再累计违规。
3. 同步指示位置位时：发送者不在同步节点集合 -> `SyncNotRegistered`；帧
   cycle count ≠ 全局周期计数 -> `SyncCycleMismatch`。两者各计一次违规。
4. 帧 ID 未登记时隙 -> `UnknownSlot` 丢弃。
5. 同周期同时隙的第二帧（不区分发送者）-> `Conflict`，两帧都丢弃，冲突计入
   该时隙所有者（`conflictCount`，与违规计数分开）；先放行的帧序列号通过
   `endCycle()` / `drainRevokedSequences()` 撤销，供监视回调清理。
6. 发送者不是该时隙所有者 -> `NonOwnerSender`，计一次违规（即使该时隙本周期
   处于非活动相位）。
7. 所有者在非活动相位发送（`c mod repetition != base`）-> `WrongCyclePhase`
   丢弃，不计违规。
8. 同一节点累计 3 次违规（`kViolationQuarantineThreshold`）后，其后续全部帧
   被隔离，直到重新构造守卫。因所有者/相位被丢弃的帧同样占用其时隙，因此后续
   同槽帧仍按冲突处理。

## 用法示例

```cpp
#include "slot_ownership.hpp"

netsec::SlotOwnershipGuard guard(
    {{10, "ECU_A", 1, 0}, {20, "ECU_B", 2, 0}},  // 时隙表
    {{1, "ECU_A"}, {2, "ECU_B"}},                // 端口 -> 节点
    {"ECU_A"});                                  // 同步节点

netsec::FrameHeader h{/*frameId*/10, /*len*/8, /*cycle*/0, /*sync*/false};
netsec::InspectionResult r = guard.inspect(h, /*senderPort*/1, /*globalCycle*/0);
if (r.action == netsec::Action::Accept) {
    forwardFrame(r.sequence);
}
for (uint64_t revoked : guard.endCycle()) {  // 周期结束处理冲突撤销
    purgeFrame(revoked);
}
```

## 测试

`test_slot_ownership.cpp` 覆盖借用他人时隙、repetition 错相位、同时隙双帧冲突、
非同步节点置同步位（含 cycle count 不匹配）、隔离阈值，以及非法帧头、未知
端口/时隙、64 周期回绕：

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```
