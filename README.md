# Nested EPT Composition & Symbolic Isolation Verifier

零外部依赖、可直接嵌入的 C++17 4 级嵌套 EPT 精确合成与安全验证库。核心实现位于
[`ept_nested.hpp`](ept_nested.hpp)，测试位于
[`tests/ept_nested_tests.cpp`](tests/ept_nested_tests.cpp)。

## 能力

- 48 位物理地址、4 级 EPT（PML4/PDPT/PD/PT）。
- 4KB、2MB、1GB 叶项；几何参数可缩放。
- MBEC 权限：`R`、`W`、`X_supervisor`、`X_user` 独立合成。
- 原始 64 位表项建模，保留位、非法 `W=1,R=0`、非法内存类型、超页对齐和不支持的
  PS 层级均判定为 `EPT Misconfiguration`。
- EPT12 区间到 L1_GPA 后，在 EPT01 上做区间求交；遇到 1GB/2MB/4KB 边界自动切分。
- EPT02 权限逐位交集：
  `R=R01&R12`、`W=W01&W12`、`Xs=Xs01&Xs12`、`Xu=Xu01&Xu12`。
- 对整个 `[0,2^48)` 地址空间做有序区间扫描和符号差集验证，不按 4KB 页循环。
- 输出最低违例 `L2_GPA`、违例类型、权限差集、HPA 差集，以及 EPT12、EPT01、EPT02
  各级原始表项见证。
- 提供 16 位缩微 4 级几何，确定性穷举全部 256 个缩微页和全部 65536 个字节地址，
  并与独立逐页参考判定交叉核对最低违例地址。

## 构建与测试

```sh
c++ -std=c++17 -Wall -Wextra -Wpedantic -Wconversion -Wsign-conversion \
  -Werror -I. tests/ept_nested_tests.cpp -o ept_nested_tests
./ept_nested_tests
```

或使用 CMake/CTest：

```sh
cmake -S . -B build -DCMAKE_CXX_COMPILER=clang++
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

当前测试输出包含：

```text
16-bit exhaustive: 80 cases, 20480 page-checks, 20480 point-checks,
5242880 byte-checks, zero-error=1
All EPT nested composition checks passed.
```

## 快速用法

```cpp
#include "ept_nested.hpp"
using namespace eptn;

EptPageTable ept01(Geometry::x86_48());
EptPageTable ept12(Geometry::x86_48());

ept01.map(0x1000, 0x2000,
          Permissions{true, true, true, false}, PageKind::Small);
ept12.map(0x3000, 0x1000,
          Permissions{true, true, false, true}, PageKind::Small);

ComposeResult composed = composeExact(ept01, ept12);
VerificationReport report = verifyCandidate(
    ept01, ept12, composed.ept02,
    VerifyOptions::exactComposition());

if (!report.clean()) {
    std::printf("lowest bad L2_GPA=%s\n%s\n",
                hex(report.lowest_gpa).c_str(),
                formatWitnessBundle(report.lowest_witness).c_str());
}
```

审计任意合成器可使用模板入口：

```cpp
VerificationReport report = verifyComposer(
    ept01, ept12,
    [](const EptPageTable& a, const EptPageTable& b) {
        return composeExact(a, b).ept02;
    },
    VerifyOptions::exactComposition());
```

自定义 EPT02 表示可实现 `ICandidateEpt02`，提供符号区间 `flatten()` 和地址行走
`walk()` 后直接交给 `verifyCandidate()`。

## 符号验证模型

`EptPageTable::flatten()` 将存活表项展开为按地址排序的最大语义区间：

```text
[lo, hi) -> { Mapped(hpa_base, perms) | Unmapped | Misconfigured(reason) }
```

`composeSemantics()` 先展开 EPT12，再把每个连续的 L1_GPA 输出区间与 EPT01 区间求交。
交集边界来自 1GB/2MB/4KB 或错误配置表项，因此大页会在精确边界处被切开，而不会把
粗粒度权限扩展到相邻 HPA。

验证器同步扫描期望区间和待审 EPT02 区间，在每个符号片段上检查：

- 未授权映射或 HPA 不一致；
- `actual_perms & ~expected_perms != 0` 的权限放大；
- exact 模式下的权限降级、缺失映射和意外映射；
- EPT01/EPT12/EPT02 的 Misconfiguration；
- 最低违例地址及完整见证。

复杂度与存活表项/符号区间数量相关，而不是与 `2^48 / 4096` 个地址相关。

## 安全边界

- security 模式把权限放大、错误 HPA 和可访问的未授权映射视为安全突破。
- exact 模式还要求映射存在、HPA 相同、四位权限完全相等且无错误配置表项。
- 源 EPT01/EPT12 的保留位错误通过 `SourceMisconfiguration` 和 L2 见证报告；精确合成器
  不会把这些区间静默映射为有效 EPT02 页。
- `W=1,R=0` 无法通过安全 `map()` API 安装；直接改写原始表项会被 `walk()` 和
  `flatten()` 判定为 `WriteWithoutRead`。

## 文件

- `ept_nested.hpp`：唯一必需嵌入文件。
- `tests/ept_nested_tests.cpp`：严格警告单元/属性/穷举测试。
- `examples/ept_audit_demo.cpp`：权限放大检测与见证打印示例。
- `CMakeLists.txt`：静态头文件目标、测试和示例。
