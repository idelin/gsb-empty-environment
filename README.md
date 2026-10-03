# Subquery Unnesting / Decorrelation（TypeScript 版）

将低效的相关子查询展开为常规 Join，消除“逐行重复执行子查询”的性能陷阱：

- `WHERE EXISTS (…)` → `SEMI JOIN`（`NOT EXISTS` → `ANTI JOIN`）
- `WHERE expr IN (SELECT …)` → `SEMI JOIN`（`NOT IN` → `ANTI JOIN`）
- 关联谓词（如 `e.dept_id = d.id`）提取为连接键，子查询局部谓词并入 `ON`
- 重写后的逻辑计划进一步翻译为物理计划：**HashSemiJoin / HashAntiJoin**（右表 build、左表 probe）

零依赖，使用 Node.js 内置测试运行器（`node:test`）。要求 **Node ≥ 23.6**（原生 TypeScript 类型擦除，直接运行 `.ts`）。

## 目录结构

```
src/lexer.ts    词法分析
src/parser.ts   递归下降解析（SELECT 子集，含 EXISTS / IN / NOT / JOIN）
src/ast.ts      AST 定义
src/unnest.ts   核心：关联子查询解除（decorrelation）
src/format.ts   AST -> SQL 文本
src/planner.ts  逻辑计划 -> 物理计划（Hash Semi/Anti Join）
src/index.ts    公共入口 rewrite(sql)
demo.ts         运行样例
test/unnest.test.ts  15 个 node:test 用例
```

## 运行

```bash
node demo.ts     # 运行样例
node --test      # 运行测试
```

## 验收用例与运行样例

输入：

```sql
SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)
```

`node demo.ts` 输出（节选）：

```
重写后 SQL:
  SELECT d.* FROM depts d SEMI JOIN emps e ON e.dept_id = d.id
物理执行计划:
  Project [d.*]
    HashSemiJoin keys: (d.id) = (e.dept_id)
      probe:
        SeqScan depts AS d
      build:
        SeqScan emps AS e
```

相关性被消除：子查询不再逐行执行，`emps` 作为 build 侧只扫描一次并构建哈希表。

更多样例（IN / NOT EXISTS / 外层残余谓词）见 `demo.ts`。

## 测试结果

`node --test`（Node v23.9.0）：

```
✔ 验收：相关 EXISTS 重写为 SEMI JOIN，消除相关性
✔ 验收：物理计划为 HashSemiJoin，emps 为 build 侧
✔ SELECT * 在 Semi Join 下只展开为左表列
✔ IN 子查询（非相关）重写为 SEMI JOIN
✔ IN 子查询的局部谓词并入 ON 条件
✔ 相关 IN 子查询同样被解除
✔ NOT EXISTS 重写为 ANTI JOIN / HashAntiJoin
✔ NOT IN 重写为 ANTI JOIN
✔ 外层残余谓词保留在 WHERE（Filter）中
✔ 多个 EXISTS 合取生成多个 Semi Join
✔ 非相关 EXISTS（无条件子查询）重写为 ON TRUE 的 Semi Join
✔ 无子查询的普通查询保持不变
✔ 嵌套子查询被递归解除
✔ 解析器往返：parse -> format 结构稳定
✔ unnest 幂等：重写结果再次重写不变
ℹ tests 15  ℹ pass 15  ℹ fail 0
```

## API

```ts
import { rewrite } from './src/index.ts';

const r = rewrite('SELECT * FROM depts d WHERE EXISTS (...)');
r.rewritten; // 重写后的 SQL 文本
r.planText;  // 物理执行计划（文本树）
r.plan;      // 物理计划 AST
```

## 说明与边界

- `NOT IN` 的 NULL 三值语义需在执行层保证子查询键无 NULL（计划中可追加 `IS NOT NULL` 过滤），本组件聚焦重写规则本身。
- 子查询 FROM 多表会折叠为 `CROSS JOIN` 后再做 Semi Join；FROM 中的派生表暂不支持（解析器会明确报错）。
