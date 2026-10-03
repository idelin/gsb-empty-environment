import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  parseLog,
  reconstruct,
  estimateClockOffsets,
  buildCausalGraph,
} from '../src/index.ts';
import type { EventKind, TraceEvent } from '../src/index.ts';

let seq = 0;
function ev(server: string, rpcId: string, kind: EventKind, iso: string): TraceEvent {
  seq += 1;
  return { id: 'e' + seq, server, rpcId, kind, time: Date.parse(iso) };
}

function offsetOf(topology: ReturnType<typeof reconstruct>, server: string): number {
  return topology.corrections.find((c) => c.server === server)!.offsetMs;
}

test('acceptance: A sends 12:00:00, B receives 11:59:59 -> B baseline forced forward', () => {
  const events = [
    ev('A', 'r1', 'REQ_SEND', '2026-10-04T12:00:00.000+08:00'),
    ev('B', 'r1', 'REQ_RECV', '2026-10-04T11:59:59.000+08:00'),
    ev('B', 'r1', 'RESP_SEND', '2026-10-04T11:59:59.050+08:00'),
    ev('A', 'r1', 'RESP_RECV', '2026-10-04T12:00:00.120+08:00'),
  ];
  const topology = reconstruct(events);

  assert.equal(topology.violations.length, 1);
  assert.equal(topology.violations[0].latencyMs, -1000);
  assert.equal(topology.violations[0].toServer, 'B');

  assert.equal(offsetOf(topology, 'A'), 0);
  assert.equal(offsetOf(topology, 'B'), 1001);

  const byId = new Map(topology.events.map((e) => [e.id, e]));
  for (const edge of topology.edges) {
    if (edge.kind !== 'message') continue;
    const latency = byId.get(edge.to)!.correctedTime - byId.get(edge.from)!.correctedTime;
    assert.ok(latency >= 1, 'corrected latency must be physically admissible, got ' + latency);
  }
});

test('clock drift is distinct from timezone: same instant in two zones needs no correction', () => {
  const events = [
    ev('A', 'r1', 'REQ_SEND', '2026-10-04T12:00:00.000+08:00'),
    ev('B', 'r1', 'REQ_RECV', '2026-10-04T04:00:00.010Z'),
    ev('B', 'r1', 'RESP_SEND', '2026-10-04T04:00:00.020Z'),
    ev('A', 'r1', 'RESP_RECV', '2026-10-04T12:00:00.050+08:00'),
  ];
  const topology = reconstruct(events);
  assert.equal(topology.violations.length, 0);
  assert.equal(offsetOf(topology, 'A'), 0);
  assert.equal(offsetOf(topology, 'B'), 0);
});

test('chain A -> B -> C propagates corrections transitively', () => {
  const events = [
    ev('A', 'r1', 'REQ_SEND', '2026-10-04T12:00:00.000Z'),
    ev('B', 'r1', 'REQ_RECV', '2026-10-04T11:59:59.000Z'),
    ev('B', 'r2', 'REQ_SEND', '2026-10-04T11:59:59.010Z'),
    ev('C', 'r2', 'REQ_RECV', '2026-10-04T11:59:58.980Z'),
    ev('C', 'r2', 'RESP_SEND', '2026-10-04T11:59:59.040Z'),
    ev('B', 'r2', 'RESP_RECV', '2026-10-04T11:59:59.090Z'),
    ev('B', 'r1', 'RESP_SEND', '2026-10-04T11:59:59.100Z'),
    ev('A', 'r1', 'RESP_RECV', '2026-10-04T12:00:00.120Z'),
  ];
  const topology = reconstruct(events);

  assert.equal(offsetOf(topology, 'A'), 0);
  assert.equal(offsetOf(topology, 'B'), 1001);
  assert.equal(offsetOf(topology, 'C'), 1032);

  const byId = new Map(topology.events.map((e) => [e.id, e]));
  for (const edge of topology.edges) {
    if (edge.kind !== 'message') continue;
    const latency = byId.get(edge.to)!.correctedTime - byId.get(edge.from)!.correctedTime;
    assert.ok(latency >= 1);
  }
});

test('topological order respects happened-before on every edge', () => {
  const events = [
    ev('A', 'r1', 'REQ_SEND', '2026-10-04T12:00:00.000Z'),
    ev('B', 'r1', 'REQ_RECV', '2026-10-04T11:59:59.000Z'),
    ev('B', 'r1', 'RESP_SEND', '2026-10-04T11:59:59.050Z'),
    ev('A', 'r1', 'RESP_RECV', '2026-10-04T12:00:00.120Z'),
  ];
  const topology = reconstruct(events);
  const position = new Map(topology.order.map((id, index) => [id, index]));
  assert.equal(position.size, events.length);
  for (const edge of topology.edges) {
    assert.ok(
      position.get(edge.from)! < position.get(edge.to)!,
      'edge ' + edge.from + ' -> ' + edge.to + ' violated',
    );
  }
});

test('one server gets exactly one baseline shared across all its RPCs', () => {
  const events = [
    ev('A', 'r1', 'REQ_SEND', '2026-10-04T12:00:00.000Z'),
    ev('B', 'r1', 'REQ_RECV', '2026-10-04T11:59:59.500Z'),
    ev('A', 'r2', 'REQ_SEND', '2026-10-04T12:00:01.000Z'),
    ev('B', 'r2', 'REQ_RECV', '2026-10-04T12:00:00.600Z'),
  ];
  const topology = reconstruct(events);
  const bCorrections = topology.corrections.filter((c) => c.server === 'B');
  assert.equal(bCorrections.length, 1);
  // tightest constraint comes from r1: 0 + t + 1 - (t - 500) = 501
  assert.equal(bCorrections[0].offsetMs, 501);
});

test('parser normalises timezone offsets to the same instant', () => {
  const events = parseLog(
    '2026-10-04T12:00:00.000+08:00 [A] rpc=r1 REQ_SEND\n' +
      '2026-10-04T04:00:00.000Z [B] rpc=r1 REQ_RECV',
  );
  assert.equal(events.length, 2);
  assert.equal(events[0].time, events[1].time);
  assert.equal(events[0].server, 'A');
  assert.equal(events[1].kind, 'REQ_RECV');
});

test('parser rejects malformed lines', () => {
  assert.throws(() => parseLog('not a log line'), /unparseable/);
});

test('estimateClockOffsets never moves senders backward', () => {
  const events = [
    ev('A', 'r1', 'REQ_SEND', '2026-10-04T12:00:00.000Z'),
    ev('B', 'r1', 'REQ_RECV', '2026-10-04T11:59:59.000Z'),
  ];
  const { edges } = buildCausalGraph(events);
  const offsets = estimateClockOffsets(events, edges);
  assert.equal(offsets.get('A'), 0);
  assert.ok(offsets.get('B')! > 0);
});
