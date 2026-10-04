# 接口兼容检查器（interface-compat-checker）

在一个含**记录、联合、函数**的有限结构类型系统中，判断**新接口能否替代旧接口**
（即 `new <: old`：新接口产生的每个值都能被旧接口的消费方接受）。
不兼容时输出**路径**与**最小值形状反例**；递归类型通过**成对访问状态**
（pairwise visited state）终止比较。

## 类型系统

```ts
type Type =
  | { kind: "prim"; prim: "number" | "string" | "boolean" | "null" | "undefined" }
  | { kind: "record"; fields: Field[] }        // Field: { name, type, optional }
  | { kind: "union"; options: Type[] }
  | { kind: "func"; params: Param[]; ret: Type }
  | { kind: "ref"; name: string };             // 递归类型经 Env 中的命名引用表达
```

## 子类型规则

| 构造 | 规则 |
| --- | --- |
| 原始类型 | 仅同名兼容 |
| 记录 | 宽度（新接口可多出字段）+ 深度（字段类型**协变**） |
| 联合（在左） | 每个选项都必须兼容右侧 |
| 联合（在右） | 左侧至少兼容一个分支（失败分支的假设会回滚，不污染兄弟分支） |
| 函数参数 | **逆变**：旧参数类型 <: 新参数类型；新函数可以少收参数，不能多要必需参数 |
| 函数返回值 | **协变**：新返回类型 <: 旧返回类型 |
| 可选 vs 可空 | `a?: T`（键可缺席）≠ `a: T \| undefined`（键必在、值可空），两个方向都不兼容 |

## 递归终止

`check` 维护一个按节点身份键控的 `(左, 右)` 已访问对集合
（Amadio–Cardelli 式共归纳假设）：再次遇到同一对即视为兼容。
由于首次探索该对时会检查其全部可达义务，任何真实违规都会在
首次探索中被发现，因此假设是可靠的，且节点对有限保证终止。

## 反例生成

每个不兼容点给出：

- `path`：从根 `$` 出发的路径，如 `$.handle.arg0(e).trace`、`$.kid|#1`、`.return`；
- `witness`：属于新接口、但旧接口不接受的最小值形状——
  记录只含必需字段、联合取首个不递归的选项、递归引用折叠为 `…Name`；
  字段级失败会包成完整的新接口值，参数级失败以 `(call argument)` 标注。

## 运行

```bash
npm test     # 类型擦除到 .build/ 后运行 test/check.test.ts
npm run demo # 运行 src/demo.ts 中的场景
```

本环境没有 TypeScript 编译器，`scripts/strip-types.mjs` 仅做受限的
类型擦除以便用 Node 直接验证逻辑（源码即标准 ESM TypeScript）。
有网络时建议用真编译器：

```bash
npm install --save-dev typescript && npx tsc -p tsconfig.json   # 产出 dist/
```

## 结构

- `src/types.ts` — 类型 AST、反例形状、检查结果
- `src/check.ts` — 核心子类型检查（成对访问状态、各构造的规则）
- `src/sample.ts` — 最小值形状采样
- `src/render.ts` — 形状与报告渲染
- `src/builders.ts` — 构造类型的便捷函数
- `src/demo.ts` — 演示场景；`test/check.test.ts` — 22 个断言用例

## 已知限制

- 联合在右侧时只做"逐分支试配"，不做交集分配等更深层推理；
- 原始类型之间无子类型（如没有 `number <: number | string` 之外的字面量类型）。
