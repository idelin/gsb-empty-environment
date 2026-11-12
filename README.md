# 字体字形点预算

网页字体清洗服务要在交给渲染器前处理不可信 TrueType。攻击者可在 glyf 中让复合字形互相引用、累计点数暴涨，或让 loca 偏移倒退。为 C++17 字体管线写可嵌入的 glyf 预算校验组件：校验 loca 单调与边界，展开复合字形时限制递归深度、总轮廓点数和指令字节数，成环、越界和超预算均拒绝并给出字形号。用自引用、两字形互引、深层复合、偏移倒退和正常字体验证。

## 组件

`glyf_budget.hpp` 是单头文件、零外部依赖（仅标准库）的 C++17 组件，命名空间
`netsec::glyf`。把 glyf/loca 表的原始字节连同 `maxp.numGlyphs` 与
`head.indexToLocFormat` 一起传入即可，不需要完整的 SFNT 解析器。

```cpp
#include "glyf_budget.hpp"

netsec::glyf::GlyfBudget budget;   // 全部字段有安全默认值，可按需收紧
budget.maxDepth = 32;
budget.maxTotalPoints = 65535;
budget.maxInstructionBytes = 65535;

netsec::glyf::GlyfStats stats;
netsec::glyf::GlyfResult r = netsec::glyf::validateGlyf(
    glyfBytes, glyfSize, locaBytes, locaSize,
    numGlyphs, /*longLoca=*/indexToLocFormat == 1, budget, &stats);

if (!r) {                       // r.error 为分类码，r.glyphIndex 为字形号
    reject(r.message);          // detailIndex 记录被引用的具体组件
}
```

## 校验内容

- **loca 完整性**：条目数量满足 `numGlyphs + 1`；支持短（16 位，值乘 2）与
  长（32 位）两种格式；每个偏移都落在 glyf 范围内，且严格单调非递减，倒退或
  越界都会定位到具体字形号。
- **边界安全解析**：每个字形按其 `[loca[i], loca[i+1])` 窗口用带边界检查的
  游标读取；完整解码简单字形的 `endPtsOfContours`、标志流（含 repeat）与
  x/y 坐标流长度，以及复合字形的组件记录、参数与变换字段。
- **复合展开预算**（按每个根字形独立累计，重复引用按次计费）：
  - `maxDepth`：复合嵌套深度，根字形记为深度 1；
  - `maxTotalPoints`：展开后累计轮廓点数；
  - `maxInstructionBytes`：简单字形与复合字形指令字节累计；
  - `maxComponentsPerGlyph`：单个复合的组件记录数上限。
- **拒绝项与定位**：自引用/互引等成环（`CompositeCycle`）、组件索引越界
  （`GlyphIndexOutOfRange`）、各类超预算、字形截断与结构非法，均返回非 Ok
  分类码。`glyphIndex` 统一为正在校验的根字形号，`detailIndex` 为触发问题的
  具体组件，`message` 同时包含两者。

## 测试

- `glyf_budget_test.cpp`：内置大端字体构造器，覆盖正常字体（含短/长 loca）、
  自引用、两字形互引、深层复合链、loca 偏移倒退与越界、点数/指令预算、组件
  越界、字形截断、空字形引用以及带缩放变换的复合字形，共 40 项断言。
- `glyf_fuzz_smoke.cpp`：2 万组确定性随机字节的健壮性冒烟测试，配合
  AddressSanitizer/UBSan 验证任意输入下不会越界读取或崩溃。

```sh
clang++ -std=c++17 -Wall -Wextra -Wpedantic -O2 glyf_budget_test.cpp -o t && ./t
# 或通过 CTest
cmake -S . -B build && cmake --build build && ctest --test-dir build
```
