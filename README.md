# PMP / Smepmp 精确符号区间策略分析库

面向 RISC-V 固件安全验证宿主的 C++17 库，对物理内存保护（PMP）与 Smepmp
扩展（`mseccfg.MML` / `MMWP` / `RLB`）做**精确**（非采样）策略分析。
零外部依赖，仅使用 C++17 标准库，并以
`-Wall -Wextra -Wconversion -Wsign-conversion -Werror -pedantic-errors`
严格编译。

## 核心设计

库**绝不按物理地址逐字节循环**。所有有效表项先解码为闭区间，再收集全部
区间端点，排序去重后把 `[0, 2^PA_BITS)`（生产环境为 56 位）切分为
**最小不相交原子区间**；每个原子区间内任何字节的仲裁结果都相同，因此
只需对每个原子做一次表项仲裁。

- 解码（`src/pmp_policy.cpp` 中的 `decode_region`）
  - `OFF`：不匹配任何地址。
  - `TOR`：区间 `[pmpaddr[i-1]<<2, pmpaddr[i]<<2)`；`prev >= cur`
    （逆序/相等）时为空区间，不产生端点。
  - `NA4`：`pmpaddr` 最低位按 0 掩码（忽略低两字节地址位），4 字节自然对齐。
  - `NAPOT`：按 `pmpaddr` 末尾连续 1 的个数 `k` 解码为
    `2^(k+2)` 字节区域；偶数 `pmpaddr` 退化为 4 字节区域。
  - 跨越物理地址顶端的区域被裁剪到 `[0, 2^PA_BITS)`，不会溢出。
- 仲裁：S/U 态取**下标最小的覆盖项**；`MML=0` 时 M 态只接受锁定项，
  `MML=1` 时所有项均可仲裁 M 态，同样按下标最小优先。
- 默认策略：S/U 无匹配即拒绝；M 态仅在 `MML=0 且 MMWP=0` 时无匹配默认
  允许，`MMWP=1` 或 `MML=1` 时默认拒绝。

### Smepmp `MML=1` 十六种编码真值表

按 `L/R/W/X` 四位编码索引，`m` / `su` 两列分别是 M 态与 S/U 态有效权限，
完整实现于 `kMmlTable`：

| 编码 L R W X | M 态 | S/U 态 | 语义 |
| --- | --- | --- | --- |
| 0 000 | --- | --- | 全拒绝 |
| 1 001 | X | X | 共享只执行 |
| 2 010 | W | W | 共享只写 |
| 3 011 | WX | WX | 共享 WX |
| 4 100 | R | R | 共享只读 |
| 5 101 | RX | RX | 共享可执行 |
| 6 110 | RW | RW | 共享读写 |
| 7 111 | RWX | RWX | 共享全权 |
| 8 1000 | --- | X | M 拒绝 / S/U 只执行 |
| 9 1001 | --- | X | 禁止 M 执行 U 态代码 |
| 10 1010 | W | --- | M 私有只写 |
| 11 1011 | WX | --- | M 私有 WX |
| 12 1100 | R | R | 共享只读（锁定） |
| 13 1101 | RX | RX | 共享只读可执行 |
| 14 1110 | RW | R | M 可写，S/U 只读 |
| 15 1111 | RWX | RX | M 私有全权，S/U 只读执行 |

`MML=0` 的经典语义单独实现：未锁定项对 M 态不可见；锁定项对 M 态
授予 `R/X` 但**永不授予 W**，对 S/U 保留 `R/W/X`。

## 对外接口（`include/pmp/pmp_policy.hpp`）

- `PmpAnalyzer(pa_bits = 56)`：构造分析器；`validate` 校验表项数量
  （≤64）、模式枚举与 `pmpaddr` 位宽。
- `analyze(cfg)`：返回覆盖整个物理空间的有序 `AtomicRegion` 列表，
  每个原子带 M/SU 两域的有效权限与命中表项索引（无匹配为 `-1`）。
- `find_violation(cfg, spec)`：对照 `IsolationSpec`（M 私有区、
  S/U 隔离区、禁止 W^X 区）返回**最低地址**违例 `Violation`
  （地址 + 命中表项索引 + 违例类型）；无违例返回 `nullopt`。
- `check_transition(from, to)`：跨状态迁移检查
  - `RLB=0`（且 `MML=0`）时锁定 `pmpcfg` 全字段不可改；
  - 锁定项 `i` 同时锁定 `pmpaddr[i]` 与作为其 TOR 下界的
    `pmpaddr[i-1]`（与下邻自身模式无关）；
  - `MML`、`MMWP` 置 1 后粘性不可清除；
  - `MML=1` 时不允许使能 `RLB`。
- `m_perm_at` / `su_perm_at`：对原子分区做二分查询的单点权限便捷接口。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

也可直接调用编译器（无需归档工具）：

```sh
clang++ -std=c++17 -Wall -Wextra -Wconversion -Wsign-conversion -Werror \
        -pedantic-errors -Iinclude -Itests \
        src/pmp_policy.cpp tests/test_pmp_policy.cpp -o pmp_tests
./pmp_tests
```

## 差分验证

`tests/reference_matcher.hpp` 是**独立重写**的参考模型：不复用库的解码器，
按架构手册对 12 位小地址空间（4096 字节）逐地址穷举匹配与权限推导。
`tests/test_pmp_policy.cpp` 运行约 4800 组随机配置（覆盖全部
`MML × MMWP` 组合、随机 TOR/NA4/NAPOT 与锁定位），对每个地址逐字节比对
符号分析与参考模型的权限和命中索引，外加 16 种 `MML=1` 编码的定点真值表
测试、迁移规则、最小违例反例、原子区间最小性与 56 位冒烟测试，
合计约 **8852 万次断言零误差**。

## 文件布局

- `include/pmp/pmp_policy.hpp`：公开类型与接口。
- `src/pmp_policy.cpp`：解码器、原子分区、真值表、违例求解、迁移检查。
- `tests/reference_matcher.hpp`：12 位全地址穷举独立参考模型。
- `tests/test_pmp_policy.cpp`：单元测试与差分测试入口。
