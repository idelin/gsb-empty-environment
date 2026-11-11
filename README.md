# 参数化查询结构防注入

一个零依赖的 TypeScript 查询构造包。它让查询网关在**不执行任何数据库语句**的前提下，
证明用户提供的值只能进入带类型的占位参数，无法改变查询的逻辑结构。

## 威胁模型与边界

- 包的输入只接受由受信代码通过工厂函数构造的查询 AST，不接受任意 SQL 文本。
- 标识符（表、列、别名）必须出现在调用方提供的固定白名单中，否则构造校验直接失败。
- 用户值只能通过 `param(name, type)` 进入 AST，渲染为 `$1`、`$2` 形式的占位符；
  渲染产物中绝不内联字符串字面量。
- 包**不提供** `rawSql`、字符串拼接或转义拼接入口，从 API 表面杜绝注入通道。
- 支持嵌套 `SELECT`、各类 `JOIN`、`EXISTS` / 标量子查询、`GROUP BY` / `HAVING`
  以及有界 `ORDER BY` 与有界 `LIMIT`。

## 工作流程

`buildAndVerify(ast, whitelist)` 依次执行：

1. 白名单与作用域校验：解析表/列引用，处理相关子查询的外层作用域与派生表投影。
2. 规范化：去重重复参数到同一个槽位，把空 `IN` 集合归一成常量谓词，生成 canonical 树。
3. 渲染：全括号化输出 SQL，所有值位置输出占位符，标识符使用双引号并转义。
4. 独立解析：用与渲染器完全独立的词法分析器和递归下降解析器把 SQL 还原成结构树，
   与 canonical 树做深比较，保证往返一致。
5. 参数不变性：对每个槽位注入多组对抗性探针（含 `OR 1=1`、`'; DROP TABLE ...`、
   空串、超大整数等），重新解析并剥值后比较，证明任意参数替换都不改变 AST。

## 用法

```ts
import {
  and,
  asc,
  buildAndVerify,
  bindValues,
  cmp,
  col,
  desc,
  inList,
  param,
  select,
  table,
} from './index.ts';

const whitelist = {
  tables: [
    { name: 'users', columns: ['id', 'name', 'tenant_id', 'status'] },
    { name: 'orders', columns: ['id', 'user_id', 'amount'] },
  ],
};

const query = select({
  selectList: [col('id'), col('name')],
  from: table('users', 'u'),
  where: and(
    cmp('=', col('u', 'tenant_id'), param('tenantId', 'integer')),
    inList(col('u', 'status'), [
      param('status', 'string'),
      param('status', 'string'), // 重复参数复用同一个槽位
    ]),
  ),
  orderBy: [desc(col('u', 'id')), asc(col('u', 'name'))],
  limit: 100,
});

// 不触碰数据库，仅做静态结构证明
const report = buildAndVerify(query, whitelist);

// report.sql 中所有值都是 $n 占位符
const orderedValues = bindValues(report.slots, {
  tenantId: 7,
  status: 'active',
});
```

## 关键语义

- **重复参数**：同名 `param` 映射到唯一槽位，重复出现复用同一占位符；类型冲突会被拒绝。
- **空 `IN` 集合**：`x IN ()` 归一为 `FALSE`，`x NOT IN ()` 归一为 `NOT FALSE`，
  不产生占位符，避免数据库方言差异。
- **子查询作用域**：`EXISTS` / 标量子查询可引用外层作用域（相关子查询）；
  `IN (subquery)` 与派生表默认隔离，越界引用会被拒绝。
- **有界排序**：`ORDER BY` 最多 `MAX_ORDER_ITEMS`（10）项，`LIMIT` 上限 `MAX_LIMIT`（10000）。
- **类型绑定**：`bindValues` 在运行时按 `string | integer | number | boolean | date | null`
  做类型守卫，缺失或类型不符都会抛出 `QueryStructureError`。

## 测试

```sh
npm test
```

测试覆盖渲染往返、参数不变性探针、重复参数、空 `IN`、相关/隔离子查询作用域、
白名单拒绝、有界排序以及独立解析器对原始 SQL 注入的拒绝。
