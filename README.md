# row-mask-policy-engine

一个纯 TypeScript 的行可见性 + 字段遮蔽策略引擎，零依赖，使用 Node.js 内置
`node:test` 测试。策略同时控制「哪些行可见」与「可见行上哪些字段被遮蔽」，
输出遵循固定公开 schema，且不会在过滤顺序、排序、聚合或错误信息中泄漏
被隐藏行或被遮蔽字段的任何信息。

## 运行环境

- Node.js >= 22.6（使用 `--experimental-strip-types` 直接运行 TypeScript 源码；
  Node >= 23.6 可去掉该 flag）。源码只使用可擦除类型语法，无需编译步骤。
- 可选：`npm i -D typescript && npx tsc --noEmit` 做完整类型检查（`tsconfig.json` 已配置）。

```sh
npm test    # node --experimental-strip-types --test test/*.test.ts
npm run demo
```

## 设计

### 策略模型（`src/policy.ts`）

- 属性必须**显式声明**，分 `subject.*`（调用方上下文，直接以普通对象传入，
  **不接任何外部身份服务**）与 `row.*`（行上的策略判定列）两个作用域。
- 属性类型只有两种：`integer` 与 `enum`（**有限集合**，声明时穷举取值）。
- 条件语言只有三种，编译期（`compilePolicy`）静态校验：
  - `in`：枚举属性 ∈ 有限字面量集合（集合外取值直接拒绝）；
  - `int`：整数属性与整数字面量或另一整数属性比较（`eq/ne/lt/lte/gt/gte`，
    非整数字面量如 `1.5` 直接拒绝）；
  - `and` / `or` / `not`：布尔组合。
- 引用未声明属性、类型不匹配、遮蔽字段不在公开 schema 内等都在编译期报错。

### 执行（`src/engine.ts`）

`executeQuery(policy, subject, table, query)` 的处理顺序：

1. **静态校验查询**：`select`/`sortBy`/`aggregates` 只能引用公开字段；
   按可遮蔽字段排序或聚合会在读取任何数据前被拒绝（防止经由排序位置或
   聚合结果反推被遮蔽原值）。
2. **逐行独立判定可见性**：每行的可见性只依赖自身与 subject，与输入顺序、
   其他行无关；`limit` 在过滤**之后**才作用于可见行。
3. **投影 + 遮蔽**：可见行按公开 schema 投影，命中遮蔽规则的单元格替换为
   全局唯一冻结标记 `MASKED`（`src/mask.ts`）。
4. **排序在遮蔽之后**进行，排序键永远拿不到原始值。
5. **聚合只统计可见行**（且与 `limit` 无关）；数值聚合跳过 `null`。

### 输出 schema（固定公开结构）

```json
{ "columns": ["..."], "rows": [{ "...": "value | null | {\"kind\":\"masked\"}" }], "aggregates": { "name": 0 } }
```

- 永远只有 `columns` / `rows` / `aggregates` 三个键——**没有**隐藏行计数、
  总数或任何过滤元数据。
- `null`（数据本身为空）与 `{"kind":"masked"}`（被策略遮蔽）严格区分。

### 失败封闭（fail-closed）

- 行属性值非法（类型不符或超出枚举有限集）时：该行按**不可见**处理、
  遮蔽规则按**命中**处理，不抛错、不输出，避免错误通道泄漏行内容。
- subject 属性非法时抛 `PolicyError`，错误信息只含属性名，**绝不包含原值**。

## 安全性质（由 `test/security.test.ts` 验证）

- 输出结构固定，无任何隐藏行元数据；
- 在输入中插入/重排不可见行，输出逐字节不变；
- 被隐藏行的内容不出现在序列化输出中；
- 按可遮蔽字段排序、聚合可遮蔽字段均在读取数据前被拒绝；
- 被遮蔽值无法影响行顺序（排序发生在遮蔽之后）；
- 错误信息不回显 subject/行的原始值；
- 非法行属性失败封闭，不抛错也不泄漏；
- `null` 与遮蔽标记在结构与 JSON 序列化层面都可区分。

## 项目结构

```
src/policy.ts   策略类型 + 编译期校验
src/eval.ts     条件求值（只读已声明属性）
src/engine.ts   查询校验与执行（过滤→遮蔽→排序→聚合→limit）
src/mask.ts     MASKED 标记（冻结单例）
src/errors.ts   PolicyError
demo/run-sample.ts  运行样例
test/           node:test 测试（31 个用例）
```

## 测试结果

```
# tests 31
# pass 31
# fail 0
```
