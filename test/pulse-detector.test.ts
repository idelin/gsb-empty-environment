import { PulseDetector } from '../src/pulse-detector.js';
import type { DetectorOptions, PulseEvent, Sample } from '../src/pulse-detector.js';

type TestFn = () => void;

let passed = 0;
let failed = 0;

function check(name: string, fn: TestFn): void {
  try {
    fn();
    passed = passed + 1;
    console.log('ok - ' + name);
  } catch (err) {
    failed = failed + 1;
    const message = err instanceof Error ? err.message : String(err);
    console.error('FAIL - ' + name + ' :: ' + message);
  }
}

function assertEqual(actual: unknown, expected: unknown, what: string): void {
  if (actual !== expected) {
    throw new Error(what + ' expected ' + String(expected) + ' got ' + String(actual));
  }
}

function assertClose(actual: number, expected: number, what: string): void {
  if (Math.abs(actual - expected) > 1e-9) {
    throw new Error(what + ' expected ' + String(expected) + ' got ' + String(actual));
  }
}

function makeDetector(high: number, low: number, maxGap: number): PulseDetector {
  const opts: DetectorOptions = { highThreshold: high, lowThreshold: low, maxGap: maxGap };
  return new PulseDetector(opts);
}

function feed(det: PulseDetector, values: number[]): void {
  values.forEach((value, index) => {
    const sample: Sample = { time: index, value: value };
    det.push(sample);
  });
}

check('hysteresis: mid-band values stay in event, drop below low closes it', () => {
  const det = makeDetector(10, 6, 0);
  feed(det, [9, 10, 8, 5, 9, 10]);
  const events = det.flush();
  assertEqual(events.length, 2, 'event count');
  const first = events[0];
  assertEqual(first.start, 1, 'first start');
  assertEqual(first.end, 2, 'first end');
  assertEqual(first.complete, true, 'first complete');
  assertEqual(first.peak, 10, 'first peak');
  assertClose(first.integral, 9, 'first integral');
  const second = events[1];
  assertEqual(second.start, 5, 'second start');
  assertEqual(second.end, 5, 'second end');
  assertEqual(second.complete, false, 'second incomplete at flush');
});

check('equal thresholds: enter at >= T, exit at < T, zero gap budget splits', () => {
  const det = makeDetector(10, 10, 0);
  feed(det, [10, 12, 9, 10, 11]);
  const events = det.flush();
  assertEqual(events.length, 2, 'event count');
  const first = events[0];
  assertEqual(first.start, 0, 'first start');
  assertEqual(first.end, 1, 'first end');
  assertEqual(first.complete, true, 'first complete');
  assertEqual(first.peak, 12, 'first peak');
  assertClose(first.integral, 11, 'first integral');
  const second = events[1];
  assertEqual(second.start, 3, 'second start');
  assertEqual(second.end, 4, 'second end');
  assertEqual(second.complete, false, 'second incomplete');
  assertClose(second.integral, 10.5, 'second integral');
});

check('short dips below low are merged and keep peak and integral', () => {
  const det = makeDetector(10, 5, 5);
  feed(det, [12, 11, 2, 11, 12, 3, 12]);
  const events = det.flush();
  assertEqual(events.length, 1, 'event count');
  const ev = events[0];
  assertEqual(ev.start, 0, 'start');
  assertEqual(ev.end, 6, 'end');
  assertEqual(ev.peak, 12, 'peak');
  assertEqual(ev.peakTime, 0, 'peak time');
  assertClose(ev.integral, 51, 'integral spans merged dips');
  assertEqual(ev.complete, false, 'incomplete at flush');
});

check('consecutive gaps: cumulative budget closes event at start of offending gap', () => {
  const det = makeDetector(10, 5, 4);
  feed(det, [12, 11, 2, 11, 2, 2, 11, 12]);
  const events = det.flush();
  assertEqual(events.length, 2, 'event count');
  const first = events[0];
  assertEqual(first.start, 0, 'first start');
  assertEqual(first.end, 3, 'first ends at start of offending gap');
  assertEqual(first.complete, true, 'first complete');
  assertEqual(first.peak, 12, 'first peak');
  assertClose(first.integral, 24.5, 'first integral excludes gaps');
  const second = events[1];
  assertEqual(second.start, 6, 'second start');
  assertEqual(second.end, 7, 'second end');
  assertEqual(second.complete, false, 'second incomplete');
  assertClose(second.integral, 11.5, 'second integral');
});

check('event still in a gap at flush is incomplete and excludes the gap', () => {
  const det = makeDetector(10, 5, 100);
  feed(det, [12, 11, 2, 2]);
  const events = det.flush();
  assertEqual(events.length, 1, 'event count');
  const ev = events[0];
  assertEqual(ev.end, 1, 'end at last good sample');
  assertClose(ev.integral, 11.5, 'integral excludes trailing gap');
  assertEqual(ev.complete, false, 'incomplete');
});

check('late correction that deepens a dip splits the original event', () => {
  const det = makeDetector(10, 5, 2);
  feed(det, [12, 11, 11, 11, 12]);
  const before = det.pending;
  if (before === null) {
    throw new Error('expected a pending event before correction');
  }
  assertEqual(before.start, 0, 'pending start before correction');
  assertClose(before.integral, 45, 'integral before correction');
  const fix1: Sample = { time: 1, value: 2 };
  const fix2: Sample = { time: 2, value: 2 };
  det.insert(fix1);
  det.insert(fix2);
  const events = det.flush();
  assertEqual(events.length, 2, 'event count after split');
  const first = events[0];
  assertEqual(first.start, 0, 'first start');
  assertEqual(first.end, 0, 'first end');
  assertEqual(first.complete, true, 'first complete');
  assertClose(first.integral, 0, 'first integral');
  const second = events[1];
  assertEqual(second.start, 3, 'second start');
  assertEqual(second.end, 4, 'second end');
  assertEqual(second.complete, false, 'second incomplete');
  assertClose(second.integral, 11.5, 'second integral');
});

check('out-of-order insert of a new timestamp recomputes the open event', () => {
  const det = makeDetector(10, 5, 5);
  const s0: Sample = { time: 0, value: 12 };
  const s2: Sample = { time: 2, value: 11 };
  det.push(s0);
  det.push(s2);
  const s1: Sample = { time: 1, value: 11 };
  det.insert(s1);
  const pending = det.pending;
  if (pending === null) {
    throw new Error('expected pending event');
  }
  assertEqual(pending.start, 0, 'start');
  assertEqual(pending.end, 2, 'end');
  assertClose(pending.integral, 22.5, 'integral after insert');
});

check('rejects lowThreshold above highThreshold', () => {
  let threw = false;
  try {
    makeDetector(5, 10, 0);
  } catch (err) {
    threw = true;
  }
  assertEqual(threw, true, 'constructor should throw');
});

check('push rejects non-increasing time', () => {
  const det = makeDetector(10, 5, 5);
  const a: Sample = { time: 2, value: 12 };
  const b: Sample = { time: 1, value: 12 };
  det.push(a);
  let threw = false;
  try {
    det.push(b);
  } catch (err) {
    threw = true;
  }
  assertEqual(threw, true, 'push should throw');
});

console.log(String(passed) + ' passed, ' + String(failed) + ' failed');
if (failed > 0) {
  throw new Error(String(failed) + ' test(s) failed');
}
