# Projection Pruning（列投影消除与无用字段修剪）

TypeScript 实现的全树依赖字段分析器：自顶向下收集真正被上层算子消费的字段列表，
裁剪底层 Scan 不需读取的多余列，降低内存与 I/O 开销。

## 设计

- `src/schema.ts` — 字段 / 模式定义，含 20 列宽表构造器 `makeWideSchema`。
- `src/operators.ts` — 计划算子树：`Scan`（叶子）、`Project`、`Filter`、`Join`。
  每个算子声明自身需要从孩子消费的字段（join key、谓词引用、投影表达式引用）。
- `src/pruner.ts` — `ProjectionPruner.prune(root, needed)` 自顶向下传播需求字段集合：
  - `Project`：消除输出列无人消费的表达式（投影消除），仅向下传递被保留表达式的引用；
  - `Filter`：谓词引用字段强制保留，其余透传上层需求；
  - `Join`：上层需求按左右孩子输出模式拆分，再叠加各自 join key；
  - `Scan`：终点，输出模式被强制压缩为被需要的列（无用字段修剪）。
  返回 `PruneReport`：每个 Scan 修剪前后列名、被消除的投影、累计裁剪列数。

## 验收场景

20 列宽表 `users`，顶层 `Project` 只需 `id`、`name`，中间 `Join` 以 `id` 为连接键。
修剪后 `Scan(users)` 输出模式被压缩为 `[id, name]`（裁掉 18 列），右表 `Scan(dept)` 仅留 `[id]`。

## 运行

要求 Node.js >= 22.6（`--experimental-strip-types`，23.6+ 可省略该 flag）。

```bash
npm test    # Node.js 内置测试运行器（node:test），9 个用例
npm run demo  # 打印修剪前后全树模式与修剪报告
```

## 测试结果

```
ℹ tests 9
ℹ pass 9
ℹ fail 0
```

样例输出（`npm run demo`）：

```
=== 修剪后（After Pruning） ===
Project  -> [id, name]
Join     -> [id, name, id]
Scan     -> [id, name]
Scan     -> [id]

Scan(users): 20 列 -> 2 列 [id, name]，裁掉 18 列
Scan(dept): 3 列 -> 1 列 [id]，裁掉 2 列
```
