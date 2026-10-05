# 规则覆盖分析器（rule-coverage-analyzer）

按顺序匹配的规则集（首条命中规则决定动作，first-match-wins）的**精确**覆盖分析器。
条件支持有限整数区间（半开 `[lo, hi)`）与枚举属性，条件之间为合取。

## 能力

- **完全不可达规则**：规则空间非空，但被前序规则完全遮蔽（`unreachable`）。
- **部分遮蔽范围**：规则空间的一部分被前序规则遮蔽（`partially-shadowed`），
  报告给出可达部分 `reachableBoxes` 与被遮蔽部分 `shadowedBoxes`（均为互不相交的盒）。
- **自相矛盾规则**：互斥条件、空区间（`lo >= hi`）、空枚举集导致规则不匹配任何输入（`empty`）。
- **未覆盖输入**：任何规则都不覆盖的输入空间 `uncoveredBoxes`。
- **最小反例**：每条可达规则给出命中它的最小输入 `witness`；
  未覆盖时给出最小未覆盖输入 `uncoveredWitness`。
  “最小”按 schema 属性声明顺序的字典序（整数按数值，枚举按声明顺序）。
- **增删规则后更新分析**：`addRule` / `removeRule` 后再次调用 `analyze()` 即可；
  分析始终是对当前规则集的全量精确重算，与一次性分析结果完全一致。

## 为什么不是抽样

分析基于盒（各属性约束的笛卡尔积）的精确集合运算：逐维求交、
盒差 `B \ (A1 ∪ A2 ∪ …)` 分解为互不相交的盒。结论对整个有限输入空间成立，
不逐点抽样、不概率推断。整数域边界在 schema 中显式声明为有限闭区间 `[min, max]`。

## 小域穷举核对

`src/exhaustive.ts` 提供与分析器**相互独立**的暴力实现：逐点枚举整个（小）输入空间，
按首条命中规则归类，再与符号分析报告交叉核对（状态、最小反例、可达/遮蔽/未覆盖体积、
逐输入归属）。测试包含 300 轮随机小域（随机空区间、相邻半开端点、互斥条件、随机增删）
的全量核对，符号结果与穷举结果完全一致。

## 使用

```ts
import { RuleCoverageAnalyzer } from './src/analyzer.ts';
import { intRange, enumEq } from './src/model.ts';

const analyzer = new RuleCoverageAnalyzer({
  attrs: [
    { kind: 'int', name: 'age', min: 0, max: 120 },      // 显式有限边界
    { kind: 'enum', name: 'tier', values: ['free', 'pro'] },
  ],
});

analyzer.addRule({ id: 'kids', action: 'deny', conditions: [intRange('age', 0, 13)] });
analyzer.addRule({ id: 'teens', action: 'trial', conditions: [intRange('age', 10, 18), enumEq('tier', 'free')] });

const report = analyzer.analyze();
// report.rules[i].status: 'reachable' | 'partially-shadowed' | 'unreachable' | 'empty'
// report.rules[i].witness / report.uncoveredWitness: 最小反例
analyzer.removeRule('kids');
const updated = analyzer.analyze(); // 增删后重新分析
```

## 运行

代码为「可擦除语法」风格的 TypeScript（无 enum / 参数属性等需代码生成的特性），
无需编译步骤：

```sh
node --test test/analyzer.test.ts   # 测试（Node >= 23.6；22.6+ 需 --experimental-strip-types）
node examples/demo.ts               # 演示
```

`tsconfig.json` 仅供编辑器 / `tsc --noEmit` 类型检查（开启 `erasableSyntaxOnly`）。

## 文件结构

- `src/model.ts` — schema / 条件 / 规则类型与条件构造辅助。
- `src/box.ts` — 盒的精确集合运算：交、差、体积、最小点、包含判定。
- `src/analyzer.ts` — `RuleCoverageAnalyzer`：有序规则集的分析与增删更新。
- `src/exhaustive.ts` — 小域穷举与 `crossCheck` 交叉核对。
- `test/analyzer.test.ts` — 定点用例（互斥条件、空区间、相邻半开端点等）+ 随机小域核对。
- `examples/demo.ts` — 演示。
