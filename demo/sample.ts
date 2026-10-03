import { parseLog, reconstruct } from '../src/index.ts';
import type { Topology } from '../src/index.ts';

// Acceptance scenario: server A logs REQ_SEND at 12:00:00 (+08:00) while
// server B logs REQ_RECV at 11:59:59-equivalent (03:59:59.000Z) -- physical
// time appears to run backwards because B's clock drifts 1s behind.
// A logs in Asia/Shanghai, B and C log in UTC: timezone differs AND drift
// differs; both are untangled by the causal corrector.
const logs = [
  '# server A (client), Asia/Shanghai wall clock',
  '2026-10-04T12:00:00.000+08:00 [server-A] rpc=order-1 REQ_SEND',
  '2026-10-04T12:00:00.120+08:00 [server-A] rpc=order-1 RESP_RECV',
  '# server B, UTC wall clock, drifts ~1s behind',
  '2026-10-04T03:59:59.000Z [server-B] rpc=order-1 REQ_RECV',
  '2026-10-04T03:59:59.010Z [server-B] rpc=db-1 REQ_SEND',
  '2026-10-04T03:59:59.090Z [server-B] rpc=db-1 RESP_RECV',
  '2026-10-04T03:59:59.100Z [server-B] rpc=order-1 RESP_SEND',
  '# server C, UTC wall clock, drifts ~1.03s behind',
  '2026-10-04T03:59:58.980Z [server-C] rpc=db-1 REQ_RECV',
  '2026-10-04T03:59:59.040Z [server-C] rpc=db-1 RESP_SEND',
].join('\n');

function iso(ms: number): string {
  return new Date(ms).toISOString();
}

function printReport(topology: Topology): void {
  console.log('=== negative-latency counterexamples (raw physical clocks) ===');
  for (const v of topology.violations) {
    console.log(
      '  rpc=' + v.rpcId + ' ' + v.fromServer + ' -> ' + v.toServer +
      '  send=' + iso(v.sendTime) + ' recv=' + iso(v.recvTime) +
      '  latency=' + v.latencyMs + 'ms  <-- impossible',
    );
  }

  console.log('\n=== forced clock-baseline corrections ===');
  for (const c of topology.corrections) {
    console.log('  ' + c.server + ': offset ' + (c.offsetMs >= 0 ? '+' : '') + c.offsetMs + 'ms');
  }

  console.log('\n=== physically self-consistent causal topology (topological order) ===');
  const byId = new Map(topology.events.map((e) => [e.id, e]));
  for (const id of topology.order) {
    const e = byId.get(id)!;
    console.log(
      '  ' + iso(e.correctedTime) + '  ' + e.server.padEnd(9) +
      ' ' + e.kind.padEnd(9) + ' rpc=' + e.rpcId +
      '  (raw ' + iso(e.time) + ')',
    );
  }

  console.log('\n=== corrected message-edge latencies ===');
  for (const edge of topology.edges) {
    if (edge.kind !== 'message') continue;
    const send = byId.get(edge.from)!;
    const recv = byId.get(edge.to)!;
    console.log(
      '  rpc=' + send.rpcId + ' ' + send.server + ' -> ' + recv.server +
      '  latency=' + (recv.correctedTime - send.correctedTime) + 'ms',
    );
  }
}

const events = parseLog(logs);
printReport(reconstruct(events));
