import { strict as assert } from 'assert';
import { ChunkedUploadProgress, ProgressSnapshot } from '../src/uploadProgress';

type TestFn = () => void;
const tests: Array<[string, TestFn]> = [];
function test(name: string, fn: TestFn): void {
  tests.push([name, fn]);
}

const CHUNK = 1000;
/** totalBytes = 2500 -> chunks of 1000, 1000, 500 */
function makeTracker(totalBytes = 2500): ChunkedUploadProgress {
  return new ChunkedUploadProgress(totalBytes, CHUNK);
}

function assertNeverExceedsTotal(s: ProgressSnapshot): void {
  assert.ok(s.confirmedBytes <= s.totalBytes, `confirmed ${s.confirmedBytes} > total ${s.totalBytes}`);
  assert.ok(s.confirmedBytes + s.temporaryBytes <= s.totalBytes,
    `confirmed+temp ${s.confirmedBytes + s.temporaryBytes} > total ${s.totalBytes}`);
  assert.ok(s.displayRatio <= 1 && s.displayRatio >= 0);
  assert.ok(s.reliableRatio <= 1 && s.reliableRatio >= 0);
}

test('confirmed bytes accumulate only via completion receipts', () => {
  const t = makeTracker();
  const a = t.beginAttempt();
  assert.equal(t.snapshot().confirmedBytes, 0);
  assert.equal(t.confirmChunk(0, a), true);
  assert.equal(t.snapshot().confirmedBytes, 1000);
  assert.equal(t.confirmChunk(2, a), true);
  const s = t.snapshot();
  assert.equal(s.confirmedBytes, 1500);
  assert.deepEqual(s.confirmedChunks, [0, 2]);
  assertNeverExceedsTotal(s);
});

test('transfer progress is temporary and capped at chunk size', () => {
  const t = makeTracker();
  const a = t.beginAttempt();
  t.reportTransfer(0, a, 400);
  assert.equal(t.snapshot().temporaryBytes, 400);
  t.reportTransfer(0, a, 99999);
  const s = t.snapshot();
  assert.equal(s.temporaryBytes, 1000);
  assertNeverExceedsTotal(s);
});

test('out-of-order transfer reports never decrease temporary progress', () => {
  const t = makeTracker();
  const a = t.beginAttempt();
  t.reportTransfer(1, a, 800);
  t.reportTransfer(1, a, 300); // stale, out of order
  t.reportTransfer(1, a, 800); // duplicate
  assert.equal(t.snapshot().temporaryBytes, 800);
  t.reportTransfer(1, a, 950);
  t.reportTransfer(1, a, 100);
  assert.equal(t.snapshot().temporaryBytes, 950);
});

test('retry rolls back temporary values but keeps confirmed values', () => {
  const t = makeTracker();
  const a = t.beginAttempt();
  t.reportTransfer(0, a, 700);
  t.confirmChunk(1, a);
  t.reportTransfer(2, a, 200);
  assert.equal(t.snapshot().temporaryBytes, 900);
  t.rollbackAttempt(a);
  const s = t.snapshot();
  assert.equal(s.temporaryBytes, 0);
  assert.equal(s.confirmedBytes, 1000);
  assert.deepEqual(s.confirmedChunks, [1]);
});

test('events from old attempts are ignored after retry', () => {
  const t = makeTracker();
  const a1 = t.beginAttempt();
  t.reportTransfer(0, a1, 900);
  t.confirmChunk(1, a1);
  const a2 = t.beginAttempt(); // retry: temp of a1 rolled back
  assert.equal(t.snapshot().temporaryBytes, 0);
  assert.equal(t.snapshot().confirmedBytes, 1000);
  // late events tagged with the old attempt id must be ignored
  assert.equal(t.reportTransfer(0, a1, 1000), false);
  assert.equal(t.confirmChunk(2, a1), false);
  assert.equal(t.rollbackAttempt(a1), undefined);
  const s = t.snapshot();
  assert.equal(s.temporaryBytes, 0);
  assert.equal(s.confirmedBytes, 1000);
  // current attempt still works
  assert.equal(t.reportTransfer(0, a2, 500), true);
  assert.equal(t.confirmChunk(2, a2), true);
  assertNeverExceedsTotal(t.snapshot());
});

