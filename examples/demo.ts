import { acceptanceScenario } from '../src/simulation.ts';

const CAPACITY_KBPS = 1500;
const DURATION_MS = 10_000;

console.log('DiffServ priority mapper demo');
console.log(`Bottleneck link: ${CAPACITY_KBPS} kbit/s, duration: ${DURATION_MS / 1000}s`);
console.log('Offered load: voice EF 64 kbit/s | video AF41 480 kbit/s | bulk BE 6000 kbit/s');
console.log('Aggregate demand ~6.5 Mbit/s >> 1.5 Mbit/s capacity (congestion)\n');

const { stats } = acceptanceScenario(DURATION_MS);

console.log(
  'flow        packets      bytes   avg-lat(ms)   max-lat(ms)   throughput(kbit/s)',
);
for (const [flowId, s] of stats) {
  console.log(
    flowId.padEnd(11),
    String(s.packets).padStart(8),
    String(s.bytes).padStart(10),
    s.avgLatencyMs.toFixed(3).padStart(12),
    s.maxLatencyMs.toFixed(3).padStart(13),
    s.throughputKbps.toFixed(1).padStart(18),
  );
}

const ef = stats.get('voice-ef');
const be = stats.get('bulk-be');
if (ef && be) {
  console.log('\nSLA check:');
  console.log(`  EF voice avg latency ${ef.avgLatencyMs.toFixed(3)} ms (ultra-low, strict priority)`);
  console.log(`  BE bulk throughput ${be.throughputKbps.toFixed(1)} kbit/s (suppressed to leftover capacity)`);
}
