# ELF64 动态安全加固审计库

一个可嵌入的 C++17 库，用原始字节解析 Linux x86-64 ELF 镜像并执行动态加载安全准入检查。实现不依赖系统 `elf.h`、ELF 解析库或其他第三方组件，所有偏移和大小均在显式溢出检查后读取。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

库目标和测试均使用 `-Wall -Wextra -Wconversion -Werror`。测试使用内存中手工构造的 ELF64 镜像，不依赖外部二进制。

## 嵌入 API

```cpp
#include "elf64_audit/elf64_audit.hpp"

elf64_audit::AuditReport report =
    elf64_audit::audit_elf64_file("/usr/libexec/privileged-helper");

if (!report.admission_passed) {
    for (const auto& finding : report.findings) {
        // finding.code / finding.message / finding.virtual_address
    }
}
```

也支持直接审计内存缓冲区：

```cpp
elf64_audit::AuditReport report =
    elf64_audit::audit_elf64(bytes, byte_count);
```

`AuditOptions::page_size` 默认 4096 字节，必须是大于等于 4096 的 2 的幂。`AuditReport::parsed` 表示结构解析是否成功，`admission_passed` 只有在解析成功且不存在 `Severity::Error` 级发现时才为真。`relro` 明确区分 `None`、`Partial` 和 `Full`。

命令行检查器：

```sh
./elf64-audit /path/to/helper
```

退出码 `0` 表示准入通过，`1` 表示存在安全阻断项，`2` 表示参数错误或 ELF 结构不可解析。

## 检查范围

- ELF64 头、扩展 section/program 计数、全部程序头和全部节头。
- `PT_LOAD` 文件/内存边界、页对齐关系、`p_filesz <= p_memsz`，以及按页范围冲突的异权限重叠。
- `PT_DYNAMIC` 边界、`DT_*` 表、字符串表、符号表、`Elf64_Rela` 表及动态重定位指针。
- `PT_GNU_RELRO` 的页对齐区间，逐项验证 `.dynamic`、`.got`、`.got.plt`、`.init_array`、`.fini_array` 是否被完整覆盖。
- `DT_BIND_NOW`、`DT_FLAGS & DF_BIND_NOW`、`DT_FLAGS_1 & DF_1_NOW` 三者任一成立才视为立即绑定。
- `DT_RPATH` / `DT_RUNPATH` 中的空项、相对路径、逃逸 `$ORIGIN` 的 `..` 穿越。
- `PT_GNU_STACK` 的存在性及其可执行位；缺失栈策略元数据按阻断处理。
- 全部 `R_X86_64_IRELATIVE` 和 `STT_GNU_IFUNC` 解析器地址必须位于 `PF_R|PF_X`、且不含 `PF_W` 的 `PT_LOAD` 内。

以下情况会直接判为不可解析：越界文件偏移、非 NOBITS 节越界、算术加/乘回绕、动态/重定位/符号表尺寸非法、重定位目标不在任何 `PT_LOAD` 中，以及无法唯一映射到文件后备 `PT_LOAD` 的动态指针。

## 测试样本

`tests/test_elf64_audit.cpp` 内含完整 ELF 构造器，覆盖：

- 三页布局下 `PT_GNU_RELRO` 跨页覆盖完整 GOT/数组的合法 Full RELRO。
- RELRO 仅覆盖首屏、`.got` 位于下一屏的伪 Full RELRO。
- 空 `DT_RPATH` 项、相对 RUNPATH、逃逸 `$ORIGIN/../..`、受控 `$ORIGIN/plugins`。
- 三种 BIND_NOW 编码、可执行栈、可写 `STT_GNU_IFUNC` 与可写 `R_X86_64_IRELATIVE` 解析器。
- 截断、越界偏移、虚拟地址回绕和异权限 `PT_LOAD` 重叠。
