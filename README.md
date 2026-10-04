# 分阶段作业执行器

依次执行 **准备(prepare) → 提交(submit) → 清理(cleanup)** 三个阶段，
并提供可 JSON 序列化的恢复记录（`RecoveryRecord`）用于崩溃恢复。

## 核心不变量

1. **准备期可取消**：提交开始前的每个异步边界都有取消检查点，
   取消后状态迁移到 `cancelled`，提交驱动不会被调用。
2. **提交一旦开始必须等待明确结果**：调用 `submit` 前先持久化
   `submission.started = true` 与查询令牌（写前日志）；此后取消仅被记录
   （`cancel()` 返回 `deferred`），执行器继续等待明确的
   `accepted` / `rejected` 结果。
3. **清理失败不丢提交状态**：清理异常只记入 `cleanup.lastError`，
   提交结果与对外状态保持不变。
4. **对外状态永不误报"未执行"**：`PublicStatus` 枚举中不存在
   `not-executed`；`submission.started` 之后只能报告
   `submitted` / `submit-rejected` / `submit-outcome-unknown`。

## 恢复

`StagedJobExecutor.resume(snapshot, driver)` 只依赖可序列化快照
（内部做 JSON 往返验证）：

- 快照显示提交未开始 → 整体安全重跑（取消意图也会被重新检查）；
- 提交已开始但结果未知 → 通过 `driver.resolveSubmission(ctx, token)`
  查询明确结果，绝不假设未执行，随后继续清理；
- 已有明确结果 → 只补跑清理。

## 运行测试

```sh
bun test
```

测试覆盖每个异步边界的取消：prepare 前、prepare 中、submit 边界、
submit 在途、cleanup 边界、cleanup 在途，以及崩溃恢复与不变量扫描。
