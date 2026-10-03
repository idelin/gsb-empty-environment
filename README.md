# DiffServ Priority Mapper

基于 IP 包头 DSCP/TOS 字段的差分服务（DiffServ）转发器，TypeScript 实现，
使用 Node.js 内置测试运行器（`node:test`）验证，零第三方依赖。

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/packet.ts` | 报文模型；TOS 字节与 6-bit DSCP 的相互转换 |
| `src/classifier.ts` | DSCP → 队列映射：EF(46) → `EF`，AFxy → `AF1..AF4`，其余 → `BE` |
| `src/scheduler.ts` | 调度器：EF 严格优先，AF 间平滑加权轮询（smooth WRR），BE 兜底 |
| `src/link.ts` | 瓶颈链路仿真：固定容量、1ms 时钟节拍、字节级 pacing（非抢占式） |
| `src/simulation.ts` | 混合流量场景构造与运行 |

调度优先级：`EF` 严格优先排空 → `AF4..AF1` 按权重 WRR（默认 4:3:2:1）→ `BE`
仅在 EF/AF 全空时服务。

## 运行

要求 Node.js ≥ 22.6（利用原生 TypeScript type stripping，无需编译）。

```sh
npm test    # node --test，运行 test/ 下全部用例
npm run demo
```

## 运行样例

场景：1.5 Mbit/s 瓶颈链路，注入 EF 语音（64 kbit/s）、AF41 视频
（480 kbit/s）与 BE 大吞吐下载流（6 Mbit/s 供给），总需求远超链路容量：

```
flow        packets      bytes   avg-lat(ms)   max-lat(ms)   throughput(kbit/s)
voice-ef         500      80000        4.816         8.800               64.0
video-af4       1000     600000        7.608        12.000              480.0
bulk-be          796    1194000     4204.684      8397.413              955.2
```

EF 语音以个位数毫秒延迟全额通过（上限即一个 1500B BE 报文的串行化时间），
BE 吞吐被压制到剩余容量（约 955 kbit/s）并承受秒级排队延迟。

## 测试结果

16 个用例全部通过，覆盖三个层级：

- `test/classifier.test.ts` — TOS/DSCP 解析、EF/AFxy/BE 映射、非法值拒绝
- `test/scheduler.test.ts` — EF 严格优先、BE 兜底、WRR 权重比例（1:2:4）、
  平滑交织、空队列跳过
- `test/sla.test.ts` — 验收场景：拥塞下 EF 语音超低延迟（avg < 6ms，
  max < 10ms）且 64 kbit/s 全额兑现；BE 吞吐被压制到剩余容量、排队延迟
  超 EF 两个数量级以上；AF 类间吞吐比符合 WRR 权重（1:3）

```
ℹ tests 16
ℹ pass 16
ℹ fail 0
```
