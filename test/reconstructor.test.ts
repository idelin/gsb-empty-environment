import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  InconsistentTraceError,
  reconstruct,
  solveClockOffsets,
  traceFromRpcQuadruples,
} from '../src/index.ts';
import type { RpcQuadruple } from '../src/index.ts';

/** 验收场景：A 12:00:00 发请求，B 11:59:59 收请求（时钟倒流 1s）。 */
const acceptanceRpc: RpcQuadruple = {
  rpcId: 'rpc-1', client: 'A', server: 'B',
  clientSendMs: 0, serverRecvMs: -1000, serverSendMs: -970, clientRecvMs: 45,
};

describe('跨时区事件关联与因果图重构器', () => {
  it('检测出负延迟反例（物理时钟倒流）', () => {
    const result = reconstruct(traceFromRpcQuadruples([acceptanceRpc]));
    assert.equal(result.violations.length, 1);
    assert.equal(result.violations[0].messageId, 'rpc-1:req');
    assert.equal(result.violations[0].rawLatencyMs, -1000);
  });

  it('强制校正 B 的时钟基线，消除负延迟', () => {
    const result = reconstruct(traceFromRpcQuadruples([acceptanceRpc]));
    assert.equal(result.offsets['A'], 0);
    assert.equal(result.offsets['B'], 1000);
    for (const l of result.messageLatencies) {
      assert.ok(l.correctedLatencyMs >= 0, `${l.messageId} 校正后仍为负延迟`);
    }
    const req = result.messageLatencies.find((l) => l.messageId === 'rpc-1:req');
    assert.equal(req?.correctedLatencyMs, 0);
  });

  it('保留服务器本地处理时长（同一时钟域内差值不变）', () => {
    const result = reconstruct(traceFromRpcQuadruples([acceptanceRpc]));
    const recvReq = result.events.find((e) => e.id === 'rpc-1:req@recv');
    const sendRes = result.events.find((e) => e.id === 'rpc-1:res@send');
    assert.ok(recvReq && sendRes);
    assert.equal(sendRes.correctedTimeMs - recvReq.correctedTimeMs, 30);
  });

  it('沿调用链传递校正（A -> B -> C）', () => {
    const rpcs: RpcQuadruple[] = [
      acceptanceRpc,
      { rpcId: 'rpc-2', client: 'B', server: 'C',
        clientSendMs: -960, serverRecvMs: -1960, serverSendMs: -1940, clientRecvMs: -910 },
    ];
    const result = reconstruct(traceFromRpcQuadruples(rpcs));
    assert.deepEqual(result.offsets, { A: 0, B: 1000, C: 2000 });
    for (const l of result.messageLatencies) {
      assert.ok(l.correctedLatencyMs >= 0, `${l.messageId} 校正后仍为负延迟`);
    }
  });

  it('尊重最小网络延迟约束（minNetworkLatencyMs）', () => {
    const result = reconstruct(traceFromRpcQuadruples([acceptanceRpc]), { minNetworkLatencyMs: 5 });
    assert.equal(result.offsets['B'], 1005);
    for (const l of result.messageLatencies) {
      assert.ok(l.correctedLatencyMs >= 5, `${l.messageId} 低于最小网络延迟`);
    }
  });

  it('拓扑序满足因果偏序：每条边的起点都先于终点', () => {
    const rpcs: RpcQuadruple[] = [
      acceptanceRpc,
      { rpcId: 'rpc-2', client: 'B', server: 'C',
        clientSendMs: -960, serverRecvMs: -1960, serverSendMs: -1940, clientRecvMs: -910 },
    ];
    const result = reconstruct(traceFromRpcQuadruples(rpcs));
    const position = new Map(result.topologicalOrder.map((id, i) => [id, i]));
    assert.equal(position.size, 8);
    for (const edge of result.edges) {
      assert.ok(
        (position.get(edge.fromEventId) as number) < (position.get(edge.toEventId) as number),
        `边 ${edge.fromEventId} -> ${edge.toEventId} 违反拓扑序`,
      );
    }
  });

  it('时钟无漂移时偏移全为 0 且无反例', () => {
    const rpc: RpcQuadruple = {
      rpcId: 'rpc-ok', client: 'A', server: 'B',
      clientSendMs: 0, serverRecvMs: 10, serverSendMs: 40, clientRecvMs: 55,
    };
    const result = reconstruct(traceFromRpcQuadruples([rpc]));
    assert.deepEqual(result.offsets, { A: 0, B: 0 });
    assert.equal(result.violations.length, 0);
  });

  it('自相矛盾的日志（正环）抛出 InconsistentTraceError', () => {
    // A->B 要求 offset[B] >= offset[A] + 100，B->A 又要求 offset[A] >= offset[B] + 100。
    assert.throws(
      () =>
        solveClockOffsets(['A', 'B'], [
          { from: 'A', to: 'B', minGapMs: 100, reason: 'm1' },
          { from: 'B', to: 'A', minGapMs: 100, reason: 'm2' },
        ]),
      InconsistentTraceError,
    );
  });

  it('拒绝引用不存在事件的消息', () => {
    assert.throws(() =>
      reconstruct({
        events: [{ id: 'e1', server: 'A', kind: 'send', messageId: 'm', localTimeMs: 0 }],
        messages: [{ id: 'm', from: 'A', to: 'B', sendEventId: 'e1', recvEventId: 'ghost' }],
      }),
    );
  });
});
