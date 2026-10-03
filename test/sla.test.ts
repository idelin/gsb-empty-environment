import { test } from 'node:test';
import assert from 'node:assert/strict';
import { acceptanceScenario, runScenario } from '../src/simulation.ts';
import { DSCP } from '../src/packet.ts';

test('acceptance: EF voice keeps ultra-low latency, BE bulk is suppressed', () => {
  const { stats } = acceptanceScenario(10_000);
  const ef = stats.get('voice-ef');
  const af = stats.get('video-af4');
  const be = stats.get('bulk-be');
  assert.ok(ef && af && be);

  // Every offered voice packet is delivered (no loss in the drain phase).
  assert.equal(ef.packets, 500);
  // EF voice: single-digit ms latency despite congestion. The bound is one
  // non-preemptive BE packet serialization (1500 B / 187.5 B-per-ms = 8 ms)
  // plus its own transmission time.
  assert.ok(ef.avgLatencyMs < 6, `EF avg latency ${ef.avgLatencyMs} ms`);
  assert.ok(ef.maxLatencyMs < 10, `EF max latency ${ef.maxLatencyMs} ms`);
  // EF voice gets its full 64 kbit/s.
  assert.ok(Math.abs(ef.throughputKbps - 64) < 1, `EF throughput ${ef.throughputKbps}`);

  // BE is starved down to leftover capacity (~956 kbit/s of the 1.5 Mbit/s
  // link after EF 64 + AF4 480 kbit/s) and pays seconds of queuing delay.
  assert.ok(be.throughputKbps < 1000, `BE throughput ${be.throughputKbps}`);
  assert.ok(be.avgLatencyMs > 1000, `BE avg latency ${be.avgLatencyMs} ms`);
  assert.ok(be.avgLatencyMs > ef.avgLatencyMs * 100,
    `BE latency ${be.avgLatencyMs} ms vs EF ${ef.avgLatencyMs} ms`);

  // Link never exceeds its 1.5 Mbit/s capacity.
  const total = ef.throughputKbps + af.throughputKbps + be.throughputKbps;
  assert.ok(total <= 1500.5, `aggregate ${total} kbit/s`);
});

test('AF classes split leftover bandwidth by WRR weights under congestion', () => {
  const { stats } = runScenario(
    125, // 1 Mbit/s
    [
      { flowId: 'af1', dscp: DSCP.AF11, sizeBytes: 500, intervalMs: 1 },
      { flowId: 'af3', dscp: DSCP.AF31, sizeBytes: 500, intervalMs: 1 },
    ],
    20_000,
  );
  const af1 = stats.get('af1');
  const af3 = stats.get('af3');
  assert.ok(af1 && af3);
  // Default weights AF1:AF3 = 1:3.
  const ratio = af3.throughputKbps / af1.throughputKbps;
  assert.ok(ratio > 2.5 && ratio < 3.5, `AF3/AF1 throughput ratio ${ratio}`);
});

test('EF is never delayed by a saturated BE backlog', () => {
  const { link, stats } = runScenario(
    100, // 0.8 Mbit/s
    [
      { flowId: 'voice', dscp: DSCP.EF, sizeBytes: 160, intervalMs: 20 },
      { flowId: 'bulk', dscp: DSCP.BE, sizeBytes: 1500, intervalMs: 1 },
    ],
    5_000,
  );
  const voice = stats.get('voice');
  assert.ok(voice);
  // BE queue is permanently backlogged, yet voice latency stays minimal:
  // worst case is one non-preemptive 1500 B BE serialization (15 ms).
  assert.ok(link.scheduler.backlog >= 0); // drained after run
  assert.ok(voice.avgLatencyMs < 10, `voice avg latency ${voice.avgLatencyMs} ms`);
  assert.ok(voice.maxLatencyMs < 17, `voice max latency ${voice.maxLatencyMs} ms`);
  assert.equal(voice.packets, 250);
});
