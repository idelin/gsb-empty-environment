# netsec：可导入的推测执行屏障组件

`netsec` 提供一组 C++20 头文件 API 和稳定的 C ABI，用于阻断 Spectre v1
风格的越界推测加载，以及跨信任边界的推测穿透。

## 核心原则

- **编译器屏障**（`netsec::compiler_barrier`）只约束编译器重排，不约束 CPU
  推测。
- **体系结构屏障**（`netsec::speculation_barrier`）按目标发出指令：
  x86 使用 `LFENCE`，AArch64 使用 `CSDB`，RISC-V 使用 `FENCE`/`FENCE.I`，
  POWER 使用 `ISYNC`，s390x 使用 `BCR 15,0`。
- **内存屏障**（`netsec::memory_barrier`）用于发布/消费顺序，不替代推测屏障。
- 越界加载前先做无分支索引掩码和指针掩码，再插入推测屏障，最后才加载。
- 信任边界入口与出口分别执行屏障；RAII 守卫在异常展开时也会执行出口屏障。

## 使用示例

```cpp
#include "netsec.hpp"

std::uint8_t read_secret(const std::uint8_t* table,
                         std::size_t length,
                         std::size_t attacker_index) {
    // 越界时返回 0；加载地址始终被掩码到合法范围。
    return netsec::speculation_safe_load(table, length, attacker_index);
}

int trusted_entry(int value) {
    netsec::TrustBoundary boundary;  // 入口屏障；析构时执行出口屏障

    netsec::enter_trust_boundary();
    // ... 处理不可信输入 ...
    netsec::leave_trust_boundary();

    return value;
}
```

需要手动保持“检查后加载”结构时，可显式使用：

```cpp
if (index < length) {
    const std::size_t safe_index = netsec::mask_index(index, length);
    const auto* safe_base = netsec::mask_pointer(base, true, fallback);
    netsec::speculation_barrier();
    value = safe_base[safe_index];
}
```

## C ABI

运行时也可以链接 `netsec` 并导入以下 C 符号：

`netsec_compiler_barrier`、`netsec_speculation_barrier`、
`netsec_memory_barrier`、`netsec_full_barrier`、`netsec_mask_index`、
`netsec_mask_pointer`、`netsec_speculation_safe_load_u8`、
`netsec_speculation_safe_load_u64`、`netsec_trust_boundary_enter`、
`netsec_trust_boundary_leave`、`netsec_abi_version`。

## 测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：

- 越界推测：在 `PROT_NONE` 保护页前放置数组，强制走“无边界分支”的
  掩码加载路径，验证不会触碰越界页。
- 侧信道恢复：用探测数组记录访问索引，验证安全加载与掩码推测体都不会
  暴露越界密钥，而未加固基线会暴露。
- 合法访问：所有合法索引返回原值，空指针/零长度安全返回默认值。
- 屏障验证：编译 `barrier_probe.cpp` 到汇编，按目标架构检查
  `LFENCE`/`CSDB`/`FENCE`/`ISYNC` 等指令确实存在；同时验证 C ABI
  和信任边界异常展开。
