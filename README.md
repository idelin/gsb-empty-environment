# `.eh_frame` 安全验证库

面向 C++17 二进制安全宿主的可嵌入 `.eh_frame` 审计库。它只依赖 C++ 标准库，
默认失败关闭：给定不可信 section 字节、当前帧栈窗口和 CFA 基址寄存器值后，
逐条验证 CIE/FDE 与 CFA 状态机；任何越界、回绕、非法寄存器、危险 RA 规则或
不受限的 DWARF 表达式都会使整段数据被拒绝。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

库目标为 `ehframe::validator`。构建使用 C++17，并对库和测试启用
`-Wall -Wextra -Wconversion -Werror`（MSVC 下为 `/W4 /WX`）。

## API

```cpp
#include "ehframe/validator.hpp"

ehframe::ValidationOptions options =
    ehframe::ValidationOptions::for_frame(
        stack_low, stack_high, current_stack_pointer, 7 /* x86-64 rsp */);
options.address_size = 8;
options.section_address = runtime_address_of_eh_frame;
options.expected_return_address_register = 16;  // x86-64 rip
options.add_register_value(6, current_rbp);     // 如果 CFI 切换到 rbp

const ehframe::ValidationResult result =
    ehframe::validate(section_data, section_size, options);
if (!result) {
    // result.error.code / offset / message
}
```

`FrameBounds` 是绝对虚拟地址的半开区间 `[low, high)`。`stack_pointer` 是执行
当前 FDE 行时栈指针寄存器的值；若 FDE 把 CFA 基址切换到其他寄存器，必须通过
`add_register_value()` 提供对应值，否则验证失败。验证器不会自行猜测运行时栈或
寄存器状态，缺失上下文属于配置错误。

## 验证策略

- 记录层：检查长度字段、扩展长度、记录边界、CIE/FDE 结构、ULEB128/SLEB128
  规范性、CIE 指针必须向后且精确指向已解析 CIE，以及 FDE 地址区间回绕和重叠。
- 寄存器层：限制所有 DWARF 寄存器号，并按 CIE 初始规则执行完整 CFA 状态机，
  支持 `def_cfa*`、`offset*`、`restore*`、`remember_state`、`restore_state`、
  `advance_loc*`、`set_loc`、`val_offset*` 等指令。
- 状态栈：硬限制深度；空栈 `restore_state` 和尾随未匹配状态直接拒绝。
- RA 策略：返回地址只能使用有界 `DW_CFA_offset` 规则。任意表达式、常量式
  `val_offset`、`register`、`same_value`、`undefined` 及栈外地址均拒绝。
- 表达式层：仅允许无控制流、无内存解引用的线性白名单（常量、`DW_OP_breg*`、
  `DW_OP_call_frame_cfa`、加减、位运算、栈操作等），限制步骤数和栈深度，
  并检查 `def_cfa_expression`/`DW_CFA_expression` 的结果位于当前帧栈窗口内。
- 资源层：限制记录数、CFI 指令数、表达式步骤数、表达式栈深和状态快照深度；
  分配失败也会返回失败结果而不是抛出宿主异常。

`DW_OP_deref`、`DW_OP_xderef`、`DW_OP_deref_size`、`DW_OP_xderef_size` 及
typed dereference 扩展被明确禁止；其他未知或未列入白名单的 `DW_OP_*` 也一律
拒绝。`DW_EH_PE_indirect` 形式的 FDE 起始地址同样被拒绝，因为解析它需要读取
进程内存。

## 测试样本

`tests/test_validator.cpp` 覆盖：

- 正常 FDE 状态机：多次 `advance_loc`、`set_loc`、CFA 寄存器切换、offset
  restore、remember/restore；
- 安全的 `DW_CFA_def_cfa_expression`；
- `DW_OP_deref` 经 `DW_CFA_val_expression` 劫持 RA；
- 重叠 FDE、`restore_state` 空栈、状态栈溢出；
- 越界记录、FDE 区间回绕、RA 常量规则、RA 栈外偏移；
- CIE 指针指向非 CIE、截断和非规范 LEB128、表达式栈深度超限。

## 集成注意

本库是静态审计组件，不执行 `dlopen`，也不替代运行时栈展开器。宿主仍应在真正
展开前把实际寄存器值、栈边界和 section 装载地址传入；对于无法静态证明安全的
架构扩展指令（例如 AArch64 RA 状态切换），库会按未知指令拒绝。
