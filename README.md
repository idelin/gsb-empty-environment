# 三值逻辑内存查询引擎与谓词下推验证

TypeScript 实现的内存查询引擎：筛选、投影、连接（inner/left/right/full），
布尔采用**真 / 假 / 未知**三值逻辑，并用小表穷举验证谓词下推的等价性、
给出非法下推的反例。

## 运行

```bash
npm test        # 等价于 node --experimental-loader ./tools/ts-loader.mjs test/run.ts
```

环境只有 Node.js（≥16.12），无法安装 tsc，因此 `tools/ts-loader.mjs`
在加载时剥离类型注解。源码遵循受限 TS 子集（类型名首字母大写、类型导入
一律 `import type`、不使用泛型调用等），如需完整类型检查可在有网环境
执行 `tsc --noEmit`（`tsconfig.json` 已备好）。

## 语义约定（src/）

- **三值逻辑** `trivalent.ts`：SQL 风格真值表。`U AND F = F`、`U OR T = T`，
  其余含 UNKNOWN 的运算多数得 UNKNOWN。
- **空值比较** `expr.ts`：任何一侧为 NULL 的比较（含 `NULL = NULL`）都得
  UNKNOWN；`filter` 只保留求值为 **TRUE** 的行，FALSE 与 UNKNOWN 都丢弃。
- **外连接补空** `operators.ts`：未匹配侧以 NULL 补齐，补出的 NULL 与数据
  中的真实 NULL 走完全相同的求值路径（测试第 3 节专门验证二者行为一致）。
- **连接键**：NULL 键互不匹配（`NULL = NULL` 为 UNKNOWN，不算匹配）。

## 谓词下推规则（src/pushdown.ts）

设 σ 为筛选、⋈/⟕/⟖/⊻ 分别为内/左/右/全外连接，p 只引用左表列、q 只引用右表列：

| 规则 | 变换 | 合法性 |
|---|---|---|
| P1 | σp(σq(R)) → σp∧q(R) | 合法（合取可交换、可结合） |
| P2 | σp(L ⋈ R) → σp(L) ⋈ R | 合法（内连接任意一侧） |
| P3 | σp(L ⟕ R) → σp(L) ⟕ R | 合法（左连接的保留侧） |
| P4 | σq(L ⟖ R) → L ⟖ σq(R) | 合法（右连接的保留侧） |
| X1 | σq(L ⟕ R) → L ⟕ σq(R) | **非法**：q 引用补空侧列 |
| X2 | σp(L ⟖ R) → σp(L) ⟖ R | **非法**：同理（非保留侧） |
| X3 | σp(L ⊻ R) → 推入任一侧 | **非法**：全外连接两侧都补空 |

合法性的直觉：保留侧的行**全部**出现在连接结果中，其上的谓词在连接前后
过滤，被筛掉的行集合相同（三值下 UNKNOWN 一律不保留，行为一致）。而补空侧
的行先被过滤会改变"未匹配"集合，从而改变补 NULL 的行——补出的 NULL 让谓词
得 UNKNOWN 被上层筛掉，与"行根本不存在"是两回事。

## 穷举验证（src/equiv.ts, test/run.ts）

在值域 `{NULL, 0, 1, 2}`、每表 2 列、0–2 行（允许重复行，包语义）上枚举
全部 **23,104** 种表对，对 4 种连接 × 6 类谓词（左侧、右侧、双侧合取、
跨表谓词）共 24 组配置验证 `execute(σp(join)) ≡ execute(pushDown(σp(join)))`
（输出按多重集比较）。任何反例都会打印具体表内容。

## 非法下推反例（src/counterexamples.ts）

- **X1** `σ_{y>1}(L ⟕ R)` vs `L ⟕ σ_{y>1}(R)`：L 中未匹配的行本应补 NULL
  后由上层 UNKNOWN 筛掉；下推后 R 侧行被提前过滤，左行反而带着补空存活，
  结果由 0 行变 2 行。
- **X2** `σ_{a>0}(L ⟖ R)` vs `σ_{a>0}(L) ⟖ R`：对称情形，未匹配右行补的
  左 NULL 本应被筛掉，下推后却保留下来。
- **X3** `σ_{a>0}(L ⊻ R)` vs `σ_{a>0}(L) ⊻ R`：左行被提前过滤后，原本能
  匹配的右行变成未匹配行，以补 NULL 形式"复活"在结果中。

## 目录

```
src/trivalent.ts        三值逻辑
src/expr.ts             表达式与求值（空值比较规则）
src/operators.ts        筛选 / 投影 / 连接
src/plan.ts             查询计划与执行器
src/pushdown.ts         谓词下推优化器（仅应用合法规则）
src/equiv.ts            小表穷举等价性检查
src/counterexamples.ts  非法下推反例
test/run.ts             全部测试（54 项）
tools/ts-loader.mjs     受限 TS 加载器（无 tsc 环境下运行）
```
