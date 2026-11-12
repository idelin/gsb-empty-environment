# 秘密缓冲扩容残留

安全消息库扩容后，旧内存块中的秘密可能残留。实现可嵌入C++17服务的字节容器，增长先分配再复制，成功切换后清零旧块；失败保持原值，缩容清零被截部分，移动后原对象不可访问旧内容。用可观察分配器验证每个失败点、容量增长和自赋值，不能用编译器可消除的普通填零声称安全清除。

## 交付内容

- `netsec.hpp`：header-only 的 `netsec::basic_secure_buffer<Allocator>`（默认别名 `netsec::secure_buffer`），可直接嵌入 C++17 服务。
- `observable_allocator.hpp`：`netsec::test::observable_allocator`，记录每次分配/释放、支持按序号或条件注入 `std::bad_alloc`，并在释放前快照整块内存。
- `test_secure_buffer.cpp`：18 个场景、540 项断言，覆盖每个分配失败点、容量增长、缩容、移动与自赋值。
- `zero_check.cpp` / `zero_check_negative.cpp`：安全清除与普通 `memset` 的 `-O3` 汇编正反对照。

## 安全语义

- 增长：先 `allocate` 新块并复制，指针切换成功后才对旧块做不可消除清零再 `deallocate`；分配或复制抛异常时旧对象完全不变（强异常保证），新块也会先清零再归还。
- 失败保持原值：`reserve`、`resize`（增长）、`push_back`、`append`、拷贝构造、拷贝赋值在分配失败后，`size`/`capacity`/数据保持原值，对象仍可继续使用。
- 缩容：`resize` 缩小时原地清零被截断的字节，不触发重分配；前缀内容保留。
- 移动：移动构造/赋值直接转移所有权，源对象的指针、大小、容量全部置空，无法再访问旧内容；自移动走显式自赋值保护。
- 析构与 `clear`：对整块容量（含未使用尾部）/已用区域做安全清零后再释放。

## 为什么清零不会被编译器消除

普通 `std::memset` 对“之后不再读取”的缓冲区属于死存储，`-O3` 下可被删除。本实现按平台选择不可消除的原语，并附带编译器屏障：

- Windows：`SecureZeroMemory`
- Apple / C11 Annex K：`memset_s`（运行时约束接口，标准禁止优化掉）
- 其他平台：`volatile` 逐字节存储（volatile 访问是可观察副作用）+ `asm volatile("" ::: "memory")`

验证方式（`make asm-check`）：

- `zero_check.cpp` 在 `-O3` 汇编中仍保留 `bl _memset_s`。
- `zero_check_negative.cpp` 中的普通 `memset` 在同一优化级别下被完全消除，作为反面对照。

## 构建与测试

```sh
make test        # -O0 与 -O3 两套功能测试 + 汇编正反对照
```

或使用 CMake：

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build
```

已在 Apple clang 21（C++17，`-Wall -Wextra -Wpedantic -Werror`）下通过 `-O0`、`-O3` 功能测试、ASan/UBSan 消毒测试与汇编检查。
