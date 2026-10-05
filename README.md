# tree-rewriter

TypeScript 实现的项重写器（term rewriter），支持带变量的模式与替换式、
规则优先级、最左最内层（leftmost-innermost）归约策略，并在共享子树（DAG）
上保证纯逻辑复制语义。附带一个无共享的朴素树解释器用于逐步核对。

## 特性

- **模式与替换式**：`v("x")` 为变量，`p("add", p("0"), v("x"))` 为节点模式；
  一阶匹配，同名变量须绑定到结构相同的子树。
- **优先级**：`Rule.priority` 数值小者优先；同优先级按声明顺序。
- **最左最内层**：先按从左到右归约所有子树，再尝试当前节点。
- **共享子树安全**：项是不可变 DAG 节点，工厂做 hash-consing
  （结构相同即同一对象）。重写只重建根到 redex 的脊柱（路径复制），
  未触及的子树按引用共享，绝不原地修改，因此不会污染其他位置。
- **轨迹**：每一步记录命中路径、规则名、redex、变量绑定与替换结果。
- **循环检测**：以结构哈希（hash-cons 节点 id）记录历史状态，
  状态重复时报告循环区间 `{from, to}`。
- **预算中断可恢复**：`run(state, budget)` 消耗有限步数后返回
  `"budget"` 状态，可直接续跑；`exportState` / `importState`
  支持 JSON 快照，跨进程恢复。
- **正确性核对**：`naiveRun` 是无共享、全深拷贝的独立实现，
  `crossCheck` 要求两者状态、每一步的规则/路径/绑定/结果完全一致。

## 文件

- `src/terms.ts` — 不可变 DAG 节点、hash-consing 工厂、模式、匹配、实例化
- `src/rewriter.ts` — DAG 重写器：优先级、最左最内层、轨迹、循环检测、预算/快照
- `src/naive.ts` — 无共享朴素树解释器（对照实现）
- `src/check.ts` — 逐步核对两个实现
- `src/demo.ts` — 演示与自测（共享污染、优先级、循环、预算恢复、随机 fuzz）

## 运行

```sh
npm install --save-dev typescript   # 或任何可用的 tsc
npm run build
npm run demo                        # 即测试：全部检查通过则退出码为 0
```

## 示例

```ts
import { Factory, p, v } from "./terms";
import { Rewriter } from "./rewriter";

const f = new Factory();
const rules = [
  { name: "add-0-l", priority: 0, lhs: p("add", p("0"), v("x")), rhs: v("x") },
];
const rw = new Rewriter(f, rules);
const shared = f.mk("add", [f.mk("0"), f.mk("a")]);
const root = f.mk("mul", [shared, shared]);   // 两个位置共享同一子树

let state = rw.init(root);
const part = rw.run(state, 1);                // 预算 1 步
// part.steps[0]: step 0 @[0] add-0-l, add(0, a) ~> a, { x := a }
// part.state.root.args[1] === shared          // 右侧共享子树未被污染
const rest = rw.run(part.state, 100);         // 恢复执行至正规形 mul(a, a)
```
