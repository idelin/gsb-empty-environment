# elfsec — ELF64 动态装载安全验证库

面向安全容器与二进制准入网关的零依赖 C++17 库。它直接解析宿主持有的原始
字节缓冲，仅依据 **ELF 头 + 程序头表** 做出全部安全判定，从不读取节头表，
因此伪造/篡改节头名（`.got`、`.init_array`、`.dynamic` 等）无法影响结论。

## 威胁模型

攻击者控制待审核的 ELF64 `ET_EXEC` / `ET_DYN` 镜像，可能通过以下手法在装载期
篡改控制流，验证器对每一项都给出结构化违规码：

- 畸形 ELF/程序头偏移、加法回绕、越界读；
- `PT_LOAD` 虚拟页重叠、`p_filesz > p_memsz`、`p_vaddr/p_offset` 不对齐；
- 未页对齐或缺失的 `PT_GNU_RELRO`，残留可写 `.got` / `.init_array`；
- 未启用立即绑定（Partial RELRO / lazy binding）；
- 可执行栈、同时 `PF_W|PF_X` 的装载段；
- 含相对路径、空项或 `$ORIGIN` 展开的 `DT_RPATH` / `DT_RUNPATH`；
- 隐蔽 `R_X86_64_IRELATIVE` 解析器劫持、未在白名单内的 `STT_GNU_IFUNC`；
- `DT_VERSYM` 引用了 `DT_VERNEED` 中不存在的版本索引的符号覆盖。

## 解析范围

- 程序头：`PT_LOAD`、`PT_DYNAMIC`、`PT_GNU_RELRO`、`PT_GNU_STACK`、`PT_INTERP`。
- 动态条目：`DT_STRTAB`、`DT_SYMTAB`、`DT_RELA`、`DT_JMPREL`、`DT_BIND_NOW`、
  `DT_FLAGS`、`DT_FLAGS_1`、`DT_INIT_ARRAY`、`DT_FINI_ARRAY`（及其 `*SZ`）、
  `DT_RPATH`、`DT_RUNPATH`、`DT_VERNEED`(+`DT_VERNEEDNUM`)、`DT_VERSYM`。
- 重定位：x86-64 与 AArch64 的 `GLOB_DAT` / `JUMP_SLOT` / `RELATIVE` /
  `IRELATIVE`。

所有读取都通过“虚拟地址 → 文件偏移”的精确映射进行：仅 `PT_LOAD` 的
文件支撑区（`[p_vaddr, p_vaddr+p_filesz)`）可读，BSS 与任何未映射地址的
访问都会被拒绝；所有地址/尺寸运算使用编译器内建溢出检查，杜绝加法回绕。

## Full RELRO 判定

1. 必须存在页对齐且非空的 `PT_GNU_RELRO`；
2. 必须启用立即绑定：`DT_BIND_NOW`，或 `DT_FLAGS` 含 `DF_BIND_NOW`，
   或 `DT_FLAGS_1` 含 `DF_1_NOW`；
3. 以目标页大小对 RELRO 区间向下/向上取整后，只读覆盖区必须完整包裹：
   - `PT_DYNAMIC`（`.dynamic`）；
   - `DT_INIT_ARRAY` / `DT_FINI_ARRAY` 全区间；
   - 全部受管重定位的写入目标（每个 8 字节 GOT 槽）。

## 公共 API

```cpp
#include "elfsec/verify.h"

elfsec::VerifyOptions options;
options.page_size = 4096;
options.ifunc_whitelist = {"trusted_ifunc"};

elfsec::VerifyResult result = elfsec::verify_elf64(byte_buffer, options);
if (!result.ok) {
  for (const auto& v : result.violations) {
    // elfsec::violation_name(v.code) + v.message
  }
}
```

`ViolationCode` 分为结构性致命错误（解析立即终止，后续结论不可信）与策略违规
（累计返回）。`VerifyResult::first_fatal()` 可区分二者，`has(code)` 用于断言
具体检测项。

## 构建与测试

要求 C++17，编译选项固定 `-Wall -Wextra -Wconversion -Werror`，零外部库依赖。

```sh
cmake -S . -B build -G "Unix Makefiles" -DCMAKE_BUILD_TYPE=Release
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

目标：

- `elfsec`：静态验证库；
- `elfsec_cli`：审核宿主示例，读取文件并打印 `ADMIT` / `REJECT` 结论；
- `elfsec_tests`：字节级构造样本的检测矩阵测试；
- `elfsec_dump_samples`：把测试样本落盘，供 CLI 端到端演示；
- `elfsec_edge_fuzz`：随机/截断缓冲不崩溃的健壮性测试。

## 检测矩阵（字节级合成样本）

| 样本 | 期望违规码 |
| --- | --- |
| 合法 Full-RELRO | 无（ADMIT） |
| Partial-RELRO（无立即绑定） | `NO_IMMEDIATE_BINDING` |
| 缺失 `PT_GNU_RELRO` | `RELRO_SEGMENT_MISSING` + 覆盖缺口 |
| RELRO 页偏移错位 | `RELRO_NOT_PAGE_ALIGNED` |
| 重定位目标落在 RELRO 外 | `RELRO_GAP_RELOCATION_TARGET` |
| 重叠 `PT_LOAD` | `LOAD_VIRTUAL_PAGE_OVERLAP`（致命） |
| `p_filesz > p_memsz` | `LOAD_FILESZ_GREATER_MEMSZ`（致命） |
| 可执行栈 | `EXECUTABLE_STACK` |
| `PF_W|PF_X` 装载段 | `WRITABLE_EXECUTABLE_SEGMENT` |
| 相对/空 `DT_RPATH`、`DT_RUNPATH` | `RPATH_RELATIVE_OR_EMPTY` 等 |
| 版本索引未在 `DT_VERNEED` 绑定 | `SYMBOL_VERSION_UNBOUND` |
| 隐蔽 IRELATIVE / 未白名单 IFUNC | `UNWHITELISTED_IFUNC_RESOLVER` |
| 伪造节头表偏移 | 忽略，结论不变 |
| 程序头表越界 | `PROGRAM_HEADER_TABLE_OOB`（致命） |

> 说明：环境自带的 `/usr/bin/ar` 在无 GUI 会话时是失效 shim。若静态归档阶段
> 报 xcode-select 错误，向 CMake 显式传入实体工具链即可：
> `-DCMAKE_AR=$(xcrun -f ar 2>/dev/null || echo /Library/Developer/CommandLineTools/usr/bin/ar)`
> 及对应的 `CMAKE_RANLIB`。
