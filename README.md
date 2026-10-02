# Projection Pruning（列投影消除与无用字段修剪）

全树依赖字段分析器：自顶向下收集真正被上层算子消费的字段列表，
裁剪底层 Scan 不需读取的多余列，降低内存与 I/O 开销。

## 环境要求

- Node.js ≥ 22.6（使用内置 `--experimental-strip-types` 直接运行 TypeScript，无需编译）
- 测试使用 Node.js 内置测试运行器 `node:test`，零第三方依赖

## 目录结构

```
src/
  expression.ts   # 表达式模型：FieldRef / Literal / BinaryExpr / FuncCall，可报告字段依赖
  operators.ts    # 逻辑计划算子：Scan / Project / Filter / Join
  pruner.ts       # ProjectionPruner：自顶向下驱动整树裁剪
  sample.ts       # 运行样例（对应验收边界）
test/
  pruner.test.ts  # node:test 单元测试
```

## 工作原理

1. `ProjectionPruner.prune(root, topLevelRequired?)` 以根算子的输出列
   （或显式指定的顶层需求）作为初始必需字段集合。
2. 每个算子实现 `applyRequiredColumns(parentRequired)`：
   - **Project**：裁掉上层不消费的输出列，把保留表达式引用的字段向下传递；
   - **Filter**：把谓词引用的字段并入需求后继续下传；
   - **Join**：按左右两侧拆分需求，并强制保留连接键；
   - **Scan**（叶子）：把输出模式压缩为「全表列 ∩ 上层需求」。
3. 修剪是原地进行的，之后 `scan.outputSchema()` 即为实际需读取的列。

## 运行样例

```bash
npm run sample
```

输出：

```
=== 修剪前 ===
Project[id=id, name=name]
  Join[id=user_id]
    L: Scan(users) -> [id, name, col03, ..., col20]   # 20 列
    R: Scan(orders) -> [id, user_id, amount, status, created_at]
users Scan 输出列数: 20

=== 修剪后 ===
Project[id=id, name=name]
  Join[id=user_id]
    L: Scan(users) -> [id, name]
    R: Scan(orders) -> [user_id]
users Scan 输出列数: 2
users Scan 输出模式: [id, name]
```

## 运行测试

```bash
npm test
```

结果：7 个测试全部通过（`# pass 7 / # fail 0`），覆盖：

- 验收边界：20 字段全表 → 顶层需 `id`/`name`、Join 需 `id` → Scan 压缩为 `[id, name]`
- Project 裁剪上层不消费的输出列
- Filter 谓词字段额外向下请求
- 函数调用表达式（如 `upper(name)`）的字段依赖收集
- Join 需求按左右两侧拆分并保留连接键
- 缺省顶层需求 = 根算子全部输出列
- 上层需求中的未知列被安全忽略
