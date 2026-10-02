import { buildCausalEdges, topologicalSort } from './causalGraph.ts';
import { messageConstraint, solveClockOffsets, type ClockConstraint } from './clockCorrector.ts';
import type {
  CorrectedEvent,
  DistributedTrace,
  LatencyViolation,
  MessageLatency,
  Reconstruction,
  RpcQuadruple,
  ServerId,
  TraceEvent,
} from './types.ts';

export interface ReconstructOptions {
  /** 网络传输的最小物理延迟（毫秒），默认 0（允许瞬时，但禁止负延迟）。 */
  minNetworkLatencyMs?: number;
}

/** 把 RPC 请求/响应四元组展开为“请求 + 响应”两条消息及四个事件。 */
export function traceFromRpcQuadruples(rpcs: RpcQuadruple[]): DistributedTrace {
  const events: TraceEvent[] = [];
  const messages: DistributedTrace['messages'] = [];
  for (const rpc of rpcs) {
    const reqMsg = `${rpc.rpcId}:req`;
    const resMsg = `${rpc.rpcId}:res`;
    const base = rpc.label ?? rpc.rpcId;
    events.push(
      { id: `${reqMsg}@send`, server: rpc.client, kind: 'send', messageId: reqMsg, localTimeMs: rpc.clientSendMs, label: `${base} 请求发送` },
      { id: `${reqMsg}@recv`, server: rpc.server, kind: 'recv', messageId: reqMsg, localTimeMs: rpc.serverRecvMs, label: `${base} 请求接收` },
      { id: `${resMsg}@send`, server: rpc.server, kind: 'send', messageId: resMsg, localTimeMs: rpc.serverSendMs, label: `${base} 响应发送` },
      { id: `${resMsg}@recv`, server: rpc.client, kind: 'recv', messageId: resMsg, localTimeMs: rpc.clientRecvMs, label: `${base} 响应接收` },
    );
    messages.push(
      { id: reqMsg, from: rpc.client, to: rpc.server, sendEventId: `${reqMsg}@send`, recvEventId: `${reqMsg}@recv` },
      { id: resMsg, from: rpc.server, to: rpc.client, sendEventId: `${resMsg}@send`, recvEventId: `${resMsg}@recv` },
    );
  }
  return { events, messages };
}

/**
 * 跨时区/漂移时钟的事件关联与因果图重构。
 *
 * 步骤：
 *  1. 用原始物理时间戳计算每条消息的表观延迟，收集负延迟反例；
 *  2. 由“发送必须先于接收”导出服务器间的差分约束；
 *  3. 求解满足全部约束的最小时钟基线偏移；
 *  4. 用校正后时间重建因果偏序图并给出拓扑序。
 */
export function reconstruct(trace: DistributedTrace, options: ReconstructOptions = {}): Reconstruction {
  const minLatency = options.minNetworkLatencyMs ?? 0;
  const eventById = new Map<string, TraceEvent>(trace.events.map((e) => [e.id, e]));

  for (const m of trace.messages) {
    for (const id of [m.sendEventId, m.recvEventId]) {
      if (!eventById.has(id)) throw new Error(`消息 ${m.id} 引用了不存在的事件 ${id}`);
    }
  }

  const violations: LatencyViolation[] = [];
  const constraints: ClockConstraint[] = [];
  for (const m of trace.messages) {
    const send = eventById.get(m.sendEventId) as TraceEvent;
    const recv = eventById.get(m.recvEventId) as TraceEvent;
    const rawLatency = recv.localTimeMs - send.localTimeMs;
    if (rawLatency < minLatency) {
      violations.push({ messageId: m.id, from: m.from, to: m.to, rawLatencyMs: rawLatency });
    }
    constraints.push(
      messageConstraint(m.from, send.localTimeMs, m.to, recv.localTimeMs, minLatency, `消息 ${m.id} (${m.from} -> ${m.to})`),
    );
  }

  const servers: ServerId[] = [...new Set(trace.events.map((e) => e.server))];
  const offsets = solveClockOffsets(servers, constraints);

  const events: CorrectedEvent[] = trace.events.map((e) => ({
    ...e,
    correctedTimeMs: e.localTimeMs + (offsets.get(e.server) as number),
  }));
  const correctedById = new Map<string, CorrectedEvent>(events.map((e) => [e.id, e]));

  const messageLatencies: MessageLatency[] = trace.messages.map((m) => {
    const send = correctedById.get(m.sendEventId) as CorrectedEvent;
    const recv = correctedById.get(m.recvEventId) as CorrectedEvent;
    return {
      messageId: m.id,
      from: m.from,
      to: m.to,
      rawLatencyMs: recv.localTimeMs - send.localTimeMs,
      correctedLatencyMs: recv.correctedTimeMs - send.correctedTimeMs,
    };
  });

  const edges = buildCausalEdges(events, trace.messages);
  const topologicalOrder = topologicalSort(events.map((e) => e.id), edges);

  return {
    offsets: Object.fromEntries(offsets),
    violations,
    events,
    edges,
    topologicalOrder,
    messageLatencies,
  };
}
