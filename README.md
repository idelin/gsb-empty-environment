# 释放访问先后关系检测

`netsec` 是一个 C++17、可嵌入分配器/运行库的 free/access 检测组件。它不依赖真实线程或采样，只消费运行库已经观察到的事件，并用每线程向量时钟判断访问与释放是否缺少 happens-before 关系。

## 事件

- `alloc(thread, address, size)`：登记一次分配，并为该地址分配新的代号。
- `free(thread, address/token)`：登记释放。
- `access(thread, address/token)`：登记一次读或写访问。
- `release(thread, sync_id)`：发布该线程当前的向量时钟。
- `acquire(thread, sync_id)`：合并同一 `sync_id` 已发布的全部释放时钟。

线程由调用方提供稳定的整数 id，因此组件不需要 TLS，也不要求事件来自 `std::thread`。组件内部加锁，可由多个运行库线程并发投递事件；确定性测试则可以按选定的顺序串行投递。

## 判定

对同一分配代号内的 free 和 access 比较向量时钟：

- `access < free`：访问发生在释放之前，合法。
- `free < access`：存在 happens-before 的释放后使用，报告 `OrderedUseAfterFree`。
- 两者都不可比较：访问和释放落在并发窗口内，报告 `ConcurrentFreeAccess`。

先访问、后释放但没有任何同步边的情况仍属于并发竞争，不会被当成“访问在释放前所以合法”。报告带有分配/释放/访问序号、线程 id、地址、代号、对象大小和两份向量时钟快照。

## 地址复用

每个地址维护独立的代号计数器。`alloc` 返回 `Allocation{address, generation, ...}`：

- 按地址调用 `free`/`access` 时，只作用于该地址的当前代号。
- 传入 `Allocation` 令牌时，只作用于令牌中的代号。
- 同地址重新分配会递增代号，旧代号的待判定访问在旧释放时结算并清空，不会被带到新对象。
- 复用后携带旧令牌的延迟访问仍归旧代号，不会计入新对象；新对象的释放也不会与旧访问配对。

## 示例

```cpp
#include "netsec.hpp"

netsec::FreeAccessDetector detector;
auto object = detector.alloc(/*thread=*/1, /*address=*/0x1000, 64);
detector.free(1, object);
detector.release(1, /*sync_id=*/7);
detector.acquire(2, 7);
detector.access(2, object.address);  // free happens-before access

for (const auto& report : detector.reports()) {
    // "ordered-use-after-free"
    netsec::report_kind_name(report.kind);
}
```

`report_count()`、`report_count(kind)`、`clear_reports()` 和 `reset()` 用于集成到运行库的统计或测试钩子。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

`tests/free_access_test.cpp` 使用固定顺序的事件回调表，分别验证有序 UAF、并发窗口、合法的先访问后释放，以及同地址两代对象复用时的报告归属。
