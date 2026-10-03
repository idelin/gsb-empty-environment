# Physical Property Enforcement: Sort / Order (TypeScript)

自底向上跟踪、自顶向下强制数据物理排序特性的迷你查询优化器。

- **自顶向下**：每个算子向子节点声明它*要求*（required）的排序属性
  （`ORDER BY` 要求输出有序、`StreamAggregate` 要求按分组键有序、`MergeJoin` 要求两侧按连接键有序）。
- **自底向上**：每个物理算子上报它*交付*（delivered）的排序属性
  （`IndexScan` 交付索引键顺序，`SeqScan` 无序，`Sort` 交付其排序键）。
- **Enforcer**：当子节点交付的顺序不满足父节点要求（前缀匹配规则）时，
  插入物理 `Sort` 节点；若底层索引已满足（如 `ORDER BY u.id` + `IndexScan(u.id)`），
  则果断省去 Sort，避免昂贵的外部排序。

## 环境要求

Node.js ≥ 23.6（原生 TypeScript 类型擦除，无需编译）。仓库根目录含 `.nvmrc`（`nvm use` 即可）。

## 运行

```bash
npm test     # node --test（Node 内置测试运行器）
npm run demo # 运行 examples/demo.ts，打印物理计划
```

## 结构

- `src/properties.ts` — Ordering 类型与前缀满足规则 `satisfies()`
- `src/catalog.ts` — 表/索引元数据（users 有 `(id)`、`(id,name)` 索引；orders 仅有主键）
- `src/plan.ts` — 逻辑/物理计划节点定义与遍历工具
- `src/optimizer.ts` — 核心：`enforce(node, required)` 双向传递排序属性，必要时插入 `Sort`
- `src/parser.ts` — 迷你 SQL 解析器（SELECT/JOIN/WHERE/GROUP BY/ORDER BY）
- `src/format.ts` — 物理计划树形打印
- `test/optimizer.test.ts` — 11 个用例，含验收边界
- `examples/demo.ts` — 运行样例

## 验收边界

查询 `SELECT u.id, u.name FROM users u ORDER BY u.id`，底层命中 `IndexScan(users.id)`：

```
Project(u.id, u.name)
└─ IndexScan(users u, index=users_pkey)
--> physical Sort operators: 0  (eliminated!)
```

优化器识别数据已有序，物理计划中不含任何 Sort 算子（见测试
`ACCEPTANCE: ORDER BY u.id over IndexScan(u.id) eliminates the Sort`）。
