# recovery — 增量恢复规划器

任务依赖若干文件版本（内容摘要）。文件损坏/变更时，只重跑受影响任务及其后继；
已成功的独立分支直接复用；重跑产物内容相同（输出摘要不变）会阻断失效继续向下游传播。

## 模型

- `Task`：声明 `Files`（读哪些文件）、`Deps`（依赖哪些任务）和 `Run`（产出字节）。
- `Record`：每个任务的持久化记录 —— 输入摘要、输出内容、输出摘要、成功标志。
- `Store`：记录存储。`MemStore`（内存）与 `FileStore`（JSON 文件，可跨进程恢复）。

## 调度规则

1. 拓扑排序（确定性顺序，检测环/未知依赖/重名）。
2. 输入摘要 = 声明文件内容 + 各依赖**输出摘要** 的 SHA-256。
3. 记录存在且成功且输入摘要一致 → 复用，不重跑。
4. 否则重跑；产物内容相同 ⇒ 输出摘要不变 ⇒ 下游输入摘要不变 ⇒ 自动复用（提前截断）。
5. 失败任务写入 `OK=false` 记录，下次运行必然重试；其后继本轮被阻断（Blocked）。
6. `Planner.Retries` 控制单次运行内的额外重试次数。

## 验证（`recovery_test.go`）

- `TestFullRebuildThenReuse`：全量重建各任务执行 1 次；无变更再跑全部复用。
- `TestCorruptFileRerunsOnlyAffectedBranch`：损坏 `a.txt`，仅 A/C/D 重跑，B/E 复用，执行次数核对。
- `TestIdenticalRerunOutputStopsInvalidation`：文件内容变但归一化产物相同，下游 B 不重跑。
- `TestFailureBlocksDependentsAndIsRetried`：失败阻断后继；输入不变也重试；修复后后继补跑。
- `TestWithinRunRetries`：单次运行内瞬时失败的重试（`Retries`）。
- `TestIncrementalDigestsMatchFullRebuild`：增量结果与清空存储后的全量重建摘要逐项一致。
- `TestFileStoreRecoveryAcrossRestarts`：`FileStore` 跨“重启”全部复用。
- `TestGraphValidation`：环、未知依赖、重名的报错。

## 运行

```sh
go test ./...
```
