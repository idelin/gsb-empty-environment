# Subquery Unnesting / Decorrelation (TypeScript)

将低效的相关子查询展开为常规 Join：把 `WHERE EXISTS` / `NOT EXISTS` / `IN` / `NOT IN`
子查询重写为 **SEMI / ANTI JOIN**，并由物理规划器选择 **Hash Semi/Anti Join**，
消除"外层每行都重复执行一次子查询"的性能陷阱。

## 快速开始

```bash
npm run demo    # 运行样例（node demo/run.js）
npm test        # 运行测试（node tests/decorrelate.test.js）
npm run build   # 用 tsc 重新编译 src/ -> lib/（需要安装 typescript）
```

> 说明：本仓库在无网络环境下开发，`lib/` 是与 `src/` 严格对应的编译产物，已随仓库提交，
> 因此不安装任何依赖即可运行 demo 和测试。测试基于 Node 内置 `node:assert`；
> 在 Node.js >= 18 上也可用 `node --test` 运行（自动切换到内置 `node:test` runner）。

## 验收用例

输入：

```sql
SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)
```

重写结果（相关性已消除）：

```sql
SELECT d.* FROM depts d SEMI JOIN emps e ON d.id = e.dept_id
```

物理计划（高效 Hash Semi Join）：

```
Project
  HashSemiJoin  [d.id = e.dept_id]
    SeqScan on depts d
    SeqScan on emps e
```

执行统计对比（demo 样例数据）：

| 执行方式 | 子查询执行次数 | 扫描行数 |
| --- | --- | --- |
| 相关子查询（嵌套循环基线） | 4（每个外层行一次） | 18 |
| 解除相关后（Hash Semi Join） | 0 | 9 |

## 架构

- `src/ast.ts` — SQL 子集的 AST 定义
- `src/parser.ts` — 手写分词器 + 递归下降解析器
- `src/decorrelate.ts` — 核心重写规则（解除相关）
- `src/planner.ts` — 物理计划选择（等值键 → Hash Semi/Anti Join）
- `src/engine.ts` — 内存执行引擎：基线（逐行执行子查询）vs 重写后计划，并采集执行统计
- `demo/run.js` — 4 个场景的端到端样例
- `tests/decorrelate.test.js` — 16 个内置断言测试

## 重写规则

| 原形式 | 重写为 |
| --- | --- |
| `WHERE EXISTS (sub)` | `SEMI JOIN` |
| `WHERE NOT EXISTS (sub)` | `ANTI JOIN` |
| `WHERE x IN (sub)` | `SEMI JOIN`（键：`x = sub` 输出列） |
| `WHERE x NOT IN (sub)` | `ANTI JOIN` |

子查询 `WHERE` 中的谓词按作用域分类：

- `inner.col = outer.col`（等值相关谓词）→ Join 键；
- 仅引用内层表的谓词 → 下推为 build 侧的 `Filter`；
- 其他情况（非等值相关、`OR` 中的子查询、嵌套子查询、多表 FROM 等）→ 安全兜底，
  保留为残余谓词，不做重写。

SEMI JOIN 只输出外层行且每行至多一次，天然避免了 IN→INNER JOIN 需要的去重问题。

## 已知边界

- 外层查询暂限单表 FROM；
- `NOT IN` 的 NULL 三值语义未特殊处理（假设键列非空）；
- 不支持子查询内含聚合 / GROUP BY / LIMIT 的解除相关（会安全地保留原样）。
