# SV 订阅包：smpCnt 连续性与来源绑定

面向 C++ 保护装置的采样值（SV）订阅组件。它按 `svID` 维护独立的来源绑定和
采样计数器状态，用于抵御来自被攻破主机的伪造/重放帧，同时向保护逻辑报告
丢失采样点。

## 安全模型

- 每个 `svID` 首次收到并接受的帧确定源 MAC 与 `confRev`。
- 也可以在首帧前调用 `confirm()` 预先固定来源，避免信任首次到达的帧。
- 后续帧的源 MAC 或 `confRev` 与绑定不一致时，该帧被拒绝，并且该 `svID`
  进入锁定状态。
- 锁定后，所有帧都被拒绝，包括重新出现的原始来源帧；只有调用
  `reconfirm()` / `confirm()` 重新确认配置后才恢复接收。
- `smpCnt` 固定为 4000 点/秒的模空间：`0..3999` 循环，正常相邻帧加 1。
- 与上一帧相同的计数值作为重复帧丢弃；早于当前序列的计数值作为重放丢弃。
- 向前的计数跳变大于 1 时，中间缺失的计数值写入
  `SvDecision::invalidSmpCnts`，并累计到 `lostSamples` / `invalidSamples`。

## 基本用法

```cpp
#include "netsec.hpp"

netsec::SvSubscriber subscriber;

// 可选：在首帧前固定某路 SV 的预期来源
subscriber.confirm("MU1/LLN0/DS1", netsec::MacAddress::parse("00:11:22:33:44:55"), 7);

netsec::SvFrame frame;
frame.svID = "MU1/LLN0/DS1";
frame.sourceMac = netsec::MacAddress::parse("00:11:22:33:44:55");
frame.confRev = 7;
frame.smpCnt = 0;

auto decision = subscriber.receive(frame);
if (!decision.accepted()) {
    // decision.status 区分重复、重放、来源变化、confRev 变化或等待重新确认
}

// 来源或 confRev 发生合法变更后，由调用方重新确认配置
subscriber.reconfirm("MU1/LLN0/DS1",
                     netsec::MacAddress::parse("00:11:22:33:44:66"), 8);
```

CMake 集成：

```cmake
add_subdirectory(path/to/netsec)
target_link_libraries(protection_device PRIVATE netsec::netsec)
```

## 返回状态

| `SvStatus` | 含义 |
| --- | --- |
| `Accepted` | 首帧、顺序帧或带缺口的向前跳变；计数器已推进 |
| `Duplicate` | 与当前最后计数值相同，丢弃且不推进 |
| `Replay` | 早于当前序列的旧计数值，丢弃且不推进 |
| `RejectedSourceMac` | 源 MAC 与绑定不一致，进入锁定 |
| `RejectedConfRev` | `confRev` 与绑定不一致，进入锁定 |
| `RejectedSourceMacAndConfRev` | 两者同时不一致，进入锁定 |
| `RejectedAwaitingReconfirmation` | 已锁定，等待调用方重新确认 |
| `RejectedMalformed` | 空 `svID` 或 `smpCnt >= 4000` |

`SvDecision` 还提供 `lostSamples`、`gapStart`、`gapEnd`、`invalidSmpCnts` 和
累计丢失数 `totalLostSamples`。`stats(svID)` 提供接收、接受、重复、重放、
来源拒绝、畸形帧、缺口事件及累计丢失统计。

计数值按模距离解释：距离不超过 2000 视为向前跳变，超过 2000 视为旧帧重放。
若真实停流超过半个计数周期，请在确认恢复后调用 `resetSequence(svID)`，
以当前首帧建立新基线；该操作不改变来源绑定。

`SvSubscriber` 本身不内建锁。每个实例应由单一订阅线程持有，或在调用方
提供外部同步后共享。

## 验证

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：首帧绑定、顺序接收、重复帧、旧帧重放、普通丢帧、跨
`3999 -> 0` 回绕的丢帧、回绕正常接收、换 MAC、换 `confRev`、MAC 与
`confRev` 同时变化、锁定后的重新确认、多 `svID` 隔离、畸形计数和长停流
重新同步。
