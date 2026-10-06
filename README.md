# batch-importer

TypeScript 导入库：CSV 解析 → 行级校验 → 分批提交，支持协作式取消、稳定批 id 与断点续传。

## 流水线

1. `parseCsv(text)` — 解析 CSV（引号、转义、CRLF、缺失单元格），返回带行号的记录。
2. `validateRows(rows, schema)` — 逐行独立校验；坏行隔离进 `rejected`（含每个字段的违规信息），好行进入 `accepted`，互不影响。
3. `commitBatches(accepted, opts, storage, checkpoints)` — 分批写入。

## 提交语义

- **提交前取消**：token 在首个批次启动前已取消 → 零可见写入（无 writeBatch 调用、无检查点），返回 `status: "cancelled"`。
- **提交中取消**：取消只在批次之间被观察。当前在途批次必定完成、写入检查点，并在结果中返回真实结果（`batches[]` 含真实 `rowCount`）；后续批次不再启动，`remainingBatches` 给出未启动批次数。
- **稳定批 id**：`batchIdFor(importId, index)` = `` `${importId}/batch/NNNNNN` ``，跨进程、跨实例一致。
- **重启恢复**：提交循环先加载 `CheckpointStore` 中已提交的批 id 并跳过；`Storage.writeBatch` 以 batchId 为幂等键，重复恢复不会产生重复行。

## 测试

测试全部基于虚拟存储（`MemoryStorage` / `MemoryCheckpointStore`）与可控 promise（`deferred` / `Gate`），覆盖：取消边界（提交前/批次中）、批 id 稳定性、全量与部分重启恢复、坏行隔离、端到端流程。

```sh
npm test   # 构建并运行 dist/test/run.js
```

注意：环境无网络，`node_modules/typescript` 是指向本机 yarn 全局 TypeScript 3.9.10 的符号链接。
