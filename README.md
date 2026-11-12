# 堆分区同页隔离分配器

`netsec` 是一个可嵌入 C++17 服务的分区 slab 分配器，用来降低堆风水攻击中
“攻击者可控对象与敏感对象落在同一页/相邻位置”的风险。

## 隔离模型

- 调用方为每次请求指定类型域 `DomainId`。分配器按域建立独立的
  `DomainState`，每个 slab 只属于一个域和一个二次幂大小类。
- slab 是由 `mmap` 得到的连续页，整段映射只供一个域使用，因此不同域的
  块不会共享页。slab 元数据（块位图、空闲链表等）放在普通堆上，不占用
  可分配页。
- 释放块先进入该域自己的 FIFO 隔离队列，不会直接回到空闲链表。队列同时
  受 `quarantine_blocks`、`quarantine_bytes` 和 `quarantine_ops` 约束：
  - 刚释放的块永远先入队，容量压力只淘汰队首的旧块；
  - 块至少经过 `quarantine_ops` 次成功分配/释放操作后才会老化出队；
  - 出队后的 slab 块只进入原域、原大小类的空闲链表。
- 大于 `large_threshold` 或对齐要求超过一页的请求绕过 slab，直接使用
  `mmap`。`PageOwner(ptr)` 可查询该映射（包括隔离期内的映射）属于哪个域；
  隔离队列淘汰大块后才真正 `munmap`，因此紧跟释放的同尺寸分配不会拿回
  刚释放的地址。

## 公开接口

```cpp
#include "netsec.hpp"

netsec::PartitionedAllocator allocator;

void* secret = allocator.Allocate(sizeof(Secret), alignof(Secret), 0x01);
netsec::DomainId owner = allocator.PageOwner(secret);  // 0x01

allocator.Deallocate(secret);        // true；进入 0x01 的隔离队列
allocator.IsQuarantined(secret);     // true
```

关键接口：

- `Allocate(size, alignment, domain)`：失败或参数非法时返回 `nullptr`。
  `alignment` 必须是二次幂；`size == 0` 按 1 字节处理。
- `Deallocate(ptr)`：只接受精确块首地址；未知地址、内部指针、二次释放和
  仍在隔离期的指针返回 `false`。
- `PageOwner(ptr)`：返回包含 `ptr` 的分配器页所属域。对空指针、外部地址
  或已 `munmap` 的地址返回 `kNoDomain`。查询不解引用 `ptr`。
- `IsAllocated`、`IsQuarantined`、`KindOf`、`Owns` 用于诊断和测试。
- `Stats()`、`StatsFor(domain)` 暴露活动/隔离块数、字节数和 slab 映射量。

`AllocatorConfig` 可调整 slab 大小、大块阈值、隔离队列容量与老化周期。
`slab_size` 需为二次幂且至少两页，`large_threshold` 需在
`min_block_size` 与 `slab_size` 之间。

该类对分配、释放和查询做了互斥保护，可并发调用。它不是 `operator new`
替换器：slab 元数据使用普通 C++ 堆。

## 验证

`tests/test_allocator.cpp` 包含固定边界测试和随机序列对照参考模型：

- 跨域分配期间，逐页检查 `PageOwner`，并维护页归属表，任意时刻不允许
  两个域共享一页。
- 每个释放后立即探测同域、同尺寸、同对齐的分配，确认返回值既不是刚
  释放的块，也不是任意隔离期块。
- 参考模型独立维护活动块、隔离队列、容量淘汰和老化；逐步比对块数、
  字节数、域统计和 `epoch`。
- 验证大块/超对齐请求走 `KindOf == LargeMmap`，跨页归属正确，隔离期内
  仍归原域，淘汰后 `PageOwner` 回到 `kNoDomain`。
- 验证二次释放、内部指针释放、非二次幂对齐和外部地址查询。

构建与运行：

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

如果 macOS 上 `xcode-select` 不可用，可为 CMake 显式指定
`CMAKE_AR`/`CMAKE_RANLIB` 指向 CommandLineTools 中的 `ar`/`ranlib`。

## 边界说明

- slab 块地址在本分配器生命周期内不会被别的域复用；大块 `mmap` 在隔离
  期结束后归还系统，之后操作系统可能把同一地址交给任意域。这是“直通
  mmap”与按页保留地址之间的取舍。
- 隔离队列降低立即重用类攻击，但不提供 use-after-free 检测；块位图只
  检测二次释放。
