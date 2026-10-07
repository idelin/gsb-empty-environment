# batch-import

TypeScript 批量导入库：解析 → 校验 → 分批提交，支持协作式取消与幂等恢复。

## 语义

- **提交前取消，零可见写入**：取消令牌在任何一批 `commitBatch` 调用之前生效（包括 `hasBatch` 检查期间到达的取消），该批及后续批次不产生任何存储写入。
- **提交中取消，只停后续批次**：一旦某批进入 `commitBatch`（不可返回点），该批必定执行完成，报告中携带存储返回的真实结果（`result.inserted` 等）；后续批次标记为 `not-started` 并停止。
- **批次 id 稳定**：id 由批内容 SHA-256 派生（`batch-<行数>-<hash16>`），相同输入与批大小跨进程、跨重启恒定。
- **重启跳过已提交批**：每批提交前先查 `storage.hasBatch(id)`，已提交的批标记 `skipped`，不会重复写入。

## 结构

- `src/parser.ts` — 文本解析，畸形行隔离为错误
- `src/validator.ts` — 字段校验（空 id/name、非正整数数量、文件内重复 id），坏行隔离
- `src/batch.ts` — 分批与内容寻址的稳定批次 id
- `src/importer.ts` — 编排与取消/恢复契约
- `src/storage.ts` — 存储接口（`commitBatch` 需按 batchId 原子且幂等）
- `src/virtual-storage.ts` — 内存虚拟存储，带可注入异步钩子（`Gate`/`deferred`），用于在精确边界挂起导入器

## 测试

```sh
npm test   # tsc 编译后运行 test/run.ts
```

覆盖：提交前取消零写入、提交中取消当前批返回真实结果、批次 id 稳定性、
重启跳过已提交批且无重复行、完成后再运行为 no-op、部分坏行隔离。

## 环境说明

离线环境，TypeScript 3.9.10 与 @types/node 通过 `node_modules` 软链自
yarn 全局目录提供；如需正式环境请 `npm install -D typescript @types/node`
并移除软链。
