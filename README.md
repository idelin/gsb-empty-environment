# dwarf_cfi

可嵌入剖析器和零开销异常展开器的 C++20 DWARF v5 CFI 解码与帧表求值组件。

提供：

- 严格 ULEB128 / SLEB128 解码，正确处理负 SLEB128 的符号扩展。
- CIE / FDE 解析，支持 `.debug_frame`、`.eh_frame`、DWARF32/DWARF64、大小端输入。
- 完整 CFI 字节码状态机，包括 `DW_CFA_restore` 回退 CIE 初始规则和
  `DW_CFA_remember_state` / `DW_CFA_restore_state` 深拷贝状态栈。
- 按 PC 查询精确的 CFA 规则和寄存器恢复规则。
- `dwarf_cfi` 静态库和可嵌入公共头文件。

## 构建

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build --output-on-failure
```

CMake 目标：

- `dwarf::cfi`（别名）或 `dwarf_cfi`
- `dwarf_cfi_dump`：读取原始 `.eh_frame` / `.debug_frame` 文件并打印行表
- 公共头文件：`<dwarf/cfi.hpp>`，另有平铺入口 `<dwarf_cfi.hpp>`

`dwarf_cfi_dump` 用法示例：

```sh
objcopy -O binary --only-section=.eh_frame prog prog.eh
./build/dwarf_cfi_dump --base 0x2050 --kind eh --rows prog.eh
```

## 直接解析 CIE / FDE

```cpp
#include <dwarf/cfi.hpp>

using namespace dwarf::cfi;

std::span<const std::uint8_t> cie_bytes = /* one CIE entry */;
std::span<const std::uint8_t> fde_bytes = /* one FDE entry */;

Cie cie = parse_cie(cie_bytes);
Fde fde = parse_fde(fde_bytes, cie);

FrameTable table(cie, fde);
FrameRow row = table.lookup(pc);       // throws CfiError if pc is out of range

if (row.cfa.kind == CfaRuleKind::reg_offset) {
    // CFA = register row.cfa.reg + row.cfa.offset
}
if (const RegisterRule* saved = row.find_register(row.return_address_register)) {
    // saved->kind describes how to recover the return address
}
```

## 解析整个 section

```cpp
ParseOptions options;
options.kind = SectionKind::auto_detect;  // or debug_frame / eh_frame
options.address_size = 8;
options.section_address = section_virtual_address;  // for DW_EH_PE_pcrel

CfiSection section = parse_cfi_section(bytes, options);
FrameRow row = section.lookup(pc);  // selects the containing FDE
```

## 语义保证

- `CfiSection::find_fde(pc)` 使用半开区间
  `[initial_location, initial_location + address_range)`。
- `FrameTable::lookup(pc)` 在首个推进后位置大于 `pc` 的
  `DW_CFA_advance_loc*` / `DW_CFA_set_loc` 处停止，返回当前行的规则，
  不会执行后续字节码。
- `DW_CFA_restore` / `DW_CFA_restore_extended` 只回退到 CIE
  `initial_instructions` 执行完后的对应寄存器规则；如果 CIE 中没有该寄存器
  规则，则删除当前寄存器规则。它不会查看当前状态栈。
- `DW_CFA_remember_state` 保存 CFA 和完整寄存器规则表的深拷贝；
  `DW_CFA_restore_state` 在空栈时抛出 `state_stack_underflow`。
- 所有 `DW_CFA_offset*` / `DW_CFA_val_offset*` / `DW_CFA_def_cfa_sf` /
  `DW_CFA_def_cfa_offset_sf` 的偏移都按有符号 `data_alignment_factor` 缩放。
- `DW_CFA_advance_loc*` 的增量按 `code_alignment_factor` 缩放，并进行
  64 位溢出检查。
- LEB128 超过 64 位、超过 10 字节或截断输入均抛出 `CfiError`，错误码分别为
  `leb128_overflow`、`leb128_too_long`、`truncated_input`。

表达式操作码 `DW_CFA_def_cfa_expression`、`DW_CFA_expression`、
`DW_CFA_val_expression` 会被解码并作为原始表达式字节保存在规则中；本组件
不执行 DWARF 表达式求值。

## 主要类型

- `Cie`，别名 `CIE`：版本、augmentation、对齐因子、返回地址寄存器、
  初始指令、增强指针编码等。
- `Fde`，别名 `FDE`：CIE 引用、`initial_location`、`address_range`、指令字节。
- `CfaRule`：`undefined`、`reg_offset` 或 `expression`。
- `RegisterRule`：`undefined`、`same_value`、`offset`、`val_offset`、`reg`、
  `expression`、`val_expression`。
- `FrameRow`：行起始地址、CFA、寄存器规则表、返回地址寄存器编号。
- `FrameTable`：单个 FDE 的行求值器，支持 `lookup`、`try_lookup`、`rows`。
- `CfiSection`：一个 section 内的 CIE/FDE 集合与 PC 查询。

## 测试覆盖

`tests/test_cfi.cpp` 覆盖：

- 负 SLEB128、`INT64_MIN`/`INT64_MAX`、超 64 位移位、超长和截断输入；
- 多步函数序言压栈与行边界；
- 内嵌尾声中的 `remember_state` / `restore_state`；
- `DW_CFA_restore` / `restore_extended` 回退 CIE 初值而非状态栈顶；
- 负 `data_alignment_factor` 与 `DW_CFA_offset_extended_sf` 等有符号偏移；
- 空状态栈弹出、非法 LEB128、未知操作码拒绝；
- `.debug_frame`、`.eh_frame`、DWARF64、大小端、pcrel FDE 地址；
- `DW_CFA_advance_loc1` / `2` / `4`、`DW_CFA_set_loc` 和 PC 范围校验。
