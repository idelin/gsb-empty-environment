# machoaudit — 原始字节级 Mach-O 64 位代码签名与权限审计库

面向终端准入网关等离线核验场景的可嵌入 C++17 静态库。仅依赖 C++ 标准库，
内置 FIPS 180-4 SHA-256，不调用任何系统签名 API，从原始字节逐层重算并核对
`LC_CODE_SIGNATURE` 完整性，避免“只看签名 Load Command 是否存在”的典型绕过。

## 威胁模型与核验内容

针对三类攻击面做硬核验，任一不满足即返回非 `Ok` 的机器可读错误码：

1. **结构畸形**：越界偏移、整数溢出、`LC_SEGMENT_64` 文件范围重叠、
   `LC_CODE_SIGNATURE` 未落在 `__LINKEDIT` 合法尾部、镜像尾部存在未被任何段
   覆盖的“藏毒”字节、SuperBlob/子 Blob 越界或重叠。
2. **代码页完整性**：解析大端序 `CS_SuperBlob`（`0xfade0cc0`）与
   `CS_CodeDirectory`（`0xfade0c02`），对 `[0, codeLimit)` 内全部
   `2^pageSize` 页（含末尾零填充的部分页）重算 SHA-256，与 `nCodeSlots`
   槽位做恒定时间比对；`codeLimit` 必须严格等于 `LC_CODE_SIGNATURE` 起始
   偏移，且完整覆盖所有带执行权限的段。
3. **特殊槽位与权限**：核对特殊槽位 `-2` Requirements、`-5` XML
   Entitlements、`-7` DER Entitlements 的摘要与对应子 Blob 原始字节一致；
   要求 CodeDirectory 同时置位 `CS_KILL`（`0x200`）与 `CS_RUNTIME`
   （`0x10000`，Hardened Runtime）；拦截显式授予的
   `com.apple.security.get-task-allow`、
   `com.apple.security.cs.allow-unsigned-executable-memory`、
   `com.apple.security.cs.disable-library-validation`。

## 目录结构

- `include/machoaudit/audit.hpp` — 审计入口、`AuditCode` 错误码、`AuditResult`。
- `include/machoaudit/sha256.hpp` / `src/sha256.cpp` — 零依赖 SHA-256。
- `src/audit.cpp` — Mach-O / SuperBlob / CodeDirectory 严格解析与核验。
- `tests/macho_builder.hpp` — 手工字节级 Mach-O 样本与签名构造器（无系统工具）。
- `tests/test_main.cpp` — KAT 与 48 项畸形/篡改/权限断言。

## 集成方式

```cpp
#include "machoaudit/audit.hpp"

machoaudit::AuditResult r = machoaudit::auditMachO(bytes /*, size*/);
if (!r.ok()) {
    // r.code 为机器可读错误码，r.detail 为人类可读说明
    reject(machoaudit::auditCodeName(r.code), r.detail);
}
```

解析过程对每个偏移做边界检查，畸形输入只返回错误码、不抛异常、不发生越界读。
SHA-256 摘比对采用恒定时间比较，避免基于摘要差异的时序侧信道。

## 构建与测试

要求 C++17，编译选项统一为 `-Wall -Wextra -Wconversion -Werror`
（额外验证 `-Wsign-conversion -Wshadow -Wpedantic` 亦零警告）。

```sh
cmake -S . -B build-cmake
cmake --build build-cmake
ctest --test-dir build-cmake --output-on-failure
```

无 CMake 时可直接编译：

```sh
c++ -std=c++17 -Wall -Wextra -Wconversion -Werror -Iinclude -Itests \
    src/sha256.cpp src/audit.cpp tests/test_main.cpp -o audit_tests
./audit_tests
```

## 测试覆盖

- SHA-256 四个标准 KAT（空串、`abc`、56 字节、百万 `a`）与逐字节流式一致性。
- 手工构造的合法 `MH_EXECUTE` / `MH_DYLIB` 样本（真实页哈希、特殊槽位摘要）。
- 页篡改（整页与部分页）、特殊槽位摘要/Blob 字节错配。
- 缺失 `CS_RUNTIME` / `CS_KILL`、三个高危权限在 XML 与 DER 载体下的注入，
  以及高危 key 显式 `<false/>` 不误报。
- `codeLimit` 截断、`nCodeSlots`/页大小伪造、尾部藏毒、段重叠/越界、
  签名非 LINKEDIT 尾部、SuperBlob/CodeDirectory magic 与长度破坏、
  非 64 位 cputype、不支持文件类型、命令区整数溢出等。
