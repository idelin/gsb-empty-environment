# staged-job-executor

分阶段作业执行器：`prepare → submit → cleanup`，带阶段状态机、可序列化恢复记录，
并保证取消安全：**已提交的作业对外永远不会被报告为"未执行"**。

## 语义保证

- **准备期可取消**：取消检查点只存在于 prepare 阶段；在此之前取消 → `cancelled`，
  `submit` 绝不会被调用，对外 `executed=false`（此时这是真话）。
- **提交是提交点（commit point）**：进入 `submitting` 前先把恢复记录持久化为
  `in-doubt`，然后无论是否收到取消请求，都等待明确结果——
  后端返回回执（`accepted`）或抛出 `SubmissionRejectedError`（`rejected`）；
  其他异常视为结果未知，记录停在 `in-doubt`，对外报告 `executed='unknown'`
  （绝不是 `false`）。
- **清理失败不丢提交状态**：cleanup 异常只记入 `cleanup.error`，
  `submission` 原样保留，最终阶段仍为 `completed`。
- **可恢复**：`serialize()` 产出 JSON 恢复记录；`StagedJobExecutor.restore()` +
  `resume()` 在崩溃后通过 `backend.reconcile()` 对账 in-doubt 提交，继续清理直至完成。

## 状态机

```
idle → preparing → submitting → cleaning → completed
          │  │          │
          │  │          └→ in-doubt → cleaning
          │  └→ failed
          └→ cancelled        （仅在提交点之前可达）
```

非法转移（如 `submitting → cancelled`）抛出 `InvalidTransitionError`，
见 `src/stateMachine.ts`。

## 对外状态

`status()` 返回 `PublicStatus`，其中 `executed` 为三态：

| submission     | executed    |
|----------------|-------------|
| `accepted`     | `true`      |
| `in-doubt`     | `'unknown'` |
| `rejected`     | `false`     |
| `not-started`  | `false`     |

推导只发生在 `src/record.ts` 的 `executedOf()`，是不变量的唯一落点。

## 验证

执行器的每个 `await` 都挂在命名边界（`prepare` / `after-prepare` / `submit` /
`after-submit` / `cleanup` / `after-cleanup` / `reconcile`）上。测试逐一在每个边界
注入取消，并断言不变量：`submission.kind === 'accepted'` 时 `executed === true`；
`executed === false` 仅在 `not-started` 或 `rejected` 时合法。

```sh
npm test        # tsc 编译 + 运行全部测试
```

## 文件

- `src/executor.ts` — 执行器与命名异步边界
- `src/phases.ts` — 阶段、提交状态、恢复记录、对外状态类型
- `src/stateMachine.ts` — 合法转移表与校验
- `src/record.ts` — 记录序列化/反序列化、`executed` 推导、持久化存储接口
- `src/backend.ts` — 后端钩子契约（prepare/submit/cleanup/reconcile）
- `src/cancellation.ts` — 协作式取消令牌（只在显式检查点生效）
- `test/executor.test.ts` — 边界取消扫描、崩溃恢复、清理失败等 18 个用例
