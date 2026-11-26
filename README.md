# ARM64 PAC/BTI 语义流与签名预言机验证库

面向 C++17 二进制安全分析宿主的可嵌入静态分析库，仅依赖 C++ 标准库。库在受限
ARM64 指令控制流图（CFG）上做跨基本块的流敏感抽象解释，追踪通用寄存器与栈槽
携带的指针认证标签，并检出四类控制流劫持风险。

## 目录结构

- `include/pacbti/label.hpp`：标签格（`Bottom/Unknown/Raw/Signed/Poisoned`）、密钥域、抽象状态与合并算子。
- `include/pacbti/model.hpp`：受限 ARM64 指令、基本块、代码点（含虚拟页 `GP` 位）、函数指针类型表与 CFG 模型，以及零分配开销的构造辅助函数。
- `include/pacbti/analyzer.hpp` / `src/analyzer.cpp`：工作流不动点引擎、PAC/BTI 传输函数、诊断报告。
- `include/pacbti/pacbti.hpp`：一次性引入全部公共接口的总括头文件。
- `tests/`：零依赖断言框架与 29 个反例/穷举测试。

## 指针标签模型

每个寄存器（`x0`–`x31`）与栈槽携带一个 `pacbti::Label`：

- `Raw`：未签名原始指针，带函数指针类型 ID 与攻击者可控污染位。
- `Signed`：已签名指针，记录密钥域 `IA/IB/DA/DB`、指针类型、绑定 `modifier` 的定义点来源链、签名指令来源链与污染位。
- `Poisoned`：认证失败后高位被置为非法错误码的毒化指针，保留产生毒化的指令来源链。
- `Unknown`/`Bottom`：格顶（信息不足）与格底（不可达）。

标签在控制流汇合点按单调格合并：种类不一致退化为 `Unknown`，来源链取并集，污染位取逻辑或，保证工作流迭代必然到达不动点。

## 支持的指令语义

- 签名：`PACIA/PACIB/PACDA/PACDB`（`build::pacSign`）。
- 认证：`AUTIA/AUTIB/AUTDA/AUTDB`（`build::pacAuth`），密钥不匹配或对已签名值重复认证会产生毒化指针。
- 剥离：`XPACI/XPACD`（`build::xpac`），剥离签名高位并保留毒化来源与剥离点。
- 间接跳转：`BR/BLR` 与带认证的 `BRAA/BLRAA/RETAA`（`build::indirectBranch`、`build::authenticatedReturn`）。
- 着陆垫：`BTI c`、`BTI j`、`BTI jc`（`build::bti`，同时由 `CodePoint::landing` 权威描述）。
- 数据与控制：栈帧读写、寄存器搬运、攻击者输入、原始指针加载、外部可观测槽位写入、条件分支与顺序 fallthrough。

## 四类检查

1. **认证绕过（`AuthBypassXpac`）**：认证失败产生的毒化指针若经 `XPACI` 剥离高位，且未触发故障就到达 `BR/BLR` 等非认证间接跳转，立即报告并给出 `AUT* -> XPACI -> 跳转` 的来源轨迹；无剥离步骤的毒化跳转单列为 `PoisonedBranch`。
2. **签名预言机（`SigningOracle`）**：攻击者可控的指针或 `modifier` 进入任意 `PAC*`，且带签名来源的结果流向外部可观测槽位（`ExtWrite` 或溢出到栈槽）时报告；污染经寄存器、栈槽与搬运传播。
3. **跨类型 `modifier` 复用（`ModifierTypeConflict`）**：不同函数指针类型在同一密钥域下绑定结构相同的 `modifier` 来源时报告，相同类型复用或不同密钥域/不同 `modifier` 不误报。
4. **BTI 着陆垫（`MissingBtiLanding`）**：对每条间接跳转，按函数指针类型表枚举全部可达目标；落在 `GP=1` 保护页时，跳转（含 `BRAA/RETAA`）要求 `BTI c/jc`，调用（`BLR/BLRAA`）要求 `BTI j/jc`。无法解析类型的跳转保守报告 `UntypedIndirectBranch`，`GP=0` 页面豁免。

## 最小用法

```cpp
#include "pacbti/pacbti.hpp"

using namespace pacbti;

ControlFlowGraph cfg;
BasicBlock block;
block.id = 0;
block.instructions = {
    build::input(0, "attacker"),
    build::pacSign(1, 0, 2, KeyDomain::IA, /*typeId*/ 10, "pacia"),
    build::extWrite(1, "shared_buffer"),
};
cfg.blocks.push_back(block);

AnalysisReport report = Analyzer(cfg).run();
for (const Finding& finding : report.findings) {
    // finding.kind / finding.site / finding.trace / finding.message
}
```

## 构建与测试

库与测试均以 `-Wall -Wextra -Wconversion -Wsign-conversion -Werror` 编译：

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖标签格合并、签名预言机正反例、`XPACI` 认证绕过与 `RETAA` 故障路径、
`modifier` 跨类型冲突、BTI `c/j/jc` 矩阵，以及含菱形分支与回边循环的小 CFG
穷举交叉验证。
