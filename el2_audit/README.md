# ARM64 EL2 超管配置与 MMIO 模拟边界审计库

可嵌入、零外部依赖的 C++17 静态/访问审计库，用于审计 ARM64 Type-1 EL2
超管在多客体虚拟机隔离场景下的配置与设备模拟安全性。仅依赖 C++ 标准库，
使用 `clang++ -std=c++17 -Wall -Wextra -Wconversion -Werror` 零警告编译。

## 威胁模型覆盖

1. `HCR_EL2` 漏设 `VM=1`（Stage-2 未启用）或漏设 `TSC/TWE/TVM/TACR`
   等关键陷阱位，导致客体直接操作敏感系统寄存器。
2. 多台虚拟机复用同一非零 `VMID`（`VTTBR_EL2[63:48]`），引发 TLB
   跨机别名与缓存污染；`VMID=0` 同样被拒绝。
3. Stage-2 映射重叠超管 EL2 自身物理内存、他机物理内存，或给本应陷入
   EL2 模拟的 MMIO 窗口建立了直通映射。
4. MMIO 模拟未校验 `IPA + access_size`：跨设备窗口、非自然对齐、跨寄存器
   边界、写只读/保留寄存器等访问被硬拦截并输出见证。

## 目录结构

- `include/el2_audit/el2_audit.h`：寄存器位定义、数据模型与公共 API。
- `src/el2_audit.cc`：全部审计逻辑，无第三方依赖。
- `tests/test_el2_audit.cc`：自带轻量测试框架与必测攻击场景。
- `Makefile` / `CMakeLists.txt`：严格编译与测试入口。

## 核心数据模型

- `VmConfig`：单台虚拟机的 `HCR_EL2`/`VTTBR_EL2`/`VTCR_EL2`、稀疏
  Stage-2 叶子映射（`S2Mapping`，与页粒度/块大小解耦）和设备窗口表。
- `ProtectedRange`：超管 EL2 私有物理内存区间，任何客体都不得映射。
- `MmioRegion` / `MmioRegister`：虚拟设备寄存器窗口及寄存器粒度
  （`RW`/`ReadOnly`/`WriteOnly`/`Reserved`）。
- `MmioAccess`：客体触发的 `ESR_EL2` 与故障 `IPA` 序列项。
- `Finding`：违规码、严重级、虚拟机 ID、人类可读描述与十六进制见证。

## 审计流程

`Auditor::audit_static_configuration()` 执行检查（1）（2）：

- 逐位核对策略要求的 `HCR_EL2` 陷阱位（默认含 `VM/TSC/TWE/TVM/TACR`
  及缓存/TLB 维护与实现定义寄存器陷阱位），每个缺失位独立出报告。
- 提取并比对全部 `VMID`，拒绝零值与跨域复用。
- 校验 Stage-2 描述符合法性、域内 IPA 重叠、EL2 保护区间重叠、跨机 PA
  重叠，以及 MMIO 窗口是否被 Stage-2 直通映射。

`Auditor::audit_mmio_access()` / `audit_mmio_sequence()` 执行检查（3）：

- 解析 `ESR_EL2`：要求 EC 为下层 Data Abort 且 `ISS.ISV=1`，再解码
  `SAS`（1/2/4/8 字节）、`WNR`（读写方向）与 `SRT`。
- 设备窗口归属 → `IPA + size` 窗口硬边界（含 64 位回绕防护）→ 自然对齐
  → 寄存器粒度边界 → 只读/保留写策略，顺序固定且每类违规独立编码。
- 所有边界运算采用回绕安全形式，避免无符号减法下溢导致的绕过。

## 构建与测试

```sh
make test
# 或
cmake -S . -B build && cmake --build build && ctest --test-dir build
```

手工严格编译：

```sh
clang++ -std=c++17 -Wall -Wextra -Wconversion -Werror -Wpedantic \
  -Iinclude src/el2_audit.cc tests/test_el2_audit.cc -o el2_audit_tests
```

## 测试场景

- `vmid_collision_blocks_tlb_alias`：复用非零 VMID 被拦截。
- `hcr_vm_and_trap_bits_missing_are_reported`：漏设 `VM` 与 `TSC` 等
  陷阱位逐位报告，零 VMID 被拒绝。
- `mmio_passthrough_and_physical_isolation_are_blocked`：MMIO 直通泄漏、
  EL2 物理内存映射、跨机 PA 重叠均被拦截。
- `cross_boundary_unaligned_and_illegal_writes_are_hard_blocked`：跨边界
  8 字节写、非对齐、跨寄存器、只读/保留写、窗口外访问与畸形 ESR。
- `pure_helpers_and_witness_text`：区间重叠、ESR 解码与见证文本。
