# cep-sequence-matcher

多模式复合事件序列匹配器（CEP 引擎），TypeScript 实现，零第三方运行时依赖。

## 架构

每个注册的模式被编译为一个非确定性有向无环状态图（DAG）：
`S0 -> S1 -> ... -> Sn(accept)`。每个事件都可能在 `S0` 派生新的运行实例
（nondeterministic skip/take 语义），已有运行实例独立推进，因此重叠的
匹配都能被发现。核心实现见 `src/engine.ts`。

能力：

- 时序模式 + 否定：识别 `A -> B` 且中间不能出现 `C`；
- 跨事件时间滑窗：首末匹配事件跨度不得超过 `windowMs`；
- 乱序事件补偿：迟到的禁止事件落在已发出命中的区间内时，精准撤销
  （retract）该命中并通知订阅者；
- 模式超时丢弃：水位线越过 `起点 + windowMs` 的运行实例被丢弃；
- 日志：所有状态迁移（run-started / run-advanced / match / retract /
  invalidated / timeout）写入 `engine.journal`。

## 环境要求

Node.js >= 22.6（使用内置类型擦除运行 `.ts`，无需编译步骤、无需安装依赖）。

```bash
npm test        # Node.js 内置测试运行器 (node:test)
npm run demo    # 运行验收场景样例
npm run typecheck   # 可选：需自行安装 typescript（tsc --noEmit）
```

## 运行样例（`npm run demo`）

场景：模式要求 A 后 10 秒内发生 B 且无 C；t=0 发生 A，t=5 发生 B 产生命中；
随后迟到事件 t=2 的 C 到达，系统精准撤销先前的命中并记入日志。

```
--- ingest A@t=0s ---
--- ingest B@t=5s ---
MATCH   #1 [A-then-B-no-C] A@t=0s -> B@t=5s
--- late arrival: C@t=2s (out-of-order compensation) ---
RETRACT #1 [A-then-B-no-C] reason: late forbidden C@2000 inside matched span (0, 5000)
--- advance watermark to t=20s (timeout sweep) ---

=== journal ===
[at=0ms] run-started  A-then-B-no-C: run#1 started by A@0
[at=5000ms] match        A-then-B-no-C: match#1 emitted: A@0 -> B@5000
[at=5000ms] retract      A-then-B-no-C: match#1 retracted: late forbidden C@2000 inside matched span (0, 5000)
```

## 测试结果（`npm test`）

10 个测试全部通过（Node.js 内置 `node:test` 运行器）：

```
▶ acceptance boundary
  ✔ A@0 then B@5 hits; late C@2 precisely retracts the hit and logs it
▶ sliding time window
  ✔ B exactly at the 10s window edge still matches
  ✔ B beyond the 10s window does not match
  ✔ expired run is discarded as timeout and logged
▶ negation (no C between A and B)
  ✔ in-order C between A and B prevents the match
  ✔ late B after an already-known C does not match (buffer scan)
  ✔ C outside the (A, B) span does not affect the match
▶ out-of-order ingestion
  ✔ late A after already-seen B still produces the match
▶ multi-pattern support
  ✔ independent patterns match concurrently on one stream
  ✔ nondeterminism: overlapping runs of one pattern both match
ℹ tests 10  ℹ pass 10  ℹ fail 0
```

## API 速览

```ts
import { CepEngine } from "./src/index.ts";

const engine = new CepEngine();
engine.registerPattern({
  name: "A-then-B-no-C",
  sequence: ["A", "B"],
  forbid: ["C"],
  windowMs: 10_000,
});
engine.subscribe((n) => {
  if (n.kind === "match") { /* 命中 */ }
  else { /* n.kind === "retract"，补偿撤销 */ }
});
engine.ingest({ id: "a1", type: "A", timestamp: 0 });
engine.ingest({ id: "b1", type: "B", timestamp: 5_000 });
engine.advanceTo(20_000); // 推进水位线，触发超时丢弃
```