test('pause preserves the confirmed set and discards temporary progress', () => {
  const t = makeTracker();
  const a = t.beginAttempt();
  t.confirmChunk(0, a);
  t.confirmChunk(2, a);
  t.reportTransfer(1, a, 600);
  t.pause();
  const s = t.snapshot();
  assert.equal(s.confirmedBytes, 1500);
  assert.deepEqual(s.confirmedChunks, [0, 2]);
  assert.equal(s.temporaryBytes, 0);
  // late events from the pre-pause attempt are ignored
  assert.equal(t.reportTransfer(1, a, 1000), false);
  assert.equal(t.confirmChunk(1, a), false);
  // resume continues from the confirmed set
  const a2 = t.resume();
  t.reportTransfer(1, a2, 1000);
  t.confirmChunk(1, a2);
  const done = t.snapshot();
  assert.equal(done.confirmedBytes, 2500);
  assert.equal(done.reliableRatio, 1);
  assertNeverExceedsTotal(done);
});

test('duplicate completion receipts never double-count a chunk', () => {
  const t = makeTracker();
  const a = t.beginAttempt();
  assert.equal(t.confirmChunk(0, a), true);
  assert.equal(t.confirmChunk(0, a), false); // duplicate receipt
  assert.equal(t.confirmChunk(0, a), false);
  t.reportTransfer(0, a, 500); // transfer on confirmed chunk ignored
  const s = t.snapshot();
  assert.equal(s.confirmedBytes, 1000);
  assert.equal(s.temporaryBytes, 0);
  assertNeverExceedsTotal(s);
});

test('out-of-order receipts and reports never exceed the total', () => {
  const total = 10 * CHUNK + 1; // 11 chunks, last one is 1 byte
  const t = new ChunkedUploadProgress(total, CHUNK);
  const a = t.beginAttempt();
  // shuffled event stream: reports and receipts interleaved out of order
  const order = [5, 2, 10, 0, 7, 3, 9, 1, 8, 4, 6];
  for (const i of order) {
    t.reportTransfer(i, a, Math.floor(CHUNK / 2));
    assertNeverExceedsTotal(t.snapshot());
    t.reportTransfer(i, a, CHUNK * 5); // over-report, must be capped
    t.confirmChunk(i, a);
    assertNeverExceedsTotal(t.snapshot());
    t.confirmChunk(i, a); // duplicate receipt
    assertNeverExceedsTotal(t.snapshot());
  }
  const s = t.snapshot();
  assert.equal(s.confirmedBytes, total);
  assert.equal(s.temporaryBytes, 0);
  assert.equal(s.reliableRatio, 1);
  assert.equal(s.displayRatio, 1);
  assert.equal(s.confirmedChunks.length, 11);
});

test('last partial chunk is accounted with its real size', () => {
  const t = makeTracker(); // 2500 total, last chunk = 500
  assert.equal(t.chunkCount, 3);
  assert.equal(t.chunkSizeOf(2), 500);
  const a = t.beginAttempt();
  t.reportTransfer(2, a, 99999); // capped at 500, not 1000
  assert.equal(t.snapshot().temporaryBytes, 500);
  t.confirmChunk(2, a);
  assert.equal(t.snapshot().confirmedBytes, 500);
});

test('temporary progress of a chunk is discarded once it is confirmed', () => {
  const t = makeTracker();
  const a = t.beginAttempt();
  t.reportTransfer(0, a, 800);
  t.confirmChunk(0, a);
  const s = t.snapshot();
  assert.equal(s.confirmedBytes, 1000);
  assert.equal(s.temporaryBytes, 0);
  assertNeverExceedsTotal(s);
});

test('invalid arguments are rejected', () => {
  assert.throws(() => new ChunkedUploadProgress(0, CHUNK), RangeError);
  assert.throws(() => new ChunkedUploadProgress(100, 0), RangeError);
  const t = makeTracker();
  const a = t.beginAttempt();
  assert.throws(() => t.reportTransfer(3, a, 1), RangeError);
  assert.throws(() => t.confirmChunk(-1, a), RangeError);
});

let failures = 0;
for (const [name, fn] of tests) {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (err) {
    failures += 1;
    console.error(`FAIL - ${name}`);
    console.error(err);
  }
}
if (failures > 0) {
  console.error(`${failures} test(s) failed`);
  process.exit(1);
}
console.log(`${tests.length} tests passed`);
