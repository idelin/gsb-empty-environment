# Late Materialization 投影算子

OLAP 分析型查询优化：在查询前中期仅加载**过滤列**与**排序列**生成选择向量，
直到 ORDER BY + LIMIT 收敛出最终胜出的少数物理行号后，才回表读取大字符串等昂贵列。

## 执行流程

以 `SELECT * WHERE a > 10 ORDER BY b LIMIT 10`（100 列 × 10000 行）为例：

1. **过滤**：仅全扫列 `a`，生成选择向量（selection vector）。
2. **排序**：仅对选中行读取列 `b`，排序并取 LIMIT 10，得到 10 个胜出 rowId。
3. **物化**：此时才按胜出 rowId 读取其余 98 列，每列仅 10 个单元格。

IO 对比（单元格读取数）：

| 执行方式 | IO |
|---|---|
| 早期物化（Eager） | 1,000,000（100 列全量） |
| 延迟物化（Late） | ~19,874（a 全扫 + b 按选择向量 + 98 列 × 10） |
| **节省** | **~98%** |

## 文件结构

- `src/columnar.ts` — 列式存储，按单元格统计 IO
- `src/lateMaterialization.ts` — 延迟物化执行器 + 早期物化基线
- `src/fixture.ts` — 100 列测试表构建（a/b 数值列 + 98 个 1KB 大字符串列）
- `src/demo.ts` — 运行样例
- `test/lateMaterialization.test.ts` — Node.js 内置测试（node:test）

## 运行

```bash
npm run demo   # 运行样例，打印 IO 对比
npm test       # 运行测试（Node >= 22.6，原生 TypeScript 类型擦除）
```
