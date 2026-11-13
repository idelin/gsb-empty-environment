# CAN总线错误帧注入导致的节点下线检测

## 威胁模型

攻击者在受害节点周期帧的数据位上注入显性位，使该节点每次发送失败，发送错误计数（TEC）按
CAN 规则每次加 8，最终超过 255 进入 bus-off 而被总线静默。

## 检测规则（按 ID 维护状态）

- 错误帧：TEC 估计 `+8`；成功帧：TEC 估计 `-1`，下限钳制为 `0`。
- TEC 达到 `128`：记为被动错误（error-passive）并上报告警。
- TEC 超过 `255`（即 `>=256`）：记为 bus-off 并上报告警。
- 背景噪声抑制：错误分散在多个 ID、且所有 ID 的 TEC 均低于 `64` 时不产生告警，
  仅通过 `backgroundNoiseDetected()` 暴露该状态；任一 ID 达到 `64` 即退出噪声区间。
- 注入攻击签名：同一 ID 连续 `16` 次错误后进入攻击监视。连错所在周期属于活跃
  周期（不计缺席）；其后若连续两个调度周期内该 ID 既无错误帧也无成功帧，则在
  第二个缺席周期末判定为注入攻击并告警。缺席期间一旦恢复任何帧活动，缺席计数清零。
- 周期语义：`onErrorFrame/onSuccessFrame` 在周期内调用，`tickPeriod()` 在每个
  调度周期末调用一次，内部用周期活跃标记区分“活跃”与“缺席”。
- 告警为边沿触发：阈值只在跨越时上报一次；成功帧使 TEC 回落到阈值以下后重新武装，
  再次跨越可再次告警。攻击告警按攻击片段锁定（latched）。

所有阈值集中在 `netsec::DetectorConfig`，默认值即题目要求，可按项目调整。

## 接入方式

`netsec` 是一个头文件 + 一个源文件的 C++17 静态库，内部用 `std::mutex` 保护，
错误帧回调线程、周期调度线程、告警消费线程可并发调用。

```cpp
#include "netsec.hpp"
netsec::CanAttackDetector detector;

// CAN 驱动错误帧回调
void on_can_error_frame(uint32_t can_id) {
    detector.onErrorFrame(can_id);   // TEC += 8
}
// 发送成功回调
void on_can_tx_ok(uint32_t can_id) {
    detector.onSuccessFrame(can_id); // TEC -= 1，不低于 0
}
// 调度器每个周期结束调用一次
void on_period_end() {
    detector.tickPeriod();           // 统计缺席，返回本周期新攻击数
}
// 告警上报线程
void pump_alarms() {
    for (const auto& alarm : detector.drainAlarms()) {
        // alarm.type: ErrorPassive / BusOff / InjectionAttack
    }
}
```

查询接口：`getStatus(id)`、`listStatus()`、`hasAttack(id)`、
`backgroundNoiseDetected()`、`trackedNodeCount()`、`reset()`。

## 构建与验证

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
./build/can_driver_demo
```

`tests/test_detector.cpp` 覆盖题目要求的全部验证场景：

- 连续注入：32 次错误使 TEC 到 256，依次产生一次被动错误（TEC=128）和一次
  bus-off（TEC=256）告警，持续故障不重复告警。
- 背景噪声：4 个 ID 各 7 次错误（TEC=56 < 64），无任何告警且噪声标志置位；
  单 ID 不算分散；任一 ID 升到 64 后噪声标志清除。
- 攻击签名：16 连错所在周期为活跃周期，其后连续两个缺席周期判攻击；缺席一
  周期后恢复活动则计数清零；仅 15 连错不判；16 连错后出现成功帧则解除监视。
- 恢复：成功帧逐帧递减并钳制在 0，状态从被动/ bus-off 回到主动，重新爬升可
  再次触发告警；从 bus-off 全程恢复无虚假告警。
- 线程安全：生产者（错误/成功帧）、周期 tick、告警消费三线程并发冒烟测试。
