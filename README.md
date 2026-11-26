# `module_plan` — 热更新模块装载规划库

`module_plan` 是一个 C++20、无第三方依赖的规划/校验库。它在真正加载或卸载原生模块之前，对签名模块图、跨命名空间桥、窄接口、ABI 重导出、句柄钉扎、驻留峰值和逐步回滚不变量进行确定性检查，并返回带绑定见证的驻留区。

> 原始任务：C++20 宿主在模块热更新时需保证在途句柄与跨空间桥接均不破坏内存安全与权限边界，实现可导入的模块装载规划库。

## 构建与验证

```bash
cmake -S . -B build
cmake --build build -j
ctest --test-dir build --output-on-failure
./build/exhaustive_matrix
```

目标平台需要 C++20。库本身不执行真实动态加载，也不内置非对称密码学；宿主通过 `mplan::ISignatureVerifier` 接入其公钥验签实现。`mplan::TestSignatureOracle` 仅用于测试和穷举图，它用确定性摘要模拟签名绑定，不能当作生产签名算法。

## 在宿主中导入

使用本仓库作为子目录：

```cmake
add_subdirectory(third_party/module_plan)
target_link_libraries(host PRIVATE module_plan::module_plan)
```

公共头文件为：

```cpp
#include "module_plan/module_plan.hpp"
#include "module_plan/exhaustive.hpp" // 可选：内置小图穷举矩阵
```

## 核心模型

- `mplan::ModuleId{ns, name}`：命名空间 + 逻辑模块名；`spacea/codec` 与 `spaceb/codec` 是不同模块。
- `mplan::ModuleManifest`：绑定发布者、公钥代次、语义版本、模块 ABI 摘要、导出符号版本/契约、权限上限、驻留字节和按 `PlatformCondition` 启用的直接依赖。
- `mplan::SignedModule`：清单加签名；规划器要求提供非空 `ISignatureVerifier`。
- `mplan::Bridge`：命名空间之间的显式签名桥，使用 `Bridge::when(condition)` 声明平台条件，并逐条列出窄符号绑定。
- `mplan::Catalog`：已签名模块、桥和 `mplan::KeyRing` 的只读目录。
- `mplan::PlanRequest`：平台、初始快照、目标快照、在途句柄、共享峰值字节预算和验签器。
- `mplan::planMigration()`：返回独立状态码、逐步 `mplan::ResidencyStep`、`mplan::PinLease` 和绑定见证。
- `mplan::reviewPlan()` / `mplan::ReadOnlyReviewer`：只读复核器，重新核对字节求和、指纹、句柄钉扎、回滚峰值、撤销代次重载和关键绑定。

## 必须区分的状态码

| 场景 | 状态码 |
|---|---|
| 跨命名空间导入找不到显式签名桥或所需绑定 | `StatusCode::BridgeMissing` |
| 存在桥，但所有候选都被 `Bridge::when` 排除 | `StatusCode::BridgePlatformExcluded` |
| 桥权限超出导入模块的签名权限上限 | `StatusCode::PermissionWidening` |
| 依赖/重导出环包含不受信任或超出代次窗口的公钥代次 | `StatusCode::OutOfGenerationCycle` |
| 普通同代次依赖环 | `StatusCode::DependencyCycle` |
| 宽 ABI 导出或 `allowsBroadAbi` 试图替代窄接口 | `StatusCode::BroadAbiSubstitution` |
| 菱形重导出到达不同 ABI 摘要 | `StatusCode::DiamondAbiDivergence` |
| 两阶段析构钉扎基线超过预算 | `StatusCode::DestructorPinningExceeded` |
| 任一中间步骤的回滚峰值超过同一预算 | `StatusCode::RollbackBudgetExceeded` |
| 回滚动作试图重新加载已撤销公钥代次 | `StatusCode::RevokedGenerationReload` |

同时提供了语义别名，例如 `BridgeExcludedByPlatform`、`PermissionRelaxed`、`GenerationCycle` 和 `PinningBudgetExceeded`。

## 跨空间与 ABI 规则

