# tri-filter

离线行筛选表达式库（TypeScript）：AND / OR / NOT、六种比较（`eq` `ne` `lt` `lte` `gt` `gte`）、`IS NULL` / `IS NOT NULL`，三值逻辑（true / false / unknown），**只有结果为 true 的行入选**。

## 语义约定

- **未知比较**：字段缺失（`undefined`）与 `null` 一视同仁，任何比较运算遇到它们都得 `unknown`；`IS NULL` 对二者都返回 true。
- **类型错误**：`eq`/`ne` 要求两侧同类型；`lt`/`lte`/`gt`/`gte` 仅支持 number-number 与 string-string；布尔位（AND/OR/NOT 的操作数）只接受布尔值。违反即产生带 AST 路径（如 `$.right.left`）的类型错误。
- **真值表**：Kleene 三值逻辑（`false AND unknown = false`，`true OR unknown = true`，`NOT unknown = unknown`，其余见 `src/logic.ts`）。

## 两种求值器

- `interpret(expr, row)`（`src/interpreter.ts`）：真值表解释器，**不短路**，两个分支都求值，按从左到右的顺序报告最左类型错误及其位置。作为语义基准（oracle）。
- `compile(expr)`（`src/compiler.ts`）：编译为行谓词函数，**可短路**（AND 左 false、OR 左 true 时跳过右分支），被短路分支**绝不被求值**（其字段不读取、其类型错误不产生）；字面量子表达式在编译期常量折叠。
- `filterRows(rows, pred)`（`src/filter.ts`）：只保留结果为 `true` 的行；某行求值出类型错误时携带行号快速失败。

## 等价性核对（test/run-tests.ts + test/oracle.ts）

对每个 (表达式, 行) 组合断言：

1. `compile(expr)(row)` 与 `interpret(prune(expr, row), row)` 完全一致（值相同，或错误路径与消息相同）——`prune` 把被短路分支替换为中性字面量，即“活分支上的真值表语义”。
2. 若 `interpret(expr, row)` 无错误，则三者值一致（剪枝不改变无错结果）。
3. 若解释器报错且错误位置在活分支上，则编译器报告**相同位置**；错误在死分支时编译器可返回确定值（短路抑制）。

测试含：真值表全组合、比较/IS NULL 单元用例、错误位置用例、getter 计数证明短路分支零求值、4000 个随机表达式 × 49 种行（缺字段/null/number/string/boolean 笛卡尔积）共 196,000 组交叉核对。

## 运行

```sh
npm test   # tsc 编译 + 运行全部测试（typescript 已随 node_modules 离线提供）
```
