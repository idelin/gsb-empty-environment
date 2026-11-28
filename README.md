# PE32+ Delay-Load IAT / `.reloc` 安全审计库

一个可嵌入 C++17 项目的原始字节级 PE32+ 审计库。它不依赖 Windows SDK、
PE 解析库、异常处理或动态内存映射；输入文件只被当作不可信的只读字节流。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

库目标为 `pe_audit`（别名 `pe_audit::pe_audit`），公开头文件为
`include/pe_audit/pe_audit.h`。所有库、测试和 CLI 目标均使用：

```text
-Wall -Wextra -Wconversion -Werror
```

## 嵌入 API

```cpp
#include "pe_audit/pe_audit.h"

pe_audit::Report report = pe_audit::audit_file("sample.exe");
if (!report.ok()) {
    for (const pe_audit::Finding& finding : report.findings) {
        // finding.code / finding.severity / finding.rva / finding.detail
    }
}
```

也可直接审计内存：

```cpp
pe_audit::Report report =
    pe_audit::audit_bytes(buffer.data(), buffer.size());
```

`AuditOptions::additional_allowed_dlls` 用于追加 DLL 白名单项，名称比较不区分
大小写。内置白名单覆盖常见的 Windows 系统 DLL；未列出的合法业务 DLL 应通过
此选项显式加入。

CLI：

```sh
./build/pe_audit_cli [--allow-dll extra.dll] sample.dll
```

退出码 `0` 表示无 Error，`1` 表示审计失败，`2` 表示参数错误。

## PE32+ 基础约束

- 校验 `MZ`、`e_lfanew`、`PE\0\0`、COFF 节数量/可选头长度、PE32+ magic
  (`0x20B`) 和 16 项数据目录的边界。
- 校验 `SizeOfHeaders`、`SizeOfImage`、节表范围和每个节的虚拟/文件范围；
  节虚拟范围不得重叠，普通内容必须完整位于文件中。
- 数据目录必须完整位于单个已映射、文件支持的节内，不能落入 PE 头。
- 所有偏移、长度和 RVA 运算均在 64 位无符号空间中先检查，再向下转换，
  避免 32 位回绕。

## `.reloc` 审计

读取 `IMAGE_DIRECTORY_ENTRY_BASERELOC`，逐块检查：

- 拒绝 `SizeOfBlock < 8`、块大小或目录 RVA/大小非 4 字节对齐、块越出目录、
  尾部不足块头、块长度回绕和零长度导致的死循环。
- 用显式小端读取取得每项高 4 位类型和低 12 位页内偏移，不用未对齐结构体
  强转。
- 接受无写入的 `IMAGE_REL_BASED_ABSOLUTE` 和通用固定宽度类型
  `HIGH`/`LOW`/`HIGHLOW`/`DIR64`；拒绝 `HIGHADJ`、架构专用拆分重定位
  及保留类型。`DIR64` 写入宽度为 8。
- 每个写区间必须完整落在一个已映射节内，不得触及 PE 头、普通导入目录或
  延迟导入描述符目录。
- 所有实际写区间按起始 RVA 排序，严格检测重叠（包括相同目标）。
- 记录通过校验的重定位项，包含块 RVA、类型、页内偏移、目标 RVA 和宽度。

## Delay-Load 审计

读取 `IMAGE_DIRECTORY_ENTRY_DELAY_IMPORT` 中的 `ImgDelayDescr` 数组，直到全零
终止项，并检查：

- `Size` 必须是 32 字节描述符的整数倍，必须有终止项。
- `grAttrs & 1` 选择 RVA 模式；旧式 VA 模式以 preferred `ImageBase` 换算，
  下溢、超过 32 位或换算为零均拒绝。
- `szName`、`pIAT`、`pINT` 必须存在并落在已映射节中；可选
  `pBoundIAT`/`pUnloadIAT` 非零时同样校验。IAT/INT 起点还必须能容纳一个
  PE32+ 指针槽。
- `szName` 必须在同一节的文件数据内遇到 NUL；字符串不能越界。
- DLL 名必须是不带目录/驱动器/`..` 的裸 `.dll` 名，并位于内置或追加的
  白名单中。
- 读取 x64 Load Config 的 `GuardFlags`（结构偏移 144，要求 load-config
  `Size >= 148`），检查：
  - `IMAGE_GUARD_CF_INSTRUMENTED` (`0x00000100`)；
  - `IMAGE_GUARD_PROTECT_DELAYLOAD_IAT` (`0x00001000`)。

只要存在有效延迟导入但缺少 CF 位即报 Error。若 Delay-Load IAT 所在节具有
`IMAGE_SCN_MEM_WRITE`，且缺少 IAT 保护位，则额外报告可写 IAT 劫持风险。

## 手工测试样本

`tests/pe_audit_tests.cpp` 不依赖任何文件或 SDK，由字节写入器手工构造完整
PE32+：

- 合法样本含 4 个节、一个 RVA 模式 Delay-Load 描述符、受保护的 `.data`
  IAT、一个 `DIR64` 重定位，以及同时设置 CF/IAT 保护位的 Load Config。
- 旧式 VA 描述符及其下溢拒绝。
- `.reloc` 越界目标、交叉节写、重叠写、写 PE 头、写导入目录。
- `SizeOfBlock == 0` 死循环输入、非 4 字节对齐、越出目录、目录整数回绕。
- 高风险重定位类型。
- 可写 Delay-Load IAT 缺少保护、缺少 CF、完全缺失 Load Config。
- 相对路径/非白名单 DLL、白名单扩展、未终止字符串、非法 `pBoundIAT`。
- 2000 次确定性单字节变异，验证解析在畸形输入上始终终止。

测试同时在 ASan/UBSan 配置下运行通过。

## 安全边界

该库是静态结构审计器，不加载、不重定位、不解析或执行输入中的代码，也不
解析 DLL 导出符号或校验数字签名。它验证 PE 元数据和写目标的结构安全，
不能替代操作系统加载器的实际重定位行为或代码签名策略。
