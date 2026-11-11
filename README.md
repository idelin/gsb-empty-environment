# 正则拒绝服务（指数回溯）分析包

面向代码审查宿主的 TypeScript 分析包，用于定位回溯匹配器中可能出现**指数级分支**
的规则。零运行时依赖，运行在 Node.js 23.9+ 的原生 TypeScript 类型擦除模式上。

## 支持的语法子集

- 字面量字符与转义字面量（如 `\*`、`\(`）
- 括号分组（捕获/非捕获在本分析中等价）
- 并集 `|`
- 连接（隐式拼接）
- 星号 `*`
- 整串锚定 `^` 与 `$`（仅允许分别出现在模式首尾）

按任务约定，**输入不含反向引用**；遇到 `\1` 等数字转义会显式报错。字符类 `[]`、
`+`、`?`、`{n,m}`、点号 `.` 等不在子集内的构造同样显式报错，而不是静默按字面量处理。

## 分析方法

1. **解析**：递归下降解析器构造 AST，记录锚点与节点源码区间。
2. **Thompson NFA**：把 AST 构造成带唯一边 ID 的 ε-NFA。NFA 的有向环只来自星号，
   因此找到的歧义环天然“可泵送”。
3. **路径敏感乘积自动机**：乘积状态的每条腿记录 `(NFA 状态, 最后一条边 ID)`。
   两条腿在同一 NFA 状态、但经由不同边汇合时构成歧义 twin state，等价于同一输入
   字符串存在两条不同的接受路径。朴素状态对乘积图会把 `a*` 这类唯一解析模式误报，
   边身份机制用于消除这类假阳性。
4. **见证构造**：从歧义 twin 之间找出“消费至少一个字符”的环，输出
   - 前缀 `prefix`
   - 可泵送重复段 `pump`
   - 失败后缀 `failingSuffix`：`pump` 本身 + 一个模式字母表外的**死字符**，
     死字符无法被任何分支消化，迫使匹配失败并穷尽所有解析路径。
5. **教学回溯解释器验证**：带步数上限的 CPS 回溯匹配器在泵送见证上实测步数，
   输出每个泵送次数下的步数、撞顶标记与相邻步数比值，作为分支数增长证据。

## 三种结论（不提供“安全”判定）

| 状态 | 含义 |
| --- | --- |
| `confirmed` | 找到可泵送歧义，且回溯解释器实测到指数特征增长或撞步数上限 |
| `unverified` | 静态找到了歧义结构，但当前泵送规模/预算下未达到指数增长阈值 |
| `no-evidence` | 未找到“可泵送歧义 + 失败后缀”证据，**不等于安全** |

每份报告都带统一免责声明：本工具只覆盖上述一类指数回溯，不覆盖多项式爆炸等其他
复杂度形态；预算耗尽、语法子集限制、检测策略都可能导致漏报。任何情况下都不会输出
“该正则安全”的结论。

## 用法

```ts
import { analyzePattern, runBacktracking } from './index.ts';

const report = analyzePattern('(a|aa)*$', {
  stepCap: 1_000_000,
  pumpCounts: [0, 2, 4, 6, 8],
});

console.log(report.status);      // 'confirmed'
console.log(report.witness);     // { prefix, pump, failingSuffix, deadChar }
console.log(report.growth);      // 每个泵送次数的步数、撞顶、比值
console.log(report.disclaimer);  // 不免责声明

// 单独调用教学回溯解释器
const result = runBacktracking('(a|aa)*$', 'aaaaaaaa~', { stepCap: 5000 });
// { matched: false, steps: 5001, capped: true, trace: [...] }
```

主要导出：

- `analyzePattern(pattern, options?)`：端到端分析入口
- `runBacktracking(pattern, input, options?)`：带步数上限的教学回溯解释器
- `parsePattern` / `PatternSyntaxError`：解析器
- `buildNfa`、`nfaAccepts`、`epsilonClosure` 等：NFA 工具
- `AmbiguityDetector`、`detectAmbiguity`、`buildFailingSuffix`：歧义检测
- 类型：`AnalysisReport`、`AmbiguityWitness`、`GrowthSample`、`AnalysisStatus` 等

## 命令

```sh
npm test     # node:test 测试套件（42 个用例）
npm run demo # 打印若干模式的分析报告与解释器增长证据
```

## 设计边界

- 检测目标是**可泵送的歧义路径**，即同一字符串存在多条 NFA 接受路径且歧义位于星号环上。
- 无歧义但仍可能昂贵的模式（如某些多项式回溯）不在判定范围内，统一返回 `no-evidence`。
- 乘积图搜索受 `productStateBudget`（默认 200_000）保护，预算内未找到即返回未证实。
- 回溯解释器的步数是节点进入次数，用于横向比较增长趋势，而非任何具体引擎的真实耗时。
