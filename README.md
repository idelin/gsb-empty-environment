# netsec 行激活监测组件

这是一个 C++20 主机防护库，用于跟踪 DRAM 行激活压力并在 Rowhammer
可利用前执行预防性刷新和隔离。库本身不直接访问物理内存；主机通过
`HardwareHooks` 接入 DRAM 控制器、ECC 驱动或固件刷新/隔离接口。

## 决策模型

- 每行维护滑动激活窗口、累计激活数、最近刷新时间和 ECC 计数。
- 压力值为 `窗口激活数 + ECC 可纠错数 * ecc_activation_weight`。
- 温度进入警戒/临界区间后，按配置缩短刷新间隔并降低激活阈值。
- 累计 ECC 可纠错数也会降低阈值；达到
  `corrected_ecc_threshold` 后直接隔离；不可纠错 ECC 立即隔离。
- 压力达到阈值时刷新目标行及 `neighbor_refresh_distance` 范围内的相邻行，
  并隔离 `isolation_radius` 范围内的可疑区域。越界激活会被拒绝，即使平台
  隔离钩子报告失败也保持逻辑隔离（fail-closed）。
- `RegionKind::PageTable` 区域使用独立阈值和刷新间隔。相邻刷新/隔离只在同一
  region 和 bank 内传播，普通内存攻击不会跨区污染页表页。

## 使用

```cpp
#include "netsec.hpp"

netsec::MonitorConfig config;
config.activation_window = std::chrono::milliseconds{100};
config.activation_threshold = 64;
config.page_table_activation_threshold = 16;

netsec::HardwareHooks hooks{
    .refresh_row = [](const netsec::RowAddress& row,
                      netsec::RefreshReason reason) {
        return platform_refresh_row(row, reason);
    },
    .isolate_row = [](const netsec::RowAddress& row,
                      netsec::IsolationReason reason) {
        return platform_quarantine(row, reason);
    },
};

netsec::RowActivationMonitor monitor(config, std::move(hooks));
monitor.register_page_table_region(/*region=*/7);

// Feed every observed row activation and ECC event.
auto action = monitor.record_activation({/*region=*/1, /*bank=*/0, /*row=*/42});
if (!action.accepted) {
    // The row is quarantined; do not perform the memory access.
}

monitor.set_temperature(platform_temperature_celsius());
monitor.record_ecc({1, 0, 42}, {.corrected_errors = 1});
monitor.maintain();  // periodic refresh-interval enforcement
```

页表 region 必须在记录该 region 的第一个事件前注册。时间戳默认使用
`std::chrono::steady_clock`，测试或回放场景可显式传入单调时间点。

## 构建与验证

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：

- 合法低频负载不被隔离，窗口频率和刷新间隔统计正确。
- 高频激活在阈值处刷新相邻行并隔离；随后 10,000 次激活全部被拒绝，
  模拟受害行的可利用翻转不会发生。
- 可纠错 ECC 先刷新，累计达到阈值后隔离；不可纠错 ECC 立即隔离。
- 高温降低有效阈值和刷新间隔。
- 刷新间隔到期时自动刷新。
- 普通内存攻击不会隔离独立的页表 region。
- 平台隔离钩子失败时仍保持 fail-closed。
