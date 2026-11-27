# CT-IR：SSA/三地址码 IR 常量时间污点验证库

`ctir` 是一个可嵌入、零外部依赖的 C++17 单头文件库，用于在密码安全审查宿主中
对底层 SSA / 三地址码 IR 做秘密污点信息流与隐式流（PC Taint）常量时间验证。

## 特性

- 单头文件、零外部依赖：仅包含 `include/ctir/constant_time.hpp`，只用 C++17 标准库。
- 严格编译：以 `-Wall -Wextra -Wconversion -Werror -pedantic` 通过。
- 格 `{PUBLIC < SECRET}` 上的单调不动点数据流分析，完整覆盖：
  - 显式流：算术 / 位运算、`COPY`、`LOAD` 的数据依赖传播；
  - 隐式控制流：基于后支配（Post-Dominator）与控制依赖图（Cytron 算法）
    计算 PC Taint，受秘密分支控制的赋值在到达后支配汇合点前一律污染为
    `SECRET`，汇合点之后自动净化；
  - 常量时间 Sink 策略：
    `BR` 条件为秘密、`LOAD`/`STORE` 地址为秘密、`DIV`/`REM` 任一操作数为秘密
    均报告违规；`CMOV`/`SELECT` 允许秘密条件，其结果标为 `SECRET` 且不报 Sink；
  - `DECLASSIFY` 边界审计：未掩蔽的原始密钥提前脱敏报告
    `UNMASKED_DECLASSIFY`；经比较归约（`CMPEQ`/`CMPLT`）或公开输入的
    分支无关 `CMOV`/`SELECT` 产生的掩码结果允许脱敏。
- 每条违规输出精确指令 ID、源秘密参数集合与污点传播路径见证（witness）。
- 结构化 IR 校验：重复 SSA 定义、未定义操作数、块/指令 ID、操作数数量、
  跳转目标等错误在分析前以硬错误返回，不产生静默误判。

## IR 模型

- `Function`：函数名、`SECRET`/`PUBLIC` 入参列表、基本块列表。
- `Block`：连续编号（从 0 开始）的基本块，内含指令序列。
- `Instruction`：三地址码指令，含唯一 `id`、操作码 `Op`、目标 SSA 名、
  操作数、立即数与分支目标块。支持的操作码：
  `CONST`、`COPY`、`ADD/SUB/MUL/AND/ORR/XOR/SHL/SHR`、
  `CMPEQ/CMPLT`、`CMOV/SELECT`、`DIV/REM`、`LOAD/STORE`、
  `BR/JMP`、`DECLASSIFY`、`NOP`。
- 每个 SSA 名只允许定义一次；`BR` 带两个块目标，`JMP` 带一个块目标；
  无终结指令的块视为连到虚拟出口。

## 分析算法

1. 构建 CFG，并为多出口函数引入虚拟出口节点。
2. 在逆 CFG 上做迭代支配分析，得到后支配关系与立即后支配点（IPostDom）。
3. 对每个条件分支 `A -> B`，沿 `B` 的立即后支配链上溯到后支配 `A` 的节点，
   链上所有块对 `A` 存在控制依赖，构成控制依赖图。
4. 在正向逆后序（RPO）上跑不动点：
   - 块的 PC Taint 为其全部控制依赖分支条件的格上界；
   - 指令结果先按显式流规则计算，再与 PC Taint 合流；
   - 迭代到环境不再变化（含回边的循环同样收敛）。
5. 遍历所有指令收集常量时间 Sink 与脱敏事件，生成确定性排序的报告。

“可安全脱敏”判定：秘密值只有经过单比特/掩码归约（比较指令），或由
`CMOV`/`SELECT` 在秘密谓词下选择公开（或本身已可安全脱敏）的数据臂产生时，
才标记为 release-safe；原始密钥材料、算术结果、隐式污染值均不可提前脱敏。

## 快速使用

```cpp
#include "ctir/constant_time.hpp"

ctir::Function function;
function.params = {ctir::Parameter{"key", ctir::Label::Secret},
                   ctir::Parameter{"tbl", ctir::Label::Public}};
ctir::Block block;
block.id = 0;
block.instrs = {
    ctir::Instruction::binary(1, ctir::Op::Add, "addr", "tbl", "key"),
    ctir::Instruction::load(2, "value", "addr"),
};
function.blocks.push_back(block);

ctir::Report report = ctir::Analyzer(std::move(function)).run();
// report.ok == false，violations 中含 SECRET_ADDRESS_LOAD（指令 2）
std::cout << ctir::Analyzer::renderReport(report);
```

## 构建与测试

Make：

```sh
make test    # 编译并运行全部单元测试
make demo    # 渲染一条 S-box 泄漏报告
```

CMake：

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

## 测试覆盖

`tests/test_constant_time.cpp` 包含：

- 标准常量时间掩码比较 + 条件交换（`SELECT` 实现）：零违规、零误报；
- S-box `table[secret]` 查表：检出 `SECRET_ADDRESS_LOAD`，校验源参数与见证；
- 隐式控制流泄漏：检出秘密 `BR`，秘密分支内的公开表达式被 PC 污染且不可
  提前脱敏，后支配汇合点之后的新定义恢复 `PUBLIC`；
- 秘密 `DIV`/`REM`：检出两条可变延迟指令违规，公开算术无误报；
- 未掩蔽 `DECLASSIFY`：检出提前脱敏；
- 自然循环：验证回边不动点收敛与循环内 Sink；
- 畸形 IR：未定义 SSA 操作数返回硬错误。
