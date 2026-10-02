# LR(1) 分析表构建与冲突检测诊断器

用 TypeScript 实现的 LR(1) 分析器构造工具链：

- **文法解析**：BNF 风格文法文本（`->`、`|`、注释、ε 产生式）；
- **First / Follow 集**：不动点迭代计算，支持 ε；
- **LR(1) 项集闭包与自动机**：`closure` / `goto` / 规范 LR(1) 项集族构建；
- **ACTION / GOTO 表生成**：shift / reduce / accept 动作；
- **冲突检测**：精确定位 Shift/Reduce 与 Reduce/Reduce 冲突（状态、触发 token、引发冲突的 LR(1) 项）；
- **最短输入证据**：在自动机上用 Dijkstra（边权 = 符号的最短终结符产出 minYield）求出触发冲突的
  最短输入 token 串，并用非确定性 LR 模拟验证证据可复现冲突点；
- **解决建议**：针对冲突类型给出消解策略，并提供 `resolveConflicts(tables, "shift" | "reduce")`
  生成消解后的分析表。

## 环境要求

- Node.js **>= 22.6**（使用内置的 TypeScript 类型擦除运行 `.ts`，无需编译、无第三方依赖）；
- Node.js >= 23.6 可省略 `--experimental-strip-types` 标志；
- 测试使用 Node.js 内置测试运行器 `node:test`。

## 快速开始

```bash
npm run demo    # 运行 Dangling-Else 样例
npm test        # 运行 node:test 测试
```

等价于：

```bash
node --experimental-strip-types examples/dangling-else.ts
node --experimental-strip-types --test test/lr1.test.ts
```

## 项目结构

```
src/grammar.ts    文法表示与解析、First/Follow、minYield（最短终结符产出）
src/lr1.ts        LR(1) 项、闭包、goto、规范项集族（自动机）构建
src/table.ts      ACTION/GOTO 表生成、冲突检测与分类、冲突消解策略
src/conflict.ts   最短输入证据（Dijkstra）、证据模拟验证、解决建议
src/formatter.ts  项集/状态/分析表/冲突报告的可读格式化
src/index.ts      统一导出
examples/dangling-else.ts   运行样例：经典 Dangling-Else 文法
test/lr1.test.ts            node:test 测试（13 个用例）
```

## 运行样例（Dangling-Else）

输入文法：

```
S -> if E then S else S
S -> if E then S
S -> other
E -> cond
```

`npm run demo` 输出（节选）：

```
First 集:
  First(E) = { cond }
  First(S) = { if, other }
Follow 集:
  Follow(E) = { then }
  Follow(S) = { $, else }

规范 LR(1) 项集族：共 17 个状态

State 14:
  S → if E then S • else S   [$, else]
  S → if E then S •   [$, else]
  ----------------------------
  else → state 15

ACTION 表:
  ...
  state 14  [else]  →  shift 15  |  reduce S -> if E then S   <<< 冲突
  ...

冲突检测结果: 共 1 处冲突 (Shift/Reduce: 1, Reduce/Reduce: 0)

================================================================
冲突报告: Shift/Reduce 冲突
================================================================
状态: 14
触发 token: else

在状态 14 下遇到 else 时，以下动作均合法:
  - shift 15
  - reduce S -> if E then S

引发冲突的 LR(1) 项:
  S → if E then S • else S   [$]
  S → if E then S • else S   [else]
  S → if E then S •   [else]

最短输入证据（8 个 token）:
  if cond then if cond then other else
  解析器读完 "if cond then if cond then other" 后处于状态 14，
  下一个 token 为 else 时移进与归约均合法。
证据模拟验证: 通过（非确定性模拟可复现冲突点）

两种选择的含义:
  - SHIFT : else 绑定到最近（内层）的未完结结构
  - REDUCE: 先归约内层结构，else 将归属外层结构

解决建议:
  1. 采用 SHIFT 优先策略（Yacc/Bison 的默认行为）……
  2. 若语义要求 else 归属外层结构，则选择 REDUCE 优先……
  3. 为 token 与产生式声明优先级/结合性（如 %prec、%left/%right/%nonassoc）……
  4. 重写文法消除二义性：拆分为 matched / unmatched 两类语句……
```

> **为什么最短证据需要两个 `if`？**
> 在规范 LR(1) 中，归约项 `[S → if E then S ·, else]` 只有当其前瞻符号含 `else`
> 时才与移进冲突，而这要求内层 `S` 之后紧跟 `else` —— 即它本身位于某个
> `if E then · S else S` 的 then 分支中。因此触发冲突的最短输入是
> `if cond then if cond then other else`（单层 `if cond then other else`
> 在外层状态下归约项前瞻为 `$`，不构成冲突）。LALR(1) 合并状态后才会在单层
> `if` 处出现冲突。

## 测试结果

`npm test`（Node.js 内置 `node:test`，13 个用例全部通过）：

```
✔ 文法解析：终结符/非终结符分类与开始符号
✔ First 集：表达式文法
✔ First 集：含 ε 产生式
✔ Follow 集：表达式文法
✔ LR(1) 闭包：初始项集包含期望的项与前瞻符号
✔ Dangling-Else：恰好检测出 1 处 Shift/Reduce 冲突，触发 token 为 else
✔ Dangling-Else：输出最短输入 token 串证据并通过模拟验证
✔ Dangling-Else：冲突报告包含状态、动作、证据与解决建议
✔ Dangling-Else：resolveConflicts('shift') 选择移进（else 绑定最近的 if）
✔ Reduce/Reduce 冲突：准确定位并给出最短证据
✔ LR(1) 表达式文法：无冲突且含 accept 动作
✔ minYield：每个符号可推导出的最短终结符串
✔ 证据最短性：证据长度等于到冲突状态的最短输入长度
ℹ tests 13
ℹ pass 13
ℹ fail 0
```

## API 示例

```ts
import {
  parseGrammar, buildAutomaton, buildTables,
  diagnoseConflicts, formatConflictReport,
} from "./src/index.ts";

const grammar = parseGrammar(`
S -> if E then S else S | if E then S | other
E -> cond
`);
const automaton = buildAutomaton(grammar);   // 规范 LR(1) 项集族
const tables = buildTables(automaton);       // ACTION/GOTO 表 + 冲突列表
for (const d of diagnoseConflicts(automaton, tables)) {
  console.log(formatConflictReport(d, automaton));
}
```
