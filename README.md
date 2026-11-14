# 双冗余安全表决组件

面向 C++20 冗余控制器的可导入表决库。两个通道各自基于同一周期输入独立计算，
表决器对通道身份、周期、同步状态、输入摘要和结果做双向交叉校验。任何差异都产生
`SafeStop`，不会从两个通道中选择一个结果，也不会退化为单通道运行。

## 安全语义

- 身份：报告自带 `ChannelId`，必须恰好组成 `{A, B}`，参数顺序不作身份依据。
- 周期：两通道周期必须等于当前 `CycleFrame`，并对已执行周期做严格单调重放检查。
- 同步：两通道必须处于 `InSync`，且同步纪元与同步计数器完全一致。
- 输入：表决器自己从原始输入计算 SHA-256 摘要，不信任通道提供的摘要。
- 结果：表决器从原始结果重新计算摘要，并绑定周期与输入摘要；结果字节必须完全一致。
- 差异：一律 `SafeStop` 且不授权执行器。结果不一致时无法安全归因，两个通道都会隔离。
- 隔离：可归因故障只隔离故障通道；隔离状态会锁存，必须显式恢复后才能重新表决。
- 执行器门控：只能通过 `decision.actuator_authorized()` 判断是否驱动执行器。

## 接入示例

```cpp
#include "netsec.hpp"

netsec::VotingComponent voter;

const auto input = std::as_bytes(std::span{input_bytes});
const auto output_a = std::as_bytes(std::span{channel_a_output});
const auto output_b = std::as_bytes(std::span{channel_b_output});

const netsec::CycleFrame frame{cycle, sync_epoch, sync_counter, input};
const auto report_a =
    netsec::make_report(frame, netsec::ChannelId::A, output_a);
const auto report_b =
    netsec::make_report(frame, netsec::ChannelId::B, output_b);

const netsec::VoteDecision decision = voter.vote(frame, report_a, report_b);
if (decision.actuator_authorized()) {
    actuator.apply(decision.output);
} else {
    actuator.safe_stop();
}
```

通道输出若由故障通道生成，接入方仍需按协议填写周期、同步标记和摘要；表决器会
重新计算摘要并拒绝被篡改或不一致的证据。真实系统中 `make_report` 应位于各通道
自己的可信计算边界内，而不是在表决器进程内代算。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_CXX_STANDARD=20
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：合法双通道表决、单通道输入/结果篡改、结果摘要篡改、同步计数器漂移、
同步纪元与状态异常、周期不一致、重复身份、重放窗口、故障隔离锁存与显式恢复。
