# capability-negotiation

能力协商库：接收双方的功能声明（能力集合 + 依赖 + 互斥），求解优先级最高的共同配置；
协商结果密码学绑定双方原始声明，重放旧声明会被识别为另一轮；无解时返回最小冲突集合。

纯 TypeScript、零依赖（仅使用 `node:crypto`）。要求 Node.js >= 22.6（直接运行 `.ts`），
或用 `tsc` 编译后在任意现代运行时使用。

## 快速开始

```ts
import { createDeclaration, negotiate, TranscriptValidator } from "capability-negotiation";

const declA = createDeclaration("peer-a", "round-1", [
  { name: "h265", priority: 4, requires: ["hwdec"], conflictsWith: ["h264"] },
  { name: "hwdec", priority: 1 },
  { name: "h264", priority: 3 },
  { name: "video", priority: 5, mandatory: true },
]);
const declB = createDeclaration("peer-b", "round-1", [/* ... */]);

const result = negotiate(declA, declB);
// result.status === "AGREED" -> result.config / result.score
// result.status === "CONFLICT" -> result.conflict (最小冲突集合)

const validator = new TranscriptValidator();
const report = validator.validate(result, declA, declB);
// report.ok === true；重放/篡改/跨轮混用会在 report.issues 中给出对应错误码
```

## 模型

- `CapabilityOffer`：能力条目。`priority` 越高越优先；`requires` 为合取依赖；
  `conflictsWith` 为互斥；`mandatory` 表示必须出现在最终配置中。
- `Declaration`：一方声明，携带 `party` / `round` / `nonce` / `issuedAt` /
  `capabilities` / `digest`。`digest` 是对声明正文的规范化 SHA-256，任何篡改都会被识别。
- 共同配置只能选自双方报价的交集；依赖与互斥关系取双方声明的并集；
  目标为最大化双方优先级之和（同分时按字典序取确定性结果）。

## 求解

`solveConfiguration(declA, declB)`：

- 可行性 = 强制能力的依赖闭包落在报价交集内且不违反互斥（依赖为合取，闭包判定是精确的）。
- 最优配置通过分支限界搜索求得，上界为未选候选的正优先级总和。
- 无解时以删除式极小化提取最小冲突核 `MinimalConflict.requirements`
  （再删任意一条即可恢复可满足），并附 `reasons`：
  `missing-offer`（含依赖路径 `path` 与缺失方 `offeredBy`）与 `mutual-exclusion`。

## 转录与防重放

- `negotiate` 拒绝混合不同 `round` 的声明（`ROUND_MISMATCH`），并校验两份声明的
  `digest` 完整性（`DECLARATION_TAMPERED`）。
- `NegotiationResult.transcriptHash` 绑定轮次、双方声明摘要与求解结果，
  任何对结果或声明的事后修改都会导致 `TRANSCRIPT_MISMATCH` / `DECLARATION_TAMPERED`。
- `TranscriptValidator` 维护 `(party, nonce) -> round` 注册表：同一 nonce 出现在不同轮次
  即判定 `REPLAY_DETECTED`；同一轮内重复校验同一转录不会误报。
  可选 `maxDeclarationAgeMs` 启用声明时效检查（`STALE_DECLARATION`）。
- 校验时会用双方声明重新执行一次协商，逐字段比对记录的结果，防止伪造协商产出。

## 测试

```sh
npm test          # node --test "test/*.test.ts"
```

求解器另经 3000 个随机实例与暴力枚举对拍验证。
