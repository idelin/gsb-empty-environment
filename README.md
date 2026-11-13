# FlexRay 静态时隙所有权包（`netsec`）

面向 C++ 网关总线监视回调的可导入头文件 + 静态库，用于判定 FlexRay 静态段每帧
是否有资格在它出现的时隙上发送，并对借用时隙、错相位、同时隙冲突、同步位造假和
重复违规做记账与隔离。

## 判定模型

每个通信周期按 `begin_cycle(c)` → 若干 `inspect(frame)` → `end_cycle()` 驱动。
`inspect` 在周期内给出暂定结论，`end_cycle` 解析同周期同时隙冲突并定案，之后用
`verdict(id)` 取最终裁决。之所以延迟到周期末定案，是因为“同一时隙收到两帧则两帧
都丢弃”只有在看到整周期流量后才能判定。

一帧依次经过以下检查，命中即丢弃（取首个原因）：

1. `InvalidHeader`：帧 ID 超 11 位、cycle count 超 6 位或 payload length 越界。
2. `Isolated`：发送节点已达到隔离阈值，其后续全部帧直接丢弃。
3. 同步策略（仅对置同步指示位的帧）：
   - `SyncNotRegistered`：发送者不在登记的同步节点集合内；
   - `SyncCycleMismatch`：帧头 cycle count 不等于当前全局周期。
4. `UnknownSlot`：帧 ID 在静态时隙表中无对应时隙（静态段帧 ID 即时隙号）。
5. `WrongPhase`：`c mod repetition != base_cycle`，该时隙本周期未激活。
6. `NotOwner`：发送者不是该时隙登记所有者，计一次违规（驱动隔离）。
7. 暂定放行；`end_cycle` 时若该时隙本周期出现 ≥2 帧，全部改判 `Conflict`。

记账口径（与题目逐条对应）：

- 放行条件：相位合法、帧 ID 等于时隙号、发送者为所有者，三者同时满足。
- 冲突：同周期同时隙的每一帧都丢弃，冲突事件计入该时隙所有者节点
  （`NodeCounters::conflicts_charged`）与时隙（`SlotCounters::conflict_events`）。
- 违规：非所有者发送计一次 `violations`；累计达到 3 次（构造时可改阈值）后该节点
  被隔离，之后所有帧以 `Isolated` 丢弃，且不再累积新的违规次数。
- 同步位造假与同步周期不符计入独立的 `sync_offenses`，不混入 3 次违规计数。
- 错相位帧丢弃但不计违规（时隙未激活，属时序错误而非占用他人发送权）。

## 用法

```cpp
#include "netsec.hpp"

netsec::SlotOwnershipMonitor monitor(
    {netsec::SlotEntry{1, /*owner=*/10, /*repetition=*/1, /*base=*/0},
     netsec::SlotEntry{2, /*owner=*/20, /*repetition=*/2, /*base=*/0}},
    {/*sync nodes*/ 10, 20});

monitor.begin_cycle(cycle);
auto id = monitor.inspect(frame);  // frame.sender 为总线监视判定的物理发送端口
monitor.end_cycle();
netsec::Verdict v = monitor.verdict(id);
if (v.decision == netsec::Decision::Dropped) { /* 拒绝转发 */ }
```

完整可运行示例见 `examples/bus_monitor_example.cpp`。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

若本机 `ar` 走 xcrun 垫片失败（无 Xcode 会话），可显式指定命令行工具链的归档器：

```sh
cmake -S . -B build \
  -DCMAKE_AR=/Library/Developer/CommandLineTools/usr/bin/ar \
  -DCMAKE_RANLIB=/Library/Developer/CommandLineTools/usr/bin/ranlib
```

## 验证覆盖

`tests/test_slot_ownership.cpp` 覆盖题目要求的五个场景：

- 借用他人时隙：非所有者帧 `NotOwner` 丢弃并计违规，所有者帧放行；
- repetition 错相位：奇数周期丢弃 `WrongPhase`，激活周期放行；
- 同时隙双帧：两帧均 `Conflict` 丢弃并计入时隙所有者；
- 非同步节点置同步位 / 同步周期不符：`SyncNotRegistered`、`SyncCycleMismatch`；
- 隔离阈值：3 次非所有者违规后隔离，第 4 帧（含自身时隙合法帧）以 `Isolated` 丢弃。

目标为 C++17，库本身非线程安全，设计为由单一总线监视回调线程驱动。
