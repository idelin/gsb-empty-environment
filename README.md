# fixpoint-rule-engine

在 JSON 兼容树上按**前置条件 + 优先级**反复应用修复规则，直到**不动点**或**预算耗尽**；
每一步修改都记入可逆日志，出现**冲突**或**振荡**时立即停止并列出证据。

## 语义

- 输入为 JSON 兼容树：**对象键无序、数组有序**。遍历与状态指纹均与键顺序无关
  （遍历时对象键按字典序、数组按下标序；指纹为键排序后的规范化串），输出保留原键序。
- 每条规则：`precondition`（不满足则**绝不执行**）、`priority`（数值小者优先）、`fix`（返回替换值）。
- 每步选择"优先级最高、路径最小"的候选；所有修改定位到 RFC 6901 路径（如 `/items/0/name`）。
- **冲突**：同一路径上有多条不同规则同优先级可应用 → 停止，`conflict` 证据列出全部竞争者。
- **振荡**：规范化状态指纹重复出现（如规则 A: `x→y` 与 B: `y→x`，含长度 1 的恒等环）→ 停止，
  `oscillation` 证据给出环的起点步号、环内全部步骤与涉及规则。
- **预算**：步数达到 `budget` 仍未到不动点 → 以 `budget-exhausted` 终止。
- **回滚**：引擎从不改动调用方输入（工作副本为深拷贝、替换为不可变操作），
  `rollback()` 按引用返回**原始输入对象本身**，而非规范化后的副本；
  每步日志含 `before`/`after`，逆序回放即可逐步撤销。

## 运行

需要 Node ≥ 22.6（利用内置 TypeScript 类型剥离，无需编译；仓库内 `.nvmrc` 指定 v23.9.0）：

```sh
nvm use          # 或任意 >= 22.6 的 Node
npm test         # node --test 'test/*.test.ts'
npm run demo     # node examples/demo.ts
```

## 运行样例（`npm run demo` 摘要）

```
=== 1. fixpoint (A: x->y) ===
status: fixpoint
  step 1 [A:x->y prio=1] /items/0/name: "x" -> "y"
  step 2 [A:x->y prio=1] /items/2/name: "x" -> "y"
output: {"items":[{"name":"y"},{"name":"ok"},{"name":"y"}]}
rollback() === input1: true

=== 2. oscillation (A: x->y vs B: y->x) ===
status: oscillation
  step 1 [A:x->y prio=1] /v: "x" -> "y"
  step 2 [B:y->x prio=2] /v: "y" -> "x"
  oscillation: cycle of 2 step(s) from seq 1, rules: A:x->y, B:y->x

=== 3. conflict (equal priority, same path) ===
status: conflict
  conflict at /v, value="x"
    competitor: to-y (priority 1)
    competitor: to-z (priority 1)

=== 4. budget exhausted (budget=4) ===
status: budget-exhausted
  step 1..4 [grow] (root): [] -> ["x"] -> ... -> ["x","x","x","x"]

=== 5. precondition not satisfied -> no-op ===
status: fixpoint
output: {"v":"untouched"}
```

## 测试结果（`npm test`，Node v23.9.0）

```
✔ fixpoint: rules apply until no precondition holds, steps journaled with paths
✔ priority: higher-priority rule fires first regardless of rule list order
✔ precondition: rule never fires when its precondition is unsatisfied
✔ oscillation: rule A (x->y) vs rule B (y->x) is detected with evidence
✔ oscillation: a no-op fix is a length-1 cycle and is also detected
✔ conflict: distinct rules with equal priority at the same path stop the run
✔ budget: run terminates when the step budget is exhausted
✔ rollback: restores the original input object by identity, not a normalized copy
✔ arrays are ordered: fixes apply in index order
✔ object keys are unordered: key insertion order affects neither output identity nor cycle detection
✔ paths locate errors precisely in nested structures
✔ journal is reversible: undoing steps in reverse restores the start state
ℹ tests 12  ℹ pass 12  ℹ fail 0
```

## 结构

- `src/json.ts` — JSON 树模型、深拷贝、规范化指纹、路径定位/不可变替换、确定性遍历
- `src/engine.ts` — 规则类型与 `runRules`：候选收集、优先级裁决、冲突/振荡检测、预算、日志与回滚
- `test/engine.test.ts` — Node 内置 `node:test` 测试（12 例）
- `examples/demo.ts` — 五种终止情形的运行样例
