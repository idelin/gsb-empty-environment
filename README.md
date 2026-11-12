# 推测路径安全索引

解释器执行不可信字节码时用数组索引读取查表，攻击者可训练分支预测，让越界读在推测路径上泄露密钥。为 C++17 解释器写可嵌入的推测安全索引头文件：正常路径越界仍报错，另提供无分支掩码，把长度非二次幂的表填充到二次幂后再用掩码取值，使被绕过检查时索引仍落在表内。调用方可注入跳过检查的测试钩子，用 0、len-1、len、负数转换值和 SIZE_MAX 验证钩子下访问也在表内。

## 思路（Spectre-v1 / bounds-check bypass）

经典防护是把条件分支替换为无分支掩码（参考 Linux 内核
`array_index_mask_nospec`）：任何索引都先与 `capacity - 1` 做无条件 AND，
再用于寻址。只要容量是二次幂，掩码后的索引恒在 `[0, capacity)` 内，分支
预测是否被训练都不影响推测读的落点。

- `netsec::next_power_of_two(n)`：把非二次幂逻辑长度向上填充到二次幂。
- `netsec::mask_index(index, mask)`：`constexpr` 单条 AND，无分支、无比较。
- `netsec::speculation_barrier()`：`atomic_signal_fence` 编译栅栏，阻止优化器
  把掩码路径重新推导成条件分支/cmov；本身不发射指令。
- `netsec::PaddedTable<T>`：按二次幂容量分配，逻辑 `size()` 之外是填充槽。

## 两条访问路径

- 架构路径 `at(index)`：越界（`index >= size()`）抛 `std::out_of_range`，
  正常语义不变。
- 推测安全路径 `operator[](index)`：`noexcept`、无分支，恒经掩码落在分配内，
  命中真实槽或填充槽，绝不会读到相邻内存。

注入的 `SkipBoundsCheckHook` 在 `at()` 中模拟“检查被推测绕过”：钩子返回
`true` 时跳过报错，但取值仍走 `operator[]` 的掩码路径。

## 用法

```cpp
#include "netsec.hpp"

netsec::PaddedTable<int> table(5);          // 逻辑长度 5，实际容量 8
table.at(5);                                 // 抛 std::out_of_range
table[5];                                    // 无分支，落到填充槽 5，安全

table.set_skip_check_hook(
    [](std::size_t /*raw*/, void*) noexcept { return true; });
table.at(SIZE_MAX);                          // 模拟绕过检查：仍在分配内
```

钩子签名：

```cpp
using SkipBoundsCheckHook =
    bool (*)(std::size_t raw_index, void* user_data) noexcept;
```

有符号操作数请用 `at_signed` / `get_signed`；负整数按模 `2^N` 转换后同样被
掩码约束。

## 验证

`test_spec_index.cpp` 用 `0`、`len-1`、`len`、负数转换值（`-1`）和 `SIZE_MAX`
验证：正常路径越界抛异常；注入钩子后每个解析地址都落在二次幂分配内，且等于
对应掩码槽。在 `-O0/-O2/-O3` 以及 ASan+UBSan 下均通过。

```sh
cmake -S . -B build && cmake --build build && ctest --test-dir build --output-on-failure
```

反汇编确认取值路径为无条件 `and` + 寻址加载，索引路径无分支。
