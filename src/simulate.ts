import { writeFileSync } from 'node:fs';
import { buildScenario } from './scenario.ts';

// --- Record phase -----------------------------------------------------------
const kernel = buildScenario();
const counts = { packet: 0, timeout: 0, reseed: 0 };
for (const e of kernel.eventLog) counts[e.kind] += 1;

console.log('=== deterministic replay kernel — recording sample ===');
console.log(`kernel seed        : 0x${kernel.seed.toString(16)}`);
console.log(`events recorded    : ${kernel.eventCount}`);
console.log(`  packet arrivals  : ${counts.packet}`);
console.log(`  clock timeouts   : ${counts.timeout}`);
console.log(`  seed injections  : ${counts.reseed}`);
console.log('');
console.log('first 5 log entries:');
for (const e of kernel.eventLog.slice(0, 5)) {
  console.log(`  tick=${String(e.tick).padStart(3)} seq=${e.seq} ${e.kind} ${JSON.stringify(e.payload)}`);
}
console.log('');
console.log('snapshot hashes (first 5 of ' + kernel.snapshots.length + '):');
for (const s of kernel.snapshots.slice(0, 5)) {
  console.log(`  #${String(s.index).padStart(2)} tick=${String(s.tick).padStart(3)} ${s.kind.padEnd(7)} sha256=${s.hash}`);
}

// --- Time-travel / replay phase ---------------------------------------------
const TARGET = 35;
console.log('');
console.log(`=== time travel: jump to event #${TARGET} ===`);
const recorded = kernel.snapshots[TARGET - 1];
const traveled = kernel.travelTo(TARGET);
console.log(`recorded snapshot  : sha256=${recorded.hash}`);
console.log(`replayed state     : sha256=${traveled.hash}`);
console.log(`byte-identical     : ${traveled.bytes.equals(recorded.bytes)}`);

console.log('');
console.log('=== verifying every historical tick ===');
let allMatch = true;
for (let i = 1; i <= kernel.eventCount; i += 1) {
  if (!kernel.verifyAt(i).match) {
    allMatch = false;
    console.log(`  MISMATCH at event #${i}`);
  }
}
console.log(`all ${kernel.eventCount} snapshots reproduce byte-identically: ${allMatch}`);

// Persist the trace for inspection.
writeFileSync(
  'trace.json',
  JSON.stringify(
    {
      seed: kernel.seed,
      events: kernel.eventLog,
      snapshotHashes: kernel.snapshots.map((s) => ({ index: s.index, tick: s.tick, kind: s.kind, hash: s.hash })),
    },
    null,
    2,
  ),
);
console.log('');
console.log('trace written to trace.json');
