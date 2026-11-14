# 导航定位可信度组件

面向 C++20 导航接收机的零依赖可信度组件。接收机将多星座、多频点的独立定位解以及接收机最终解送入组件，组件结合 C/N0、到达时间（TOA）、星历、惯性预测和地图约束进行交叉核对。异常超过阈值时返回人工确认或拒绝，不把可疑偏移解作为可用导航位置。

## 构建与导入

```bash
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

静态库目标为 `netsec::netsec`，公共头文件为 `netsec.hpp`。安装后可这样使用：

```cmake
find_package(netsec CONFIG REQUIRED)
target_link_libraries(navigation_receiver PRIVATE netsec::netsec)
```

## 最小调用示例

```cpp
#include "netsec.hpp"

netsec::PositioningTrustComponent trust;

netsec::TrustInput input;
input.timestamp_ns = 1'000'000'000;
input.receiver_fix.position = {31.2304, 121.4737, 10.0};
input.receiver_fix.timestamp_ns = input.timestamp_ns;

input.source_fixes.push_back({
    .source_id = "gps-l1",
    .constellation = netsec::Constellation::Gps,
    .band = netsec::FrequencyBand::L1,
    .position = {31.2304, 121.4737, 10.0},
    .timestamp_ns = input.timestamp_ns,
    .cn0_dbhz = 44.0,
    .expected_cn0_dbhz = 44.0,
});

// 再追加 Galileo/E1、BeiDou/B1 等独立源定位解。
// 可选：input.inertial、input.map。

const netsec::TrustDecision decision = trust.evaluate(input);
if (!decision.accepted) {
    // 进入人工确认；如需保守保持，可使用 decision.safe_position。
}
```

`GnssSourceFix` 表示同一星座/频点组合的独立定位解。接收机若只有原始伪距，应先在测量引擎内为各星座/频点分别求解，或至少生成可交叉核对的独立源解；`expected_cn0_dbhz`、`toa_residual_ns`、`ephemeris_consistent` 和 `ephemeris_id` 可直接来自测量引擎。

## 判定语义

| 结果 | 是否接受 | 含义 |
| --- | --- | --- |
| `Trusted` | 是 | 多源、惯性和地图检查均通过 |
| `Degraded` | 是 | 仅存在警告且没有完整性冲突，例如单星座或单频点冗余不足 |
| `ManualConfirmation` | 否 | 任一关键异常或评分低于阈值，必须人工确认 |
| `Rejected` | 否 | 无有效接收机解或没有有效源解 |

关键异常始终进入 `ManualConfirmation`，不受数值评分影响。`accepted == false` 时，`safe_position` 优先给出最近一次可信位置，其次给出可用惯性预测；不会回填攻击输入中的偏移位置。

## 校验内容

- 多星座、多频点独立源数量及组合冗余；单源和单一星座/频点组合会降级。
- 源间聚类与共识中心；单个源偏离共识标记 `SourceOutlier`。
- C/N0 下限、绝对上限、相对期望值突增、相对同侪中位数异常，以及逐源功率突变。
- TOA 残差、接收机/源时间戳偏差、时间回退和接收机钟差。
- 星历有效性、跨源一致性、陈旧程度及短时间内的星历身份突变。
- 跟踪源位置突变、接收机位置跳变、跳变隐含的超限速度。
- 惯性预测与最终解偏差。
- 地图禁行状态、允许多边形和路线走廊距离。

所有阈值集中在 `TrustConfig`，可通过 `set_config()` 调整。`evaluate()` 是线程安全的，内部保存逐源和最近可信位置历史；`reset()` 清除历史，`set_config()` 也会清除历史。

## 验证场景

`tests/trust_tests.cpp` 覆盖：

- 合法多星座/多频点定位保持可信。
- 单星座冗余不足时降级但仍可接受。
- 单源位置欺骗被标记且不静默接受。
- 强信号 + TOA + 星历伪造触发人工确认。
- 位置跳变保持最近可信位置。
- 正常车辆移动不会被误判为位置跳变或单源突变。
- 地图约束阻止偏离路线。
- 时间异常、单源突变和无效输入。
- 重置后不残留历史状态。
