# netsec：时序防护的密钥比较

面向 C++20 安全模块的可导入组件。它把密钥/摘要比较放在双冗余通道中执行，
用硬件确认寄存器锁存最终结果，并在整个比较、最短周期和确认窗口内监控时钟。
任何时钟异常都会立即使确认寄存器失效、清除已锁存结果、递增复位计数并写入故障日志。

## 防护模型

- **常量时间比较**：两个独立通道分别正向和反向遍历完整公开长度；字节差异用
  按位或累积，循环次数不取决于密钥内容，长度不等和首字节不等都不会提前返回。
- **双通道冗余**：只有两个通道结论一致，且硬件确认寄存器同时保存两份通道证据时
  才可能接受。通道结论冲突按故障处理，不能产生成功结果。
- **硬件结果锁存**：调用者可见的 `equal == true` 只来自
  `HardwareConfirmRegister::read()` 的回读值；锁存失败、回读缺失、回读值被篡改
  或通道证据不一致均失败关闭。
- **时钟监控**：拒绝无效采样、回退计数器、冻结计数器和超过
  `maximum_cycles` 的跳变。检测到异常后立即复位并记录。
- **最短周期**：从起始采样到硬件锁存及回读完成，至少消耗
  `minimum_cycles`；普通不匹配和硬件拒绝路径同样受约束。
- **故障日志**：内置 32 条无分配环形日志，并可通过 `FaultSink` 转发到平台日志。

纯软件实现不能替代芯片级时钟毛刺检测。正式部署应把 `ClockSource` 接到硬件
时钟监视器/周期计数器，把 `HardwareConfirmRegister` 接到写保护寄存器或安全
外设，并使用目标平台支持的常量时间编译选项。

## 导入

```cmake
add_subdirectory(path/to/netsec)
target_link_libraries(your_security_module PRIVATE netsec)
```

安装后也可以使用：

```cmake
find_package(netsec 1 REQUIRED)
target_link_libraries(your_security_module PRIVATE netsec::netsec)
```

也可以直接把 `netsec.hpp` 和 `netsec.cpp` 加入已有构建。库要求 C++20。

## 使用

```cpp
#include "netsec.hpp"

netsec::TimingPolicy policy{
    .minimum_cycles = 2'000,
    .maximum_cycles = 20'000'000,
    .maximum_stalled_samples = 64,
};

MyClockMonitor clock;             // 实现 netsec::ClockSource
MyConfirmRegister hardware;       // 实现 netsec::HardwareConfirmRegister
netsec::TimingGuard guard(policy, clock, hardware, &platform_fault_sink);

const auto result = guard.compare(expected_key, supplied_key);
if (!result) {
    // equal == false；根据 result.status / result.fault 拒绝请求。
}
```

`compare()` 同时提供 `std::span<const std::uint8_t>`、`std::string_view` 重载。
只需要常量时间原语时，可调用 `netsec::constant_time_equal()` 或
`netsec::secure_compare()`；这两者不包含时钟监控和硬件锁存。

`HardwareConfirmRegister` 的关键契约：

1. `latch()` 必须把两个通道结论分别保存到冗余存储，并在硬件中计算最终 AND。
2. `read()` 只在操作 ID 和每次操作令牌都匹配时返回；旧事务的回读必须失败。
3. `invalidate()` 必须清除锁存和冗余副本。正常成功也绝不能绕过回读。

## 验证

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

测试覆盖：时钟无效/回退/冻结/超频毛刺、首字节提前返回、双通道及硬件回读不一致、
合法比较、确认寄存器拒绝/篡改、最短周期和复位后重试。
