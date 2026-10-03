import { PeakConcurrencyTracker } from '../src/peak-concurrency.ts';
import type { PeakResult } from '../src/peak-concurrency.ts';

function format(peak: PeakResult): string {
  const segments = peak.segments.map((s) => `[${s.start}, ${s.end})`).join(', ');
  return `peak=${peak.value}  segments: ${segments || '(none)'}`;
}

const tracker = new PeakConcurrencyTracker();

console.log('Step 1: device A occupies [0, 5)');
console.log('  ' + format(tracker.add('A', 0, 5)));

console.log('Step 2: device B occupies [5, 9) — adjacent, no overlap');
console.log('  ' + format(tracker.add('B', 5, 9)));

console.log('Step 3: late backfill arrives — device C occupied [4, 6)');
console.log('  ' + format(tracker.add('C', 4, 6)));

console.log('Step 4: backfilled interval C is retracted');
console.log('  ' + format(tracker.remove('C')));
