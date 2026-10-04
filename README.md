# Lazy Columnar Query Pipeline

一个有限查询管道的 TypeScript 实现：数据源按批提供列，执行器先在窄列上
筛选行号（选择向量），宽列延迟到最终投影阶段才按需异步加载。算子只有
`filter`、`project` 和单键稳定 `sort`。不依赖任何数据库。

## 运行

需要 Node ≥ 22.6（使用原生 TypeScript 类型擦除运行，无需编译）：

```sh
npm test        # node --test 'tests/*.test.ts'
npm run example # node examples/demo.ts
```

## 物理布局

每个批次（`RowBatch`）占据缓冲池中的一个物理槽位：

- **行标识**：`rowIds: Uint32Array` + `rowVersions: Uint32Array`，行有稳定
  rowID 与版本；同一 rowID 的不同版本是不同的行。
- **窄列**（`src/column.ts` 的 `ColumnVector`）：随批次内联到达，布局为
  密集值缓冲 `values[]` + 有效性位图 `validity`（每行 1 bit，0 = null）。
- **宽列**：留在后备存储中，不随批次物化；只有排序键或最终投影需要时，
  才通过 `BatchSource.loadColumn()` 按选中行号 gather 加载。
- **批次身份四元组**：`(slotId, batchId, dataVersion, generation)`。
  `generation` 在槽位每次复用时递增。

## 生命周期

```
acquire ──> open（可建视图/选择向量）──> release ──> 槽位归还池中，generation++
```

- 释放批次后，任何经旧 `ColumnView` 的读取抛 `ReleasedBatchError`；
  对已释放批次的列加载请求同样被拒绝。
- 选择向量（`SelectionVector`）绑定批次身份四元组；`resolve(batch)` 在校验
  失败时抛 `StaleSelectionError`，因此缓冲复用永远不会把上一批的行号映射到
  新数据上。
- 列加载是异步且可取消的：`AbortSignal` 触发 `QueryAbortedError`，加载在
  异步间隙后重新校验批次令牌（batchId/dataVersion/generation）。

## 执行管线（`src/executor.ts`）

1. **扫描 + 过滤**：窄列上逐行求值三值逻辑谓词（null 比较 = unknown，
   只保留 true），生成绑定批次的选择向量；只把 `(rowRef, sortKey, seq)`
   留在运行记录里，批次立即释放。
2. **稳定排序**：单键比较器 + 输入序号 `seq` 决胜，同键严格保持原输入
   次序，与键值何时物化无关；null 键恒排最后。
3. **延迟投影**：按批次分组最终行，重新 `fetch` 批次，只加载被投影的列、
   只 gather 存活行。未投影的宽列载入次数为 0（`LoadStats` 统计每列的
   调用次数与加载行数）。

## 参考实现与测试

`src/reference.ts` 是逐行急切求值的参考实现（读全部列 → 过滤 → 稳定排序
→ 投影）。`tests/pipeline.test.ts` 覆盖：空选择、全 null 过滤列、跨批
稳定排序、重复 rowID 不同版本、取消、缓冲复用（过期选择向量/已释放视图
报错）、未投影宽列零读取，以及随机数据集上与参考实现逐值一致。
