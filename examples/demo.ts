import { PeakOverlapDetector } from '../src/peakOverlapDetector.ts';
import type { PeakSnapshot } from '../src/peakOverlapDetector.ts';

function format(snap: PeakSnapshot): string {
  const segs = snap.atomicSegments
    .map((s) => `[${s.start}, ${s.end})`)
    .join(', ');
  return `峰值并发 = ${snap.peak}，峰值原子区间 = ${segs || '(无)'}`;
}

const detector = new PeakOverlapDetector();

console.log('1) 登记设备占用 [0, 5) 与 [5, 9)：');
detector.add('job-a', 0, 5);
console.log('   ' + format(detector.add('job-b', 5, 9)));

console.log('2) 补录迟到区间 [4, 6)：');
console.log('   ' + format(detector.add('job-late', 4, 6)));

console.log('3) 撤回补录区间 [4, 6)：');
console.log('   ' + format(detector.remove('job-late')));
