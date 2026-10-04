# consensus-trace-checker

固定 5 成员（`n1`–`n5`，quorum = 3）共识实现的事件轨迹安全性检查器。
审查目标是区分**确实违反安全性**（输出最小因果闭包证据）与**证据不足**
（标记 incomplete，不据缺失日志推断实现有错）。

## 事件模型

每个事件（`src/types.ts` 的 `TraceEvent`）：

| 字段 | 含义 |
| --- | --- |
| `id` | 全局唯一事件 ID |
| `kind` | `promise` / `accept` / `persist` / `restart` / `learn` / `apply` |
| `node` | 成员 `n1`–`n5`（固定成员，之外即拒绝） |
| `epoch` | 非负整数 |
| `ballot` | 全序 ballot（promise/accept/persist/learn 有效） |
| `slot` | 槽号（accept/learn/apply ≥ 1） |
| `value` | 值摘要（accept/learn/apply 必填；persist 可携带已接受值） |
| `causes` | 显式因果引用的事件 ID 列表 |

`persist` 记录已确认持久化状态：`ballot` 为持久化的 promise ballot，
`slot`+`value` 可选地表示持久化的 accepted 记录。

## 安全规则

- `invalid_ballot`：ballot 必须落在全序内（非负整数）。
- `accept_below_promise`：accept 的 ballot 不得低于因果上先于它的已持久 promise。
- `restart_state_loss`：重启后不得丢失已确认持久状态（更低的 promise/accept
  ballot，或覆盖已持久槽位的值）。
- `learn_without_quorum`：一个值必须有 3 个不同成员的**有效** accept
  （ballot 合法且自身未违反持久化规则）才能 learn 为 chosen。
- `conflicting_chosen`：同一槽位不得有两个 quorum 支撑的不同 chosen 值。
- `apply_out_of_order`：apply 必须按从 1 开始的连续 chosen 槽推进。
- `apply_without_chosen`：apply 的槽位/值必须已有因果上先于它的 learn。

存在性违规（witness 事件齐备即成立）不受日志缺失影响；缺失性判定
（quorum 不足、apply 前驱缺失、无 chosen）只有在事件因果闭包完全解析时
才下结论，否则记入 `incomplete`，**不**视为实现有错。

重复事件 ID：内容相同幂等去重；内容冲突则整条轨迹 `rejected`。

## 证据最小化与独立验证

每个违规的证据从其 focus 事件的因果闭包中搜索：按**事件数升序、再按
排序后 ID 列表字典序**枚举子集，第一个被独立验证器（`src/verifier.ts`，
不复用检查器任何分析代码）确认的子集即为最小证据。验证器调用预算
（`--budget`，默认 50000）耗尽时，返回完整因果闭包作为当前有效反例，
并置 `minimal: false`、`budgetExhausted: true`（未证最小）。

## 使用

```sh
npm test                              # 构建（src/*.ts -> dist/）并运行全部测试
node bin/tracecheck.mjs check samples/doubleChosen.json
node bin/tracecheck.mjs check trace.json --budget 1000 --emit-evidence out/
node bin/tracecheck.mjs verify out/evidence-0-conflicting_chosen.json
node bin/tracecheck.mjs samples samples   # 重新生成样本轨迹
```

`check` 退出码：0 无违规，1 发现安全违规，2 轨迹被拒绝/用法错误，4 证据不足。
`verify` 退出码：0 证据独立成立，1 不成立。

## 样本轨迹（samples/）

- `doubleChosen`：同一槽位两个值各获 quorum 被 learn（双 chosen）。
- `lowBallotAccept`：accept 低于已持久 promise。
- `restartStateLoss`：重启后丢失持久化 promise ballot。
- `outOfOrderApply`：先 apply 槽 2 再 apply 槽 1。
- `legalNotificationDelay`：迟到 accept / 延迟 learn / 延迟 apply，完全合法。
- `missingLogs`：learn 引用的 accept 缺失 → incomplete，不报违规。

## 测试

- `test/checker.test.mjs`：各规则正/反例、幂等去重、冲突拒绝、成员校验。
- `test/verifier.test.mjs`：独立验证器接受检查器证据、拒绝篡改/错类/空证据。
- `test/minimality.test.mjs`：对短轨迹的因果闭包做**全子集枚举**，逐一核对
  检查器输出的最小证据与暴力枚举结果一致；并验证预算耗尽路径仍给出有效反例。
- `test/cli.test.mjs`：CLI 退出码、证据导出与独立 verify、样本生成。

## 构建说明

环境无 tsc/网络，`build.mjs` 是零依赖类型擦除器，将 `src/*.ts`（受控的
可擦除 TS 子集：无 enum/class/`as`/显式调用泛型）编译到 `dist/`。有
TypeScript 工具链时可直接 `npx tsc --noEmit` 做类型检查（见 `tsconfig.json`）。
