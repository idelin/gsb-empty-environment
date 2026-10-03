/**
 * Demo: EF voice + AF video + BE bulk download sharing one congested link.
 *
 * Run with:  npm run demo        (requires Node >= 22.6)
 */
import { DiffServScheduler } from '../src/scheduler.ts';
import { simulateLink, statsByQueue } from '../src/link.ts';
import { voiceFlow, bulkFlow, cbrFlow, resetPacketIds } from '../src/traffic.ts';
import { DSCP } from '../src/packet.ts';

const LINK_BPS = 1_000_000; // 8 Mbps bottleneck link
const DURATION = 5; // seconds

resetPacketIds();
const voice = voiceFlow('voice-g711', 0, DURATION); // EF: 160B every 20ms (64 kbps)
const video = cbrFlow({
  flowId: 'video-conf',
  dscp: DSCP.AF41,
  start: 0,
  duration: DURATION,
  interval: 1500 / 400_000, // AF4: 3.2 Mbps offered
  sizeBytes: 1500,
});
const download = bulkFlow('bulk-download', 0, DURATION, 2_000_000); // BE: 16 Mbps offered

console.log('=== DiffServ link simulation ===');
console.log(`Link capacity : ${(LINK_BPS / 125_000).toFixed(1)} Mbps`);
console.log(`Offered load  : voice 0.064 Mbps (EF) + video 3.2 Mbps (AF4) + download 16 Mbps (BE)`);
console.log(`Total offered : ${(0.064 + 3.2 + 16).toFixed(1)} Mbps  ->  ${((0.064 + 3.2 + 16) / 8 * 100).toFixed(0)}% overload\n`);

const result = simulateLink([...voice, ...video, ...download], LINK_BPS, new DiffServScheduler());
const stats = statsByQueue(result, DURATION);

console.log('queue  packets    avg delay     max delay   delivered');
console.log('-----  -------    ---------     ---------   ---------');
for (const s of stats) {
  const mbps = (s.throughputBps / 125_000).toFixed(3).padStart(7);
  console.log(
    `${s.queue.padEnd(5)}  ${String(s.packets).padEnd(9)} ${s.avgDelayMs.toFixed(2).padStart(8)} ms   ${s.maxDelayMs.toFixed(2).padStart(8)} ms   ${mbps} Mbps`,
  );
}

const ef = stats.find((s) => s.queue === 'EF')!;
const be = stats.find((s) => s.queue === 'BE')!;
console.log('\n=== SLA verdict ===');
console.log(`EF voice max delay  : ${ef.maxDelayMs.toFixed(2)} ms   (SLA: < 5 ms)   -> ${ef.maxDelayMs < 5 ? 'PASS' : 'FAIL'}`);
console.log(`EF voice goodput    : ${(ef.throughputBps / 125_000).toFixed(3)} Mbps (SLA: 0.064 Mbps) -> ${ef.throughputBps >= 8_000 * 0.99 ? 'PASS' : 'FAIL'}`);
console.log(`BE download goodput : ${(be.throughputBps / 125_000).toFixed(3)} Mbps (offered 16 Mbps, suppressed to leftover) -> ${be.throughputBps < 2_000_000 * 0.35 ? 'PASS' : 'FAIL'}`);
