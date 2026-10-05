import { PulseDetector, PulseEvent } from '../src/pulse-detector';

let failures = 0;

function check(cond: boolean, msg: string): void {
  if (cond) {
    console.log(`ok   - ${msg}`);
  } else {
    failures++;
    console.error(`FAIL - ${msg}`);
  }
}

function approx(actual: number, expected: number, msg: string): void {
  check(Math.abs(actual - expected) < 1e-9, `${msg} (expected ${expected}, got ${actual})`);
}

function feedAll(d: PulseDetector, values: number[], t0 = 0): PulseEvent[] {
  const out: PulseEvent[] = [];
  values.forEach((v, i) => out.push(...d.feed(t0 + i, v)));
  return out;
}

// --- 1. Equal thresholds (high === low) -------------------------------------
{
  const d = new PulseDetector({ highThreshold: 5, lowThreshold: 5, maxGap: 0, maxTotalGap: 0 });
  check(d.feed(0, 6).length === 0, 'equal thresholds: event opens, nothing finalized');
  check(d.feed(1, 6).length === 0, 'equal thresholds: still open');
  check(d.feed(2, 4).length === 0, 'equal thresholds: dip below threshold starts a gap');
  const fin = d.feed(3, 4);
  check(fin.length === 1, 'equal thresholds: maxGap=0 closes the event on the next sample');
  check(fin[0]!.start === 0 && fin[0]!.end === 2 && fin[0]!.complete, 'equal thresholds: event [0,2] complete');
  approx(fin[0]!.peak, 6, 'equal thresholds: peak');
  d.feed(4, 6);
  const tail = d.flush();
  check(tail !== null && tail.complete === false && tail.start === 4 && tail.end === 4,
    'equal thresholds: trailing open event flushed as incomplete');
}

// --- 1b. Config validation ---------------------------------------------------
{
  let threw = false;
  try {
    new PulseDetector({ highThreshold: 5, lowThreshold: 6, maxGap: 0, maxTotalGap: 0 });
  } catch {
    threw = true;
  }
  check(threw, 'rejects lowThreshold > highThreshold');

  threw = false;
  try {
    new PulseDetector({ highThreshold: 5, lowThreshold: 5, maxGap: -1, maxTotalGap: 0 });
  } catch {
    threw = true;
  }
  check(threw, 'rejects negative maxGap');
}

// --- 2. Consecutive gaps bounded by cumulative limit --------------------------
{
  const d = new PulseDetector({ highThreshold: 10, lowThreshold: 5, maxGap: 2, maxTotalGap: 3 });
  // gaps: t=2 (dur 1), t=5..6 (dur 2) -> cumulative 3, still one event.
  // third dip at t=9 would push cumulative to 4 > 3, so the event closes at t=9.
  const finalized = feedAll(d, [12, 12, 1, 12, 12, 1, 1, 12, 12, 1, 1]);
  check(finalized.length === 1, 'consecutive gaps: exactly one event finalized');
  const e = finalized[0]!;
  check(e.start === 0 && e.end === 9 && e.complete, 'consecutive gaps: event [0,9] closed at the third dip');
  approx(e.totalGap, 3, 'consecutive gaps: cumulative gap total');
  approx(e.peak, 12, 'consecutive gaps: peak');
  approx(e.integral, 69.5, 'consecutive gaps: integral includes bridged dips');

  d.feed(11, 12);
  const tail = d.flush();
  check(tail !== null && !tail.complete && tail.start === 11, 'consecutive gaps: new event after close is incomplete at flush');
}

// --- 2b. A single gap longer than maxGap splits events ------------------------
{
  const d = new PulseDetector({ highThreshold: 10, lowThreshold: 5, maxGap: 2, maxTotalGap: 100 });
  const finalized = feedAll(d, [12, 1, 1, 1, 1]);
  check(finalized.length === 1 && finalized[0]!.start === 0 && finalized[0]!.end === 1,
    'long gap: event closes at gap start when maxGap exceeded');
  d.feed(5, 12);
  const p = d.pending();
  check(p !== null && p.start === 5, 'long gap: a later pulse opens a fresh event');
}

// --- 3. Late correction splits an already-finalized event ---------------------
{
  const d = new PulseDetector({ highThreshold: 10, lowThreshold: 5, maxGap: 2, maxTotalGap: 100 });
  const finalized = feedAll(d, [12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 1, 1, 1, 1]);
  check(finalized.length === 1 && finalized[0]!.start === 0 && finalized[0]!.end === 10,
    'correction: single event [0,10] before corrections');

  let r = d.correct(4, 1);
  check(r.retracted.length === 0 && r.emitted.length === 0, 'correction: dip of 1 sample still bridged');
  r = d.correct(5, 1);
  check(r.retracted.length === 0 && r.emitted.length === 0, 'correction: dip of 2 samples still bridged');
  r = d.correct(6, 1);
  check(r.retracted.length === 1 && r.retracted[0]!.start === 0 && r.retracted[0]!.end === 10,
    'correction: original event retracted once the dip exceeds maxGap');
  check(r.emitted.length === 2, 'correction: original event split into two');
  const a = r.emitted[0]!;
  const b = r.emitted[1]!;
  check(a.start === 0 && a.end === 4 && a.complete, 'correction: first half [0,4]');
  check(b.start === 7 && b.end === 10 && b.complete, 'correction: second half [7,10]');
  check(d.events().length === 2, 'correction: detector state consistent after split');
}

// --- 3b. correct() inserts a missing sample -----------------------------------
{
  const d = new PulseDetector({ highThreshold: 10, lowThreshold: 5, maxGap: 2, maxTotalGap: 100 });
  feedAll(d, [12, 12, 12]);
  const r = d.correct(1.5, 20);
  check(r.retracted.length === 0 && r.emitted.length === 0, 'insert: no finalized events affected');
  const p = d.pending();
  check(p !== null && p.peak === 20 && p.peakTime === 1.5, 'insert: late sample updates the open event peak');
}

// --- 4. Peak and integral on a plain event ------------------------------------
{
  const d = new PulseDetector({ highThreshold: 2, lowThreshold: 1, maxGap: 0, maxTotalGap: 0 });
  check(d.pending() === null, 'plain: nothing pending before data');
  d.feed(0, 3);
  const p = d.pending();
  check(p !== null && !p.complete && p.end === null, 'plain: pending event exposed while streaming');
  const finalized = feedAll(d, [5, 4, 0, 0], 1);
  check(finalized.length === 1, 'plain: event finalized');
  const e = finalized[0]!;
  approx(e.peak, 5, 'plain: peak value');
  approx(e.peakTime, 1, 'plain: peak time');
  approx(e.integral, 10.5, 'plain: trapezoidal integral');
  check(e.end === 3, 'plain: ends at first sample below low threshold');
}

// --- 4b. Out-of-order feed rejected -------------------------------------------
{
  const d = new PulseDetector({ highThreshold: 2, lowThreshold: 1, maxGap: 0, maxTotalGap: 0 });
  d.feed(1, 5);
  let threw = false;
  try {
    d.feed(1, 5);
  } catch {
    threw = true;
  }
  check(threw, 'feed() rejects non-increasing timestamps');
}

if (failures > 0) {
  console.error(`\n${failures} test(s) failed`);
  throw new Error('test failures');
}
console.log('\nall tests passed');
