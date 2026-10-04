# versioned-vote-ledger

支持离线修改、按投票人版本合并的评审投票账本（TypeScript，零依赖，Node >= 23 直接运行）。

## 模型

- **票**：`{ issueId, voterId, version, replicaId, choice }`。
  `choice` 为 `{kind:'candidate', candidateId}`、`{kind:'abstain'}` 或 `{kind:'retract'}`。
- **合并**：按 `(issueId, voterId)` 归并，保留 `(version, replicaId)` 字典序最大的一票；
  撤回也是一个版本，旧票之后到达不会复活它。
- **幂等与冲突**：重复导入同样的票是空操作；同一 `(version, replicaId)` 出现不同内容时
  抛出 `VoteConflictError`，不任选一份。
- **资格表**：独立版本化（`EligibilityTable`），议题通过 `eligibilityVersion` 冻结快照，
  之后的权重调整不影响历史议题。同版本重复登记相同内容幂等，内容不同抛
  `EligibilityConflictError`。
- **计票**：弃权计入参与权重但不计候选；无资格票保留在 `rejected` 中；撤回的票不参与。
  权重一律用 `bigint` 精确求和。`participatedWeight >= participationThreshold` 才有决议，
  胜者取权重最大的候选，并列时取候选 ID 字典序最小者。
- **复算器**：`recount()` 从原始票集合独立地排序归并、计票，
  `tallyResultsEqual()` 用于与账本结果交叉校验。

## 运行

```sh
npm test            # node --test test/
npm run cli -- tally   --issue examples/issue.json --eligibility examples/eligibility.json --votes examples/votes.json
npm run cli -- recount --issue examples/issue.json --eligibility examples/eligibility.json --votes examples/votes.json
```

`recount` 同时输出账本与复算器两份结果及 `consistent` 标志，不一致时退出码为 1。

## 输入格式

- `issue.json`：`{ "issueId", "eligibilityVersion", "participationThreshold": "<bigint 字符串>" }`
- `eligibility.json`：`{ "versions": [{ "version", "weights": { "<voterId>": "<bigint 字符串>" } }] }`
- `votes.json`：票对象数组，`choice` 形如 `{ "kind": "candidate", "candidateId": "c1" }`、
  `{ "kind": "abstain" }` 或 `{ "kind": "retract" }`。

大整数权重（超过 2^53）以字符串形式在 JSON 中传递，内部用 `bigint` 处理。

## 目录

- `src/types.ts` — 共享类型
- `src/ledger.ts` — 账本：校验、按投票人版本合并、冲突检测
- `src/eligibility.ts` — 版本化资格表
- `src/tally.ts` — 计票与胜者判定
- `src/recount.ts` — 独立复算器与结果比对
- `src/cli.ts` — 命令行入口
- `test/` — Node 内置测试（撤回迟到、资格版本缺失、跨副本并列、全部弃权、大整数权重等）
