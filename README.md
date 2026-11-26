# eBPF Scalar & Bounded-Pointer Verifier

可嵌入的 C++20 头文件与静态库，实现字节码加载器可直接调用的 eBPF 标量抽象域和有界指针越界验证核。组件不依赖内核 UAPI、LLVM 或任何第三方库。

## 核心能力

- `Tnum { value, mask }`：构造时强制 `(value & mask) == 0`，覆盖已知位与未知位。
- `Scalar`：同时维护 `tnum`、`[umin64, umax64]`、`[smin64, smax64]`、`[u32_min, u32_max]`、`[s32_min, s32_max]` 的交集语义。
- 双向不动点：64/32 位、有符号/无符号、`tnum` 之间互相收紧；无法满足全部分量时精确标记为空分支。
- ALU：64 位和 32 位 `add/sub/mul/div/mod/and/or/xor/lsh/rsh/arsh/neg/mov`；ALU32 总是清零高 32 位。
- 溢出：64/32 位无符号或有符号回绕可能发生时，受影响区间退化为该位宽全集，绝不生成过窄区间。
- 条件跳转：`JEQ/JNE/JLT/JGT/JLE/JGE/JSLT/JSGT` 及 32 位版本的双分支精化，`JEQ/JNE` 也支持寄存器对寄存器。
- DAG CFG：条件边拆分、路径 Join 取保守并集、不可达分支过滤；循环 CFG 会被明确拒绝。
- 指针：包、Map Value、栈区域内的 `ptr + scalar` / `ptr - scalar` 偏移范围；每个 load/store 都基于区域终点证明 `offset + size` 不越界。

## 构建

```bash
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build --output-on-failure
```

产物为 `libebpf_verifier.a`，公共头目录为 `include/`。CMake 目标名为 `ebpf::verifier`。

安装后可被其他 CMake 工程直接嵌入：

```cmake
find_package(ebpf_verifier 1 REQUIRED)
target_link_libraries(your_loader PRIVATE ebpf::verifier)
```

## 直接使用抽象域

```cpp
#include "ebpf/verifier.hpp"

using namespace ebpf;

Scalar offset = Scalar::from_unsigned_range(0, 1 << 20);
offset = alu64(AluOp::And, offset, Scalar::constant(0xff));

// offset.umax64 <= 255，高 56 位在 tnum 中变成已知 0。
auto split = refine_branch_constant(offset, CompareOp::Sgt, 200);
if (split.taken.reachable) {
    // taken.lhs 已收紧到 [201, 255]。
}

Scalar x = Scalar::constant(0x1'0000'0000ULL);
Scalar y = alu32(AluOp::Mov, x, Scalar::constant(0));
// y == 0，ALU32 已清零高 32 位。
```

`Scalar::normalize()` 是幂等的单调收紧过程；所有 ALU、分支和 Join API 返回前都会自动规范化。`Scalar::empty` 为真时，该路径不可达。

## 验证 8 字节 eBPF 指令

`Instruction` 的布局与经典 `struct bpf_insn` 一致（8 字节：`opcode`、低/高半字节分别为 `dst/src` 的寄存器字节、`off`、`imm`），因此加载器可直接把已有字节码按该布局传入。C++ API 通过 `dst()` / `src()` 读取寄存器号，构造器仍接受 `opcode,dst,src,off,imm`。

```cpp
#include "ebpf/verifier.hpp"

using namespace ebpf;

Program program;
program.packet_length = 64;
program.instructions = {
    {0x07, 1, 0, 0, 1},   // r1 += 1
    {0x3d, 1, 2, 1, 0},   // if r1 >= r2 goto exit
    {0x71, 0, 1, 0, 0},   // r0 = *(u8 *)(r1 + 0)
    {0x95, 0, 0, 0, 0},   // exit
};

VerificationResult result = verify(program);
if (!result.ok) {
    // result.pc 和 result.error 给出失败指令及原因。
}
```

默认初始寄存器：

- `r1`：`pkt_start` 指针，偏移 `[0, 0]`。
- `r2`：`pkt_end` 指针，偏移 `[packet_length, packet_length]`。
- `r10`：栈顶指针，偏移 `[stack_size, stack_size]`，支持 `r10 - N` 访问。
- 其余寄存器：标量全集。

需要自定义上下文、Map Value 或栈区域时，设置 `program.use_custom_initial_regs = true` 并填充 `program.initial_regs`。`Pointer::region_size` 是静态已知区域大小；`Pointer::offset_min/max` 是相对区域起点的偏移范围。

验证器要求 CFG 为有向无环图。每个可达路径最终必须到达 `EXIT`，否则拒绝。当前核不建模 helper 调用的类型语义，`CALL` 后会把 `r0` 与 `r1..r5` 放宽为未知标量，并保留 `r6..r9/r10`。

## 健全性约束

- `Tnum` 的加减使用逐位进位/借位状态传播，位级结果不会漏掉任何具体输入组合。
- 区间在可能回绕时退化为全集，而不是用端点差猜测结果。
- ALU32 先对两个源操作数做 32 位投影，再对结果零扩展；不会把高 32 位带入结果。
- 32 位比较只约束低 32 位，不会错误地把两个寄存器的高半部分设为相等。
- 分支空集由 `tnum` 加 64 位完整区间、低 32 位区间的精确位 DP 判定。
- 内存访问只有在最坏偏移仍落在 `[0, region_size]` 内时才通过。

## 测试

测试包含：

- 小位宽穷举和随机 `tnum` 运算差分。
- 随机 64 位、负值、符号边界和回绕边界的 ALU 健全性采样。
- 有符号/无符号混合分支推导、空分支检测和 32 位高位隔离。
- `r &= 0xFF`、ALU32 零扩展、DAG Join。
- 基于 `pkt_end` 的安全访问、差 1 字节越界、指针立即数减法逃逸和恶意字节码拦截。

## 范围限制

该组件聚焦标量抽象域和有界指针越界证明。它不包含完整 eBPF 类型系统、helper 原型校验、Map 创建语义、循环不变量或尾调用语义；这些应由上层加载器补充。`packet_length`、Map/栈区域大小必须是静态已知值，或者由上层先转换为区域大小。
