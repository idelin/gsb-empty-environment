# query-ast

TypeScript SQL 查询 AST 库：以**参数节点身份**绑定值，渲染为 `$n` 占位符 + 位置参数数组，并支持"渲染 → 再解析"的绑定核对。

要求 Node.js ≥ 22.6（利用原生类型擦除直接运行 `.ts`）。

```bash
node --test 'test/*.test.ts'   # 运行测试
```

## 绑定语义

- **按节点身份绑定**：`param(1)` 调用两次得到两个节点，即使值相同也**不合并**，各占一个占位号。
- **同节点复用**：同一个 `ParamNode` 实例在查询中出现多次，只分配**一个**占位号，值在 params 数组中只出现一次。
- **确定遍历顺序**：占位号按固定顺序分配——SELECT → FROM 子查询 → WHERE（深度优先、从左到右，含 IN 子查询递归）→ ORDER BY → LIMIT。嵌套子查询在其出现位置按深度优先展开编号。
- **原始文本不能插值**：`raw()` 片段原样输出，不接受、也不允许包含占位符（`?` 或 `$n` 会在构造时抛错）。
- **空列表**：`IN ()` 渲染为常量假 `(1 = 0)`，`NOT IN ()` 渲染为常量真 `(1 = 1)`，均不产生绑定；空 `AND`/`OR` 同理渲染为 `(1 = 1)`/`(1 = 0)`。
- **字段名转义**：标识符以双引号包裹，内嵌双引号翻倍（`we"ird` → `"we""ird"`），点分路径逐段转义（`u.name` → `"u"."name"`）。

## 示例

```ts
import { select, field, param, eq, and, inSubquery, render, verifyRoundTrip } from 'query-ast';

const shared = param(42);
const inner = select(field('uid')).from('orders').where(eq(field('total'), param(100)));
const q = select(field('id'))
  .from('users')
  .where(and(eq(field('status'), param('active')), inSubquery(field('id'), inner), eq(field('age'), shared)))
  .limit(shared);

render(q);
// sql:    SELECT "id" FROM "users" WHERE ("status" = $1 AND "id" IN (SELECT "uid" FROM "orders" WHERE "total" = $2) AND "age" = $3) LIMIT $3
// params: ['active', 100, 42]

verifyRoundTrip(q).ok; // true —— 渲染后再解析，逐位核对占位号与绑定值
```

## 渲染-再解析核对

`verifyRoundTrip(query)` 执行：渲染 → 用内置解析器把 SQL 解析回 AST → 按同一确定遍历顺序配对中国参数节点与占位符，检查：

- 每个占位号 `$n` 绑定的值 `params[n-1]` 与对应参数节点的值一致；
- 同一参数节点的所有出现映射到同一占位号，不同节点不共享占位号；
- params 数组长度等于不同参数节点数，且占位号 `1..N` 连续无空缺。

返回 `VerifyReport { ok, sql, params, bindings, errors }`，`bindings` 给出逐位置的对应记录。

## 模块结构

- `src/ast.ts` — AST 节点与构造器（`param`/`field`/`raw`/`select`/`eq`/`and`/`or`/`inList`/`inSubquery` …）
- `src/render.ts` — 渲染器：身份去重的占位号分配、标识符转义、空列表常量
- `src/parse.ts` — 渲染方言的递归下降解析器（占位符 → 数字引用）
- `src/verify.ts` — 遍历顺序参数收集 + `verifyRoundTrip` 核对
