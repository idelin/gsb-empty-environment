# 传感器融合被单一伪造源带偏

自动驾驶融合模块等权合并摄像头、雷达与激光雷达，攻击者只伪造一种传感器就让目标被误判。请实现供C++20感知融合组件调用的可导入来源可信度组件。每源按时间、标定、置信度与一致性加权，单源突变与跨源冲突标记；降级时提高保守阈值并请求人工。攻击特征进入评分。用单源伪造、跨源冲突、合法融合和降级验证不静默接受。

## 组件：`netsec::trust`（source_trust.hpp / source_trust.cpp）

```cpp
#include "source_trust.hpp"
netsec::trust::TrustFusion fusion;            // 可传 TrustConfig 调整阈值
auto r = fusion.fuse(observations, now_s);    // observations: vector<Observation>
// r.decision == Accept / RequireHuman；r.fused_value 为可信度加权结果
// r.sources 含每源 weight/jump/conflict/attack 标记；r.reasons 可审计
```

- 每源得分 = 时间新鲜度(exp 衰减) × 标定健康度 × 自报置信度 × 历史一致性(EMA 偏差) × 冲突降权 × 攻击惩罚
- 单源突变（偏离自身 EMA 超阈值）与跨源冲突（偏离鲁棒中位数中心）被标记；冲突源默认剔除出融合，防止单源伪造带偏结果
- 任何异常信号进入降级态：接受阈值从 `accept_threshold` 提高到 `conservative_threshold`，并置 `human_requested`；攻击特征存在时一律不接受
- 源数量不足 `min_sources` 时拒绝静默接受

## 构建与测试

```sh
cmake -S . -B build && cmake --build build && ctest --test-dir build --output-on-failure
```

测试覆盖：合法融合、单源伪造、跨源冲突、降级（攻击特征+过期+突变）、源不足。
