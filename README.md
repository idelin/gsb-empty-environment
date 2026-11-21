# 留存治理组件

按数据类别（category）执行日志平台留存治理：

- **到期计算**：`RetentionPolicyTable` 为每个数据类别配置留存时长，到期时刻 =
  `createdAtMs + retentionMs`，未配置策略的类别直接拒绝写入。
- **到期删除**：`RetentionGovernanceService.runRetentionSweep()` 删除所有到期数据，
  并同步清理其派生副本（replica）与索引（index）；未到期数据在同一轮扫描中保持不动，
  到期时刻与扫描时刻相等时按到期处理。
- **合并归档不延长留存**：`mergeArchive()` 将归档的有效到期时刻锁定为来源记录中
  最早的到期时刻；任何会使归档存活超过该时刻的合并都会抛出
  `RetentionExtensionError` 并整体回滚（不写归档、不改来源记录）。
- **删除审计**：每次删除（记录或归档）都生成一条仅追加的 `DeletionAuditEntry`，
  记录目标、类别、到期时刻、被清理的副本/索引 ID 与来源记录。

## 模块

- `src/policy.ts`：类别留存策略与到期时刻计算
- `src/store.ts`：记录、归档、副本、索引与审计日志的内存存储
- `src/retention.ts`：留存治理服务（合并归档约束 + 到期扫描）
- `src/errors.ts`：治理相关错误类型
- `test/retention.test.ts`：基于 `node:test` 的验证，覆盖到期 / 未到期两类数据的删除范围

## 运行

```sh
npm test
```
