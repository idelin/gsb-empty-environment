# causal-gap-handler

因果缺口处理库：前驱已过期删除时，后继消息不会永久挂起；只有安装带缺口证明的重置边界快照后，才允许跳过旧前驱。

## 核心规则

- 后继消息缺失前驱时缓冲等待；前驱过期（`expire`）后仍**不得**直接跳过。
- `createResetBoundary()` 生成边界快照，为当前所有已过期前驱出具缺口证明（gap proof），并记录逻辑视界 `horizon`。
- 只有 `installBoundary(boundary)` 之后，且消息 `seq <= horizon`（旧事件），其缺失前驱全部被缺口证明覆盖时，才允许跳过并释放。
- `seq > horizon` 的新事件永远不被该边界跳过——重置与新事件交错时不会错误跳过新依赖。
- 边界创建之后才过期的前驱不在旧边界证明范围内，需要更新的边界。

## API

- `receive(msg)` — 接收消息；返回 `delivered` / `buffered` / `delivered-with-skipped-predecessors`。
- `expire(predecessorId, reason?)` — 标记前驱已过期删除。
- `createResetBoundary()` — 创建带缺口证明的边界快照（未安装前不生效）。
- `installBoundary(boundary)` — 安装边界，释放被证明覆盖的旧挂起消息。
- `getAffectedMessages()` — 输出受影响消息及每条消息的恢复步骤。
- `getRecoveryPlan()` — 输出全局有序恢复步骤。

## 测试

```sh
npm test
```

零依赖：Node >= 23 原生运行 TypeScript（类型擦除），测试使用 `node:test`。
