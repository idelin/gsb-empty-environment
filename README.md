# json-template-engine

一个 TypeScript 实现的**安全、可增量更新的 JSON 模板引擎**。模板本身是一棵 JSON 树，
对输入的唯一访问方式是**路径读取**与**白名单函数调用**；引擎维护输出片段树与
**输出 → 模板 + 输入**的双来源映射，数据修改经**事务**提交后增量发布，失败整体回滚。

## 运行环境

- Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译；开发用的是 23.9.0，见 `.nvmrc`）
- 无第三方依赖

```bash
nvm use            # 或任何 >= 22.6 的 Node
npm test           # node --test "tests/*.test.ts"（Node 内置测试运行器）
npm run demo       # 运行 examples/demo.ts
```

## 模板能力

模板是 JSON 兼容树；字符串中的 `${...}` 是表达式（路径、白名单函数调用、字面量）：

```jsonc
{
  "title": "Hello ${user.name}",          // 插值；整串只有一个 ${} 时保留原始类型
  "esc":    "\\${literal} \\\\",           // 转义：\${ 保留字面量，\\ 是反斜杠
  "users":  { "$for": "users", "as": "u", "index": "i",
              "body": { "name": "${upper(@u.name)}", "pos": "${@i}" } },
  "banner": { "$if": "user.vip", "then": "VIP", "else": "basic" },
  "nick":   { "$get": "user.nick", "missing": "null" },
  "score":  { "$call": "div", "args": ["user.points", "user.games"] },
  "fixed":  { "$lit": [1, 2] }
}
```

- **指令**：`$for`（可嵌套，`@var` 引用循环变量）、`$if/then/else`、`$get`、`$call`、`$lit`。
- **缺失策略**（`missing`，引擎级默认 `onMissing`，缺省 `error`）：
  `error` 抛错 / `null` 产出 null / `empty` 空串 / `omit` 删除该键或数组元素 / `keep` 保留 `${...}` 原文。
  **缺失与显式 `null` 严格区分**：显式 null 照常渲染，不触发缺失策略。
- **白名单函数**：`upper lower trim len join concat add sub mul div mod eq ne gt gte lt lte not fmt`
  及特殊形式 `default` `defined` `and` `or`（短路）。未注册函数、函数抛错（如除零）
  都会带**模板路径**报错；可通过 `EngineOptions.functions` 追加白名单。

## 额度限制（防爆）

- `maxExpansions`：**循环迭代与函数调用共享**的总展开预算（默认 10000）；
- `maxOutputNodes`：输出节点上限，防输出爆炸（默认 100000）；
- `maxDepth`：模板嵌套深度上限，防递归模板（默认 200）。
超限抛 `BudgetError`，事务内超限同样回滚。

## 实体身份与增量更新

- 输入树每个节点拥有**稳定实体 ID**；身份只按 ID 判定，**同内容节点不是同一身份**
  （两个相同的 `{name:"b"}` 重排后仍是两个不同实体）。
- 渲染产出**片段树**（fragment tree），每个片段记录读依赖（值依赖 / 结构依赖 / 深依赖）。
- 事务提交时按变更集失效最小片段子树：`$for` 片段按**实体 ID 对账**，
  纯重排直接复用并重排子片段，内容未变的实体片段原样保留（引用相等）。

## 事务与双来源

```ts
const eng = new Engine(template, data, { onMissing: "error" });
eng.output;       // 已发布输出
eng.provenance;   // { "/users/0/name": { template: "/users/body/name", inputs: ["/users/0/name"] } }

eng.begin()
  .move("/users", 0, 2)        // 重排（实体 ID 跟踪）
  .insert("/users", 1, {...})  // 变长
  .set("/users/0/nick", "b")   // 缺失 -> 出现
  .commit();                   // 原子发布 output + provenance；任何函数失败全部回滚

eng.verify();     // 与完整重新渲染（含来源表）逐位对比
```

- 来源表同时定位**模板路径**与**输入路径**；错误（`TemplateError`/`MissingError`）
  携带 `tpath` 与 `ipath` 字段。
- 输入为 JSON 兼容树：对象键无序（`Map` 存储）、数组有序。
- 事务 API：`set / insert / remove / move`，`commit()` 失败自动撤销全部数据修改，
  已发布状态不变；也可 `rollback()` 手动放弃。

## 目录结构

- `src/json.ts` — JSON 类型、深比较、路径解析
- `src/store.ts` — 实体化数据存储（稳定 ID、变更集、撤销日志）
- `src/expr.ts` — 表达式解析（路径 / 调用 / 字面量）
- `src/template.ts` — 模板编译（指令、`${}` 扫描与转义）
- `src/functions.ts` — 白名单函数
- `src/render.ts` — 片段渲染、增量刷新、来源表收集
- `src/engine.ts` — 引擎与事务
- `tests/` — Node 内置测试（32 个）；`examples/demo.ts` — 运行样例

## 测试结果

```
✔ 32 tests, 0 failed（node --test，Node v23.9.0）
```

覆盖：插值/转义/类型保留、循环嵌套与 `@index`、条件与省略、五种缺失策略、
缺失 vs 显式 null、白名单函数与嵌套调用、共享展开预算、输出爆炸与递归深度上限、
纯重排片段复用（含同内容实体）、嵌套移动、变长数组、缺失出现/消失、
来源随实体迁移、函数失败/预算超限回滚、增量结果与完整重渲染逐位一致（含来源表）。