1. 同命名空间依赖按普通直接依赖解析。
2. 不同命名空间的依赖必须找到 `fromNamespace -> toNamespace` 的签名桥，或通过 `Dependency::bridgeId` 指定显式桥。
3. 没有候选桥返回 `BridgeMissing`；候选存在但平台不匹配返回 `BridgePlatformExcluded`。
4. 桥授权集合必须是导入模块权限集合的子集；额外权限返回 `PermissionWidening`。
5. 目标导出必须是 `InterfaceKind::Narrow` 且契约/版本匹配；宽 ABI 替代返回 `BroadAbiSubstitution` 或 `NarrowContractMismatch`。
6. `checkReexportDiamonds()` 追踪所有重导出边。同一导出名经菱形路径到达多个不同 ABI 摘要时返回 `DiamondAbiDivergence`，相同 ABI 的收敛菱形允许通过。

## 两阶段析构钉扎

`mplan::OpenHandle` 指定在途句柄锁定的旧快照，`mplan::DestructorHook` 可列出析构钩子可达的桥目标模块和桥垫片。

`mplan::HandlePinRegistry` 实现显式状态机：

```text
Open --beginDestruction()--> DestructionArmed --completeDestruction()--> Completed
```

- 第一阶段只排空新调用，旧快照、钩子目标和桥垫片仍保持钉扎。
- 未经过 `DestructionArmed` 不能直接完成析构。
- 第二阶段确认析构完成后才释放钉扎。

规划结果中的 `plan.pinLeases` 保存每个句柄的钉扎模块、桥、字节数和带指纹的 `WitnessKind::Pinning` 见证。

## 驻留与回滚公式

每个步骤都返回独立区域和汇总字段。对步骤 `k`：

```text
oldPinnedBytes =
    initialClosureBytes
  + hookBridgeTargetClosureBytesNotInInitialClosure
  + destructorScratchBytes

peakBytes(k) =
    oldPinnedBytes
  + loadedNewClosurePrefixBytes(k)
  + activeShimBytes(k)
```

`activeShimBytes` 包含析构桥垫片、过渡垫片和已绑定的跨空间窄桥垫片。旧闭包与新闭包即使坐标相同也保守地同时计入，因为热更新期间可能存在两份快照映射。

每个 `RollbackWitness` 都以初始快照为目标。规划器只生成“保留旧钉扎、丢弃新前缀、释放垫片”的动作，不通过重新加载旧公钥代次来腾挪预算。若：

- 基线钉扎已超过预算：`DestructorPinningExceeded`；
- 任一装载步骤的 `peakBytes` 超过同一预算：`RollbackBudgetExceeded`；
- 复核发现 `reloadsRevokedGeneration == true`：`RevokedGenerationReload`。

成功计划的每个步骤都满足 `forwardPeakBytes == rollbackPeakBytes <= budgetBytes`，因为回滚开始时仍持有完整的当前过渡驻留区，随后才收缩。

## 只读复核

`mplan::reviewPlan(catalog, request, plan)` 不会修改目录或计划，它会检查：

- 根、预算和句柄租约是否与请求一致；
- 区域字节求和是否等于 `old + new + shim`；
- 步骤、区域、绑定、钉扎和回滚指纹是否匹配；
- 每个装载步骤是否带有模块签名见证；
- 成功计划中的绑定是否满足平台、签名和代次策略；
- 每一步是否都有回滚见证且未超预算；
- 是否存在重新加载撤销代次的动作。

## 小图穷举矩阵

`mplan::runExhaustiveMatrix()` 构造并对照以下场景：

- 桥缺失、平台排桥、平台匹配控制组；
- 权限放宽和宽 ABI 替代；
- 菱形 ABI 分歧与同 ABI 收敛控制组；
- 析构钉扎超限与足够预算控制组；
- 中间步骤回滚预算不足与预算充足控制组；
- 代外循环与同代次循环控制组；
- 已撤销公钥代次仅以钉扎内存回滚、不重新加载；
- 平台条件直接依赖被排除/启用后缺失。

CTest 会校验所有实际状态码等于预期，并对所有成功计划执行只读复核。
