import { reconstruct, traceFromRpcQuadruples } from '../src/index.ts';
import type { RpcQuadruple } from '../src/index.ts';

/** 以 12:00:00.000 为 t=0 的时钟格式化工具。 */
const BASE_MS = 12 * 3600 * 1000;
function fmt(ms: number): string {
  const total = ((BASE_MS + ms) % 86400000 + 86400000) % 86400000;
  const h = Math.floor(total / 3600000);
  const m = Math.floor((total % 3600000) / 60000);
  const s = Math.floor((total % 60000) / 1000);
  const mmm = total % 1000;
  const pad = (n: number, w: number) => String(n).padStart(w, '0');
  return `${pad(h, 2)}:${pad(m, 2)}:${pad(s, 2)}.${pad(mmm, 3)}`;
}

/*
 * 验收场景：三台服务器物理时钟存在未知漂移。
 *  - A 的时钟视为基准；
 *  - B 的时钟落后约 1.2s：A 12:00:00.000 发出的请求，B 的日志竟记录为 11:59:59.000 收到；
 *  - C 的时钟落后约 2.1s，且只与 B 通信（传递校正）。
 */
const rpcs: RpcQuadruple[] = [
  {
    rpcId: 'rpc-1', client: 'A', server: 'B', label: 'A->B GetUser',
    clientSendMs: 0,      // A: 12:00:00.000 发请求
    serverRecvMs: -1000,  // B: 11:59:59.000 收请求（物理时钟倒流！）
    serverSendMs: -970,   // B: 11:59:59.030 发响应（本地处理 30ms）
    clientRecvMs: 45,     // A: 12:00:00.045 收响应
  },
  {
    rpcId: 'rpc-2', client: 'B', server: 'C', label: 'B->C QueryBalance',
    clientSendMs: -960,   // B: 11:59:59.040 发请求
    serverRecvMs: -1960,  // C: 11:59:58.040 收请求（C 的时钟更慢）
    serverSendMs: -1940,  // C: 11:59:58.060 发响应
    clientRecvMs: -910,   // B: 11:59:59.090 收响应
  },
];

const trace = traceFromRpcQuadruples(rpcs);

console.log('=== 原始日志（各服务器本地物理时钟） ===');
for (const e of trace.events) {
  console.log(`  [${e.server}] ${fmt(e.localTimeMs)}  ${e.label}`);
}

const result = reconstruct(trace);

console.log('\n=== 检测到的物理荒谬（负延迟反例） ===');
for (const v of result.violations) {
  console.log(`  消息 ${v.messageId} (${v.from} -> ${v.to}): 表观延迟 ${v.rawLatencyMs}ms < 0`);
}

console.log('\n=== 因果图强制校正后的时钟基线偏移 ===');
for (const [server, offset] of Object.entries(result.offsets)) {
  console.log(`  offset[${server}] = +${offset}ms`);
}

console.log('\n=== 校正后的消息延迟（物理自洽） ===');
for (const l of result.messageLatencies) {
  console.log(`  ${l.messageId} (${l.from} -> ${l.to}): 原始 ${l.rawLatencyMs}ms  ->  校正 ${l.correctedLatencyMs}ms`);
}

console.log('\n=== 物理自洽的调用拓扑图（因果拓扑序） ===');
const eventById = new Map(result.events.map((e) => [e.id, e]));
result.topologicalOrder.forEach((id, i) => {
  const e = eventById.get(id)!;
  console.log(`  ${i + 1}. [${e.server}] ${fmt(e.correctedTimeMs)}  ${e.label}`);
});

console.log('\n=== 因果边 ===');
for (const edge of result.edges) {
  console.log(`  ${edge.fromEventId} --${edge.kind}--> ${edge.toEventId}`);
}
