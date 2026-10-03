import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DiffServScheduler } from '../src/scheduler.ts';
import { simulateLink, statsByQueue } from '../src/link.ts';
import { voiceFlow, bulkFlow, cbrFlow, resetPacketIds } from '../src/traffic.ts';
import { DSCP } from '../src/packet.ts';
import type { Packet, QueueId } from '../src/packet.ts';

const LINK_BPS = 1_000_000; // 8 Mbps bottleneck link
const DURATION = 5; // seconds

function buildMixedTraffic(): { packets: Packet[]; offered: Record<string, number> } {
  resetPacketIds();
  const voice = voiceFlow('voice', 0, DURATION); // EF, 160B / 20ms = 8 kB/s
  const video = cbrFlow({
    flowId: 'video',
    dscp: DSCP.AF41,
    start: 0,
    duration: DURATION,
    interval: 1500 / 400_000, // AF4, 400 kB/s offered
    sizeBytes: 1500,
  });
  const download = bulkFlow('download', 0, DURATION, 2_000_000); // BE, 2x link capacity
  return {
    packets: [...voice, ...video, ...download],
    offered: { voice: 8_000, video: 400_000, download: 2_000_000 },
  };
}

function perFlow(result: ReturnType<typeof simulateLink>, windowSec = DURATION) {
  const byFlow = new Map<string, { bytes: number; delaysMs: number[]; queue: QueueId }>();
  for (const rec of result.records) {
    // Only packets transmitted inside the measurement window count as
    // delivered goodput; backlog draining afterwards is not.
    if (rec.startTime >= windowSec) continue;
    const id = rec.packet.flowId!;
    const entry = byFlow.get(id) ?? { bytes: 0, delaysMs: [], queue: rec.queue };
    entry.bytes += rec.packet.sizeBytes;
    entry.delaysMs.push(rec.queueingDelay * 1000);
    byFlow.set(id, entry);
  }
  return byFlow;
}

test('SLA: EF voice keeps ultra-low latency while BE download is suppressed', () => {
  const { packets, offered } = buildMixedTraffic();
  const result = simulateLink(packets, LINK_BPS);
  const flows = perFlow(result);

  const voice = flows.get('voice')!;
  const video = flows.get('video')!;
  const download = flows.get('download')!;

  // Every packet of every flow is eventually forwarded (no drops, only delay).
  const voiceOffered = Math.floor(DURATION / 0.02);
  assert.equal(voice.delaysMs.length, voiceOffered);

  // EF voice: ultra-low latency. Worst case is bounded by one non-preemptive
  // MTU transmission (1500B / 1MB/s = 1.5 ms) plus its own 0.16 ms.
  const voiceMax = Math.max(...voice.delaysMs);
  const voiceAvg = voice.delaysMs.reduce((a, b) => a + b, 0) / voice.delaysMs.length;
  assert.ok(voiceMax < 5, `EF max delay ${voiceMax.toFixed(3)} ms must stay < 5 ms`);
  assert.ok(voiceAvg < 2, `EF avg delay ${voiceAvg.toFixed(3)} ms must stay < 2 ms`);

  // EF voice: zero throughput loss — full goodput delivered despite congestion.
  const voiceThroughput = voice.bytes / DURATION;
  assert.ok(
    voiceThroughput >= offered.voice * 0.99,
    `EF goodput ${voiceThroughput} B/s must match offered ${offered.voice} B/s`,
  );

  // AF video: guaranteed bandwidth fully delivered (400 kB/s < remaining capacity).
  const videoThroughput = video.bytes / DURATION;
  assert.ok(
    videoThroughput >= offered.video * 0.99,
    `AF goodput ${videoThroughput} B/s must match offered ${offered.video} B/s`,
  );

  // BE download: actively suppressed — only leftover capacity (~592 kB/s to
  // be precise: 1 MB/s link minus EF and AF) instead of the offered 2 MB/s.
  const beThroughput = download.bytes / DURATION;
  assert.ok(
    beThroughput < offered.download * 0.35,
    `BE throughput ${beThroughput.toFixed(0)} B/s must be suppressed well below offered ${offered.download} B/s`,
  );

  // BE pays the price in delay: its backlog grows, so its average delay must
  // be orders of magnitude worse than EF's.
  const beAvg = download.delaysMs.reduce((a, b) => a + b, 0) / download.delaysMs.length;
  assert.ok(
    beAvg > voiceAvg * 100,
    `BE avg delay ${beAvg.toFixed(1)} ms must be >100x EF avg delay ${voiceAvg.toFixed(3)} ms`,
  );

  // Sanity: class-level stats agree with the queue mapping.
  const stats = statsByQueue(result, DURATION);
  assert.equal(stats.find((s) => s.queue === 'EF')!.packets, voiceOffered);
  assert.ok(stats.find((s) => s.queue === 'BE')!.avgDelayMs > stats.find((s) => s.queue === 'EF')!.maxDelayMs);
});

test('SLA: congested AF classes share spare bandwidth by WRR weights', () => {
  resetPacketIds();
  // Two saturated AF flows, weights AF4:AF1 = 4:1, no EF/BE traffic.
  const af4 = cbrFlow({ flowId: 'af4', dscp: DSCP.AF41, start: 0, duration: DURATION, interval: 1500 / 900_000, sizeBytes: 1500 });
  const af1 = cbrFlow({ flowId: 'af1', dscp: DSCP.AF11, start: 0, duration: DURATION, interval: 1500 / 900_000, sizeBytes: 1500 });
  const result = simulateLink([...af4, ...af1], LINK_BPS, new DiffServScheduler());
  const flows = perFlow(result);

  const t4 = flows.get('af4')!.bytes / DURATION;
  const t1 = flows.get('af1')!.bytes / DURATION;
  const ratio = t4 / t1;
  assert.ok(
    Math.abs(ratio - 4) < 0.2,
    `AF4:AF1 throughput ratio ${ratio.toFixed(2)} must approximate weight ratio 4:1`,
  );
  // The link is fully utilized by the two AF classes.
  assert.ok(t4 + t1 > LINK_BPS * 0.95, `AF total ${t4 + t1} B/s must saturate the link`);
});

test('SLA: EF preempts congested AF+BE mix even under extreme overload', () => {
  resetPacketIds();
  const voice = voiceFlow('voice', 0, DURATION);
  const afStorm = cbrFlow({ flowId: 'afStorm', dscp: DSCP.AF41, start: 0, duration: DURATION, interval: 1500 / 1_500_000, sizeBytes: 1500 });
  const beStorm = bulkFlow('beStorm', 0, DURATION, 3_000_000);
  const result = simulateLink([...voice, ...afStorm, ...beStorm], LINK_BPS);
  const flows = perFlow(result);

  const voiceDelays = flows.get('voice')!.delaysMs;
  const voiceMax = Math.max(...voiceDelays);
  assert.ok(voiceMax < 5, `EF max delay ${voiceMax.toFixed(3)} ms must stay < 5 ms under overload`);

  // BE is starved down to whatever EF+AF leave behind.
  const beThroughput = (flows.get('beStorm')?.bytes ?? 0) / DURATION;
  assert.ok(beThroughput < 100_000, `BE throughput ${beThroughput.toFixed(0)} B/s must be starved`);
});
