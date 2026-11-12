# 浏览器历史恢复权限

页面从往返缓存恢复时，冻结前的临时权限可能已被撤销。实现供TypeScript宿主调用的文档恢复组件，冻结记录权限代次和待处理操作，恢复先复核当前许可再解冻受保护任务；普通显示状态可恢复，但不可重发已消费操作。用取消、后台撤销和重复恢复事件测试不能凭旧快照重新取得能力。

## 组件结构

- `src/document-restore-controller.ts`：`DocumentRestoreController`，供 TypeScript 宿主调用的核心组件。
- `src/errors.ts`：`DocumentRestoreError` 与错误码。
- `index.ts`：对外导出。
- `test/document-restore-controller.test.ts`：基于 `node:test` 的安全不变量测试。

## 宿主接口

宿主实现 `RestoreHost`：

- `queryPermission(capability)`：恢复时重新查询当前许可，只读取当前状态，不触发新的授权提示。
- `runProtectedTask(operationId)`：仅在复核通过后由组件调用，执行受保护任务。

组件自身从不授予任何能力；能力只存在于宿主侧，快照只是不可变的纯数据记录。

## 生命周期

1. `registerPending(id, capability, generation)` 登记待处理的受保护操作及其权限代次。
2. `freeze(displayState)` 在 `pagefreeze` 时生成 `FrozenSnapshot`：记录每个能力的权限代次、全部待处理操作（含已取消/已消费状态）与普通显示状态。冻结后 `cancel`、`markConsumed`、`registerPending`、再次 `freeze` 均抛出 `CONTROLLER_FROZEN`。
3. `revokeCapability(capability)` 模拟后台撤销：推进代次并把相关待处理操作标记为 `blocked`，冻结期也可调用。
4. `restore(snapshot)` 在 `pageshow`（persisted）时执行：
   - 先按能力去重并向宿主重新查询当前许可，再比对当前代次与快照代次；
   - 代次已变 → `generation-changed`；许可非 `granted` → `permission-revoked`；
   - 已消费 → 计入 `skippedConsumed` 且绝不重发；已取消 → 计入 `skippedCancelled`；
   - 仅当代次一致且许可仍为 `granted` 时才调用 `runProtectedTask` 解冻；
   - 普通显示状态始终随报告返回，可安全恢复。

## 安全不变量

- 旧快照不携带能力，无法凭快照重新取得授权，恢复路径必须重新复核宿主当前许可。
- 同一 `snapshotId` 的重复恢复事件返回 `duplicate: true`，不会二次解冻或重发任务。
- 已消费操作的消费记录在恢复后保留，任何恢复事件都不会重发。
- 撤销后即使状态又回到 `granted`，代次不一致仍阻止解冻。
- 快照经深度 `Object.freeze`，运行时篡改快照字段会抛 `TypeError`，且不影响恢复判定。

## 运行测试

```sh
npm test
```

需要 Node 22.6+（依赖 `--experimental-strip-types`，Node 23 起默认可直接运行 TypeScript）。
