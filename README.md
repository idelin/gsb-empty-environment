# consensus-trace-checker

固定 5 成员共识实现的事件轨迹安全性检查器。区分「确实违反安全性」
（violation，附最小因果证据）与「证据不足」（incomplete，缺日志不定罪）。

## 运行要求

- Node.js >= 23.6（直接使用原生 TypeScript 类型擦除运行 `.ts`，无需构建）。
- 仅用可擦除 TS 语法；`tsconfig.json` 仅供编辑器/类型检查使用。

```sh
npm test                                   # node --test test/*.test.ts
node src/cli.ts check  examples/double-chosen.json
node src/cli.ts check  trace.json --budget 1000
node src/cli.ts verify evidence.json
```

退出码：`check` — 0 无违规 / 1 有违规 / 2 输入非法（含冲突事件 ID）；
`verify` — 0 证据有效 / 1 无效 / 2 输入非法。

## 事件模型

每条事件：`id`、`kind`、`node`、`epoch`、`causes`（显式因果引用），以及
按 kind 而定的字段：

| kind      | 额外字段                                   | 含义                     |
|-----------|--------------------------------------------|--------------------------|
| `promise` | `ballot`                                   | 承诺 ballot              |
| `accept`  | `ballot`, `slot`, `value`                  | 接受值摘要               |
| `persist` | `persisted: {promised?, accepted?[]}`      | 已确认持久化状态         |
| `restart` | `recovered: {promised?, accepted?[]}`      | 重启后恢复的状态         |
| `learn`   | `ballot`, `slot`, `value`                  | 学会某槽被选定           |
| `apply`   | `slot`, `value`                            | 应用某槽的值             |

ballot 全序：先比较 `round`，再比较 `node`。quorum 固定为 3（5 成员）。

## 检查规则

- `AcceptBelowPersistedPromise`：accept 的 ballot 低于本节点已持久 promise。
- `RestartLostPersistedState`：重启恢复状态未覆盖已确认持久的 promise/accept。
- `UnquorumedLearn`：learn 缺少 3 个不同成员的有效 accept（同 slot/value/ballot，
  且 accept 本身非低 ballot）。
- `ConflictingChosen`：同一槽两个不同值各自获得有效 quorum。
- `ApplyGap`：apply 未按从 1 开始的连续槽推进（同值重放幂等，不算违规）。
- `ApplyValueMismatch` / `ApplyNotChosen`：apply 引用的 learn 值不符或本身无 quorum。

幂等与拒绝：相同事件 ID 内容一致则幂等去重；内容冲突则整条轨迹拒绝
（`TraceConflictError`）。缺失因果事件只产生 `incomplete` 结论，
绝不据此推断实现有错。

## 证据与最小化

每个 violation 附带因果闭包证据，并在「事件数最少、其次事件 ID 列表
字典序最小」的顺序下枚举子集，用**独立验证器**（`src/verifier.ts`，
与检查器不共享逻辑）逐一复核，第一个通过者即最小证据
（`minimalProven: true`）。验证器调用预算（`--budget`，默认 200000）
耗尽时返回当前有效反例并标记 `minimalProven: false`、`budgetExhausted: true`。

`verify` 子命令可对 `{claim, events}` 形式的证据独立复核。

## 目录

- `src/types.ts` — 事件/结论/证据类型
- `src/trace.ts` — 摄入：校验、幂等去重、冲突拒绝
- `src/checker.ts` — 两遍扫描的规则检查器
- `src/verifier.ts` — 独立证据验证器
- `src/evidence.ts` — 因果闭包 + 预算受限最小化
- `src/cli.ts` — 命令行
- `test/samples.ts` — 双 chosen、低 ballot、乱序 apply、合法通知延迟、缺失日志等样本
- `test/` — node:test 测试（含短轨迹全子集枚举核对最小证据）
