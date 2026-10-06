# adt-match-checker

代数数据类型（ADT）模式匹配检查库：判定分支**穷尽性**、检出被前面分支**完全覆盖（不可达）的冗余分支**，非穷尽时给出**最小构造深度的见证值**，并用**深度受限枚举**独立交叉验证。

> 源码为 ESM JavaScript + 完整 JSDoc 类型注解（`tsc --checkJs` 可直接类型检查），
> 零依赖，Node >= 16 即可运行。

## 快速开始

```bash
npm test    # 运行测试（test/run.mjs）
npm run demo  # 运行演示（examples/demo.js）
```

```js
import { checkMatch, crossValidate, ctor, wild, v } from 'adt-match-checker';

const _ = wild();
const schema = {
  types: {
    Bool: [ { name: 'True', args: [] }, { name: 'False', args: [] } ],
    List: [ { name: 'Nil', args: [] }, { name: 'Cons', args: ['Bool', 'List'] } ],
  },
};

const branches = [
  ctor('Nil'),
  ctor('Cons', ctor('True'), ctor('Cons', _, _)),
  ctor('Cons', ctor('True'), ctor('Cons', ctor('False'), _)), // 被上一行覆盖
];

const r = checkMatch(schema, 'List', branches);
// r.exhaustive === false
// r.witness.value  === Cons(False, Nil)   （构造深度 2 的最小见证）
// r.redundant      === [2]                （被 #1 完全覆盖的不可达分支）

const cv = crossValidate(schema, 'List', branches, 4);
// cv.uncovered     深度 <= 4 内未被覆盖的值
// cv.unreachable   深度内零命中的分支（与 r.redundant 对照）
// cv.inconsistencies === []  算法结论与枚举结果一致
```

## 模型

- **Schema**：`{ types: { 类型名: [{ name, args: [类型名...] }] } }`，构造器有限且封闭，类型可递归引用。
- **模式**：构造器模式可任意嵌套；`wild()` 为通配符 `_`，`v('x')` 为变量模式（语义同通配符，仅多绑定名）。
- **值**：`{ ctor, args }` 有限树；构造深度定义为叶子 1、其余 `1 + max(子值深度)`。

## 算法

**有用性判定（Maranget）** `src/useful.js`
- `useful(M, q)`：模式向量 `q` 相对矩阵 `M` 是否还能匹配新值；通过**专门化矩阵** `S(c, M)` 与**默认矩阵** `D(M)` 递归。
- 穷尽性 = 通配符向量相对全部分支无用；分支 `#i` 冗余 = 它相对前 `i-1` 行无用。
- 行首构造器集合**未覆盖签名**时走默认矩阵，见证行首是带 `avoid` 约束的通配符（表示“任一未出现的构造器”），保证见证不会被已有分支匹配。

**最小构造深度见证** `src/schema.js` `src/witness.js`
- 先用**不动点迭代**求每个类型/构造器的最小驻留深度（递归类型在有限步内收敛；不可驻留类型如 `Loop = Wrap(Loop)` 保持 `Infinity`，绝不无限展开）。
- 签名完备需要展开通配符时，对所有构造器分支的见证按最小深度代价择优；最后把通配符具体化为本类型深度最小的驻留值。
- 若缺失模式只经过不可驻留类型，则报告 `vacuous`：无具体缺失值，视为穷尽。

**深度受限枚举交叉验证** `src/enumerate.js`
- 按构造深度截断枚举所有值（递归类型不会无限展开），逐个做首次匹配：
  - 算法称穷尽 ⇒ 深度内不得有未覆盖值；
  - 算法给出深度 `d` 的见证 ⇒ 枚举到的最浅未覆盖值深度必须恰为 `d`；
  - 算法称分支冗余 ⇒ 该分支深度内零命中；零命中且算法称可达时给出“可能需要更深值”的说明。

## API 一览

| 导出 | 说明 |
| --- | --- |
| `checkMatch(schema, type, branches)` | 返回 `{ exhaustive, vacuous, witness, redundant }` |
| `crossValidate(schema, type, branches, maxDepth)` | 枚举交叉验证报告（含 `uncovered`、`unreachable`、`inconsistencies`） |
| `enumerateValues(schema, type, maxDepth)` | 深度受限值枚举 |
| `computeMinDepth(schema)` | 最小驻留深度不动点 |
| `useful(schema, depths, matrix, colTypes, vec)` | 底层有用性判定（返回见证向量或 `null`） |
| `ctor / wild / v / val` | 模式与值的构造器 |
| `prettyPattern / prettyValue / matchValue / valueDepth` | 打印、匹配、深度工具 |
