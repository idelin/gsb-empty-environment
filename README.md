# DiffServ 优先级映射器（DiffServ Priority Mapper）

基于 IP 包头 DSCP/TOS 字段的差分服务转发器，TypeScript 实现，零依赖，
使用 Node.js 内置测试运行器（`node:test`）验证。

## 功能

- **分类器（Classifier）**：按 RFC 2474/2597/3246 将 DSCP 映射到内部队列
  - `EF (46)` → 高优队列（Expedited Forwarding，语音等低时延业务）
  - `AFxy (10/12/14/18/20/22/26/28/30/34/36/38)` → 4 个保底队列 AF1–AF4
  - 其余（含 `CS0`）→ 尽力而为队列 BE
  - 支持直接解析 IPv4 TOS / IPv6 Traffic-Class 字节（高 6 位为 DSCP，低 2 位 ECN 忽略）
  - 映射表可按运营商策略覆盖
- **调度器（Scheduler）**：
  - EF 队列**严格优先**排空（包级非抢占）
  - AF1–AF4 间按**平滑加权轮询（smooth WRR）**调度，权重可配（默认 4:3:2:1）
  - BE 仅在 EF 与所有 AF 队列均空时获得服务
- **链路仿真器**：离散事件模拟带宽受限的出口链路，输出每包排队时延与分类吞吐统计

## 目录结构

```
src/packet.ts      DSCP 常量、队列与报文类型
src/classifier.ts  DSCP/TOS -> 队列映射器
src/scheduler.ts   EF 严格优先 + AF 平滑 WRR + BE 调度器
src/link.ts        带宽受限链路离散事件仿真与统计
src/traffic.ts     语音/视频/大流量下载等流量生成器
test/              node:test 测试（分类、调度、SLA 验收）
examples/demo.ts   运行样例：EF 语音 + AF 视频 + BE 下载拥塞混跑
```

## 运行

要求 Node.js >= 22.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，无需编译）。

```bash
npm test       # 运行全部 15 个测试
npm run demo   # 运行拥塞场景样例
```

## 样例输出

8 Mbps 链路注入 241% 超载流量（EF 语音 0.064 Mbps + AF4 视频 3.2 Mbps + BE 下载 16 Mbps）：

```
queue  packets    avg delay     max delay   delivered
-----  -------    ---------     ---------   ---------
EF     250           0.74 ms       1.50 ms     0.064 Mbps
AF4    1334          0.75 ms       1.61 ms     3.202 Mbps
BE     1973       1759.70 ms    3518.50 ms     4.735 Mbps

=== SLA verdict ===
EF voice max delay  : 1.50 ms   (SLA: < 5 ms)   -> PASS
EF voice goodput    : 0.064 Mbps (SLA: 0.064 Mbps) -> PASS
BE download goodput : 4.735 Mbps (offered 16 Mbps, suppressed to leftover) -> PASS
```

EF 语音最大时延被约束在一个 MTU 传输时间（1.5 ms）以内；BE 下载被压制到
剩余带宽，平均排队时延劣化到秒级，差异化 SLA 严格兑现。

## 测试结果

```
✔ EF (DSCP 46) maps to the EF queue
✔ all AFxy codepoints map to their AF class
✔ CS0 and unknown codepoints fall back to BE
✔ TOS byte: DSCP is the high 6 bits, ECN bits are ignored
✔ rejects out-of-range DSCP / TOS values
✔ operator overrides can re-map codepoints
✔ EF is served with strict priority over AF and BE
✔ BE is served only when EF and all AF queues are empty
✔ AF classes follow smooth WRR with exact long-term ratios
✔ empty AF queues are skipped and do not consume WRR turns
✔ dequeue on an empty scheduler returns undefined
✔ rejects non-positive AF weights
✔ SLA: EF voice keeps ultra-low latency while BE download is suppressed
✔ SLA: congested AF classes share spare bandwidth by WRR weights
✔ SLA: EF preempts congested AF+BE mix even under extreme overload
ℹ tests 15  pass 15  fail 0
```

## 使用示例

```ts
import { DiffServScheduler } from './src/scheduler.ts';
import { simulateLink, statsByQueue } from './src/link.ts';
import { voiceFlow, bulkFlow } from './src/traffic.ts';

const scheduler = new DiffServScheduler({ afWeights: { AF4: 4, AF3: 3, AF2: 2, AF1: 1 } });
const packets = [...voiceFlow('voip', 0, 5), ...bulkFlow('dl', 0, 5, 2_000_000)];
const result = simulateLink(packets, 1_000_000, scheduler); // 8 Mbps 链路
console.log(statsByQueue(result, 5));
```
