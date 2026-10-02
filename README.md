# 跨时区事件关联与因果图重构器

离散分布式追踪日志的时间因果拓扑重构器。各服务器物理时钟存在未知漂移时，
利用 RPC 请求/响应的发送/接收四元组建立时间因果偏序图，强制校正时钟基线，
消除“负延迟”等物理荒谬，输出物理自洽的调用拓扑图。

纯 TypeScript 实现，零依赖（Node.js ≥ 23.6 原生类型擦除运行），
测试基于 Node.js 内置 `node:test`。

## 算法

1. **反例检测**：用原始物理时间戳计算每条消息的表观延迟，负值即为时钟倒流反例。
2. **约束提取**：每条消息导出因果约束“发送必先于接收”：
   `offset[recv] >= offset[send] + sendLocal + minLatency - recvLocal`。
3. **基线求解**：约束构成差分约束系统，用 Bellman-Ford 风格最长路松弛求
   满足全部约束的最小平移；若存在正环则日志自相矛盾，抛 `InconsistentTraceError`。
4. **拓扑重构**：以校正后时间建因果图（message 边 + 同机 local-order 边），
   Kahn 算法输出拓扑序。

## 运行

```bash
npm run demo   # 运行验收场景样例
npm test       # 运行 node:test 测试（9 个用例）
```

## 验收场景样例输出（节选）

A 日志 12:00:00.000 发请求，B 日志 11:59:59.000 收请求（时钟倒流）：

```
=== 检测到的物理荒谬（负延迟反例） ===
  消息 rpc-1:req (A -> B): 表观延迟 -1000ms < 0

=== 因果图强制校正后的时钟基线偏移 ===
  offset[A] = +0ms
  offset[B] = +1000ms
  offset[C] = +2000ms

=== 校正后的消息延迟（物理自洽） ===
  rpc-1:req (A -> B): 原始 -1000ms  ->  校正 0ms
  rpc-1:res (B -> A): 原始 1015ms   ->  校正 15ms

=== 物理自洽的调用拓扑图（因果拓扑序） ===
  1. [A] 12:00:00.000  A->B GetUser 请求发送
  2. [B] 12:00:00.000  A->B GetUser 请求接收
  3. [B] 12:00:00.030  A->B GetUser 响应发送
  4. [A] 12:00:00.045  A->B GetUser 响应接收
  ...
```

## 测试结果

```
✔ 跨时区事件关联与因果图重构器 (9 个用例全部通过)
ℹ tests 9 / pass 9 / fail 0
```

覆盖：负延迟反例检测、时钟基线强制校正、同机处理时长保持、
跨服务器链式传递校正（A→B→C）、最小网络延迟约束、拓扑序因果一致性、
无漂移零偏移、矛盾日志正环报错、非法消息引用报错。

## API

```ts
import { reconstruct, traceFromRpcQuadruples } from './src/index.ts';

const trace = traceFromRpcQuadruples([
  { rpcId: 'rpc-1', client: 'A', server: 'B',
    clientSendMs: 0, serverRecvMs: -1000, serverSendMs: -970, clientRecvMs: 45 },
]);
const result = reconstruct(trace, { minNetworkLatencyMs: 0 });
// result.offsets / violations / events / edges / topologicalOrder / messageLatencies
```

## 目录结构

- `src/types.ts` — 事件、消息、RPC 四元组、重构结果等类型
- `src/clockCorrector.ts` — 差分约束提取与 Bellman-Ford 时钟偏移求解
- `src/causalGraph.ts` — 因果边构建与 Kahn 拓扑排序
- `src/reconstructor.ts` — 重构主流程与 RPC 四元组展开
- `examples/demo.ts` — 验收场景运行样例
- `test/reconstructor.test.ts` — `node:test` 测试
