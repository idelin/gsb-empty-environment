# debugloc — 调试位置表达式求值库

C++17 实现的栈式表达式求值器，用于调试器位置描述（类 DWARF location
expression）：从寄存器和内存中读取值、做算术/位运算、拼接位段，最终得到
一个**带未知位（unknown bits）**的结果。

## 核心概念

- `BitValue`（`include/debugloc/value.hpp`）：定宽（≤64 位）三态位值。
  `known` 掩码逐位标记该位是否已知；**缺失内存产生未知位，绝不静默填零**。
- `Op` / 程序（`include/debugloc/evaluator.hpp`）：指令序列，含
  `Lit/Reg/Deref/DerefAt`、算术（`Add/Sub/Mul`，全已知时做溢出检查）、
  位运算（`And/Or/Xor/Not/Shl/Shr`，三态传播）、比较（`Eq/Lt`）、
  栈操作（`Dup/Drop/Swap`）、位段（`Piece`）与拼接（`Concat`）。
- `Evaluator`：带预算的求值器。每条指令消耗 1 点预算，每读一个内存字节
  再消耗 1 点；预算耗尽返回 `Error::BudgetExhausted`。

## 语义要点

- **地址运算溢出检查**：`Deref`/`DerefAt` 检查 `addr + size` 是否回绕
  64 位地址空间；全已知操作数的 `Add/Sub/Mul` 回绕时报
  `Error::ArithmeticOverflow`。
- **缺失内存 = 部分未知**：逐字节读取，读不到的字节对应 8 个未知位，
  已知字节不受影响（不是零，也不是整体失败）。
- **未知传播**：`And/Or/Xor` 逐位三态求值（如 `0 & x = 0`）；加减乘在
  最低未知位以下保持精确，以上全部未知；移位同步移动已知掩码。
- **位段 `Piece(size, offset)`**：小端目标 offset 从 LSB 计，大端目标
  从 MSB 计（DWARF `DW_OP_bit_piece` 规则），支持不对齐（非字节倍数）
  的位段。`Concat` 把栈顶作为低位段拼接。
- **栈下溢**：空栈弹栈或程序结束时栈为空，返回 `Error::StackUnderflow`。

## 构建与测试

```sh
cmake -S . -B build && cmake --build build
ctest --test-dir build --output-on-failure
```

测试（`tests/test_evaluator.cpp`）覆盖：不对齐位段、大小端切换（内存
解释与 `Piece` offset 基准）、表达式栈下溢、预算耗尽、地址/算术溢出、
缺失内存的部分未知传播，以及一个"寄存器基址 + 偏移 → 解引用 → 位段
拼接"的组合程序。
