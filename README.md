# netsec：PTP 主时钟准入与单调时间保护

这是一个可导入 C++20 时间同步服务的主时钟准入组件。它解决的核心问题是：
未认证或未授权的 Announce 不能进入 BMCA，更不能把从设备时间拨到攻击者
选择的值。

## 安全模型

- **信任先于选择**：只有配置的 `TrustedMaster` 才能进入候选集。未知源、
  被禁用源和错误端口在 BMCA 之前即被拒绝。
- **先认证再评估**：Announce 的关键字段用 HMAC-SHA256 覆盖，包括源/祖时钟
  身份、优先级、clockClass、accuracy、variance、stratum、序列号、时间和
  leap/UTC offset。key id 也在 MAC 内，防止降级。认证缺失、空标签、篡改和
  错误 key id 均不会改变当前最佳主时钟。
- **防重放**：每个源维护 16 位序列号窗口；重复或回退的合法签名公告仍按
  重放拒绝。主时钟重启可由运维调用 `resetSourceState()` 显式放行。
- **策略门限不随公告变化**：全局 `AdmissionPolicy` 与每个 `TrustedMaster`
  的优先级区间、stratum 上限、clockClass 上限以及可选质量钉扎共同生效。
  伪造的“更优”公告无法降低这些门限，也无法进入选择。
- **BMCA 隔离**：只对通过认证和策略门限的候选做 BMCA。默认使用 IEEE 1588
  dataset comparison；也可切换到 `PriorityStratumIdentity` 策略。
- **时间输出单调**：大步向前跳变默认拒绝，也可选择截断或 slew；向后校正
  永不直接回拨，只允许受限减速追赶。leap second 与频率/漂移均受配置上限
  约束，所有越界行为产生 `Alarm`。
- **同步源绑定**：`TimeSyncGuard::submitSync()` 只接受当前 BMCA 选中主时钟
  的 Sync 样本，避免“公告被拒但 Sync 仍被应用”。

## 构件

| 类型 | 用途 |
| --- | --- |
| `MasterClockAdmission` | 公告认证、策略检查、防重放、候选缓存与 BMCA |
| `BoundedMonotonicClock` | 有界 step/slew、跳变/闰秒/漂移告警、单调输出 |
| `TimeSyncGuard` | 组合以上两层，并把 Sync 绑定到当前选中主时钟 |
| `signAnnouncement` / `verifyAuthentication` | HMAC-SHA256 认证辅助函数 |

## 使用示例

```cpp
#include "netsec.hpp"

using namespace std::chrono_literals;
using namespace netsec;

const ClockIdentity primary =
    *ClockIdentity::fromString("00:11:22:33:44:55:66:77");

TrustedMaster trusted =
    makeTrustedMaster(primary, "deployment-shared-secret", 1, 7);
trusted.allowed_priority1 = PriorityRange{10, 20};
trusted.max_stratum = 2;

AdmissionPolicy admission_policy;
admission_policy.max_stratum = 3;

ClockSafetyPolicy clock_policy;
clock_policy.max_step = 250ms;
clock_policy.max_slew_ppm = 200.0;
clock_policy.max_drift_ppm = 100.0;
clock_policy.jump_policy = JumpPolicy::Reject;

TimeSyncGuard guard(
    admission_policy, {trusted}, clock_policy, Timestamp{0});

const auto auth_key = keyFromText("deployment-shared-secret");
Announcement announce = makeAnnouncement(primary, 10, 2, 1);
announce = signAnnouncement(announce, auth_key, 7);

const AdmissionResult admitted = guard.submitAnnouncement(announce, Timestamp{0});
if (!admitted) {
    // admitted.reason is a RejectionReason; admitted.alarms has details.
}

SyncSample sync;
sync.source = PortIdentity{primary, 1};
sync.local_time = Timestamp{1s};
sync.master_time = Timestamp{1s};
const TimeUpdateResult update = guard.submitSync(sync);
const Timestamp monotonic_now = guard.now(Timestamp{2s});
```

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：合法主时钟、伪造的更优主时钟、优先级操纵、stratum 越界、认证
缺失/篡改、重放、过期、祖时钟钉扎、BMCA 选择、向前/向后跳变、leap second、
漂移上限以及 Sync 源绑定。

## 集成说明

本组件处理的是时间同步服务内部的准入与时间安全策略，不负责 PTP 报文编解码
或网络收包。服务层应把收到的 Announce 字段填入 `Announcement`，并保留原始
认证 TLV 的 key id/tag；`signAnnouncement()` 可用于测试或与同一 canonical
payload 约定互操作。Announce 的 `transmit_time` 与 `submit()` 的
`received_at` 使用同一时间域，只有提供 `transmit_time` 时才检查新鲜度。
