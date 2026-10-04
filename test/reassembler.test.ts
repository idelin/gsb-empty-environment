import test from 'node:test';
import assert from 'node:assert/strict';
import { Reassembler } from '../src/reassembler.ts';

const encoder = new TextEncoder();
const decoder = new TextDecoder();
const bytes = (s: string): Uint8Array => encoder.encode(s);
const text = (u: Uint8Array): string => decoder.decode(u);

function makeClock(start = 1_000) {
  let t = start;
  return {
    now: () => t,
    advance: (ms: number) => {
      t += ms;
    },
  };
}

test('empty message completes immediately and dedupes retransmits', () => {
  const clock = makeClock();
  const r = new Reassembler({ maxBytes: 1024, timeoutMs: 1000, now: clock.now });

  const res = r.addFragment({ id: 'm', totalLength: 0, offset: 0, data: new Uint8Array(0) });
  assert.equal(res.status, 'complete');
  if (res.status === 'complete') assert.equal(res.data.length, 0);

  const dup = r.addFragment({ id: 'm', totalLength: 0, offset: 0, data: new Uint8Array(0) });
  assert.equal(dup.status, 'duplicate');

  // After the tombstone times out, the same id may be reused.
  clock.advance(1001);
  const again = r.addFragment({ id: 'm', totalLength: 0, offset: 0, data: new Uint8Array(0) });
  assert.equal(again.status, 'complete');
});

test('out-of-order fragments reassemble into the original bytes', () => {
  const r = new Reassembler({ maxBytes: 64, timeoutMs: 1000 });
  const msg = bytes('abcdefghij');

  assert.equal(
    r.addFragment({ id: 'm', totalLength: 10, offset: 6, data: msg.subarray(6) }).status,
    'pending',
  );
  assert.equal(
    r.addFragment({ id: 'm', totalLength: 10, offset: 0, data: msg.subarray(0, 3) }).status,
    'pending',
  );
  const res = r.addFragment({ id: 'm', totalLength: 10, offset: 3, data: msg.subarray(3, 6) });
  assert.equal(res.status, 'complete');
  if (res.status === 'complete') assert.equal(text(res.data), 'abcdefghij');
});

test('overlapping fragments with consistent boundary bytes merge cleanly', () => {
  const r = new Reassembler({ maxBytes: 64, timeoutMs: 1000 });
  const msg = bytes('hello world'); // 11 bytes

  const first = r.addFragment({ id: 'm', totalLength: 11, offset: 0, data: msg.subarray(0, 6) });
  assert.equal(first.status, 'pending');
  if (first.status === 'pending') assert.equal(first.received, 6);

  // Overlaps [4, 6) with identical bytes ("o ").
  const res = r.addFragment({ id: 'm', totalLength: 11, offset: 4, data: msg.subarray(4) });
  assert.equal(res.status, 'complete');
  if (res.status === 'complete') assert.equal(text(res.data), 'hello world');
});

test('identical retransmits of the same range are accepted and counted once', () => {
  const r = new Reassembler({ maxBytes: 64, timeoutMs: 1000 });
  const msg = bytes('ABCDEFGH');

  r.addFragment({ id: 'm', totalLength: 8, offset: 0, data: msg.subarray(0, 4) });
  const again = r.addFragment({ id: 'm', totalLength: 8, offset: 0, data: msg.subarray(0, 4) });
  assert.equal(again.status, 'pending');
  if (again.status === 'pending') assert.equal(again.received, 4);

  const res = r.addFragment({ id: 'm', totalLength: 8, offset: 2, data: msg.subarray(2) });
  assert.equal(res.status, 'complete');
  if (res.status === 'complete') assert.equal(text(res.data), 'ABCDEFGH');
});

test('retransmits after completion are duplicates, not a new message', () => {
  const r = new Reassembler({ maxBytes: 64, timeoutMs: 1000 });
  const res = r.addFragment({ id: 'm', totalLength: 4, offset: 0, data: bytes('wxyz') });
  assert.equal(res.status, 'complete');

  const dup = r.addFragment({ id: 'm', totalLength: 4, offset: 0, data: bytes('wxyz') });
  assert.equal(dup.status, 'duplicate');

  // A newer generation is admitted once the previous one completed.
  const next = r.addFragment({ id: 'm', generation: 1, totalLength: 4, offset: 0, data: bytes('WXYZ') });
  assert.equal(next.status, 'complete');
  if (next.status === 'complete') assert.equal(text(next.data), 'WXYZ');
});

test('conflicting bytes in the same range quarantine the whole message', () => {
  const clock = makeClock();
  const r = new Reassembler({ maxBytes: 1024, timeoutMs: 1000, now: clock.now });

  r.addFragment({ id: 'm', totalLength: 8, offset: 0, data: bytes('ABCD') });
  const conflict = r.addFragment({ id: 'm', totalLength: 8, offset: 2, data: bytes('XY12') });
  assert.equal(conflict.status, 'rejected');
  if (conflict.status === 'rejected') assert.equal(conflict.reason, 'conflict');

  // The message is isolated: buffer freed, further fragments refused.
  assert.equal(r.isQuarantined('m'), true);
  assert.equal(r.stats().usedBytes, 0);
  const refused = r.addFragment({ id: 'm', totalLength: 8, offset: 4, data: bytes('EFGH') });
  assert.equal(refused.status, 'rejected');
  if (refused.status === 'rejected') assert.equal(refused.reason, 'quarantined');

  // Other messages are unaffected.
  const ok = r.addFragment({ id: 'other', totalLength: 4, offset: 0, data: bytes('wxyz') });
  assert.equal(ok.status, 'complete');

  // Quarantine lifts after the timeout; the same id can start over.
  clock.advance(1001);
  const fresh = r.addFragment({ id: 'm', totalLength: 4, offset: 0, data: bytes('QQQQ') });
  assert.equal(fresh.status, 'complete');
});

test('inconsistent totalLength quarantines the message', () => {
  const r = new Reassembler({ maxBytes: 1024, timeoutMs: 1000 });
  r.addFragment({ id: 'm', totalLength: 8, offset: 0, data: bytes('ABCD') });
  const res = r.addFragment({ id: 'm', totalLength: 9, offset: 4, data: bytes('EFGH') });
  assert.equal(res.status, 'rejected');
  if (res.status === 'rejected') assert.equal(res.reason, 'conflict');
  assert.equal(r.isQuarantined('m'), true);
});

test('late fragments from an old generation never mix into the new message', () => {
  const clock = makeClock();
  const r = new Reassembler({ maxBytes: 1024, timeoutMs: 1000, now: clock.now });

  // Generation 0 partially arrives.
  r.addFragment({ id: 'm', generation: 0, totalLength: 8, offset: 0, data: bytes('AAAA') });

  // A newer generation is locked out while generation 0 is still live.
  const locked = r.addFragment({ id: 'm', generation: 1, totalLength: 8, offset: 0, data: bytes('BBBB') });
  assert.equal(locked.status, 'rejected');
  if (locked.status === 'rejected') assert.equal(locked.reason, 'generation-locked');

  // After the timeout the new generation is admitted.
  clock.advance(1001);
  const started = r.addFragment({ id: 'm', generation: 1, totalLength: 8, offset: 0, data: bytes('BBBB') });
  assert.equal(started.status, 'pending');

  // A straggler from generation 0 arrives late and must be dropped.
  const stale = r.addFragment({ id: 'm', generation: 0, totalLength: 8, offset: 4, data: bytes('CCCC') });
  assert.equal(stale.status, 'stale');

  // Generation 1 completes unpolluted by the late fragment.
  const done = r.addFragment({ id: 'm', generation: 1, totalLength: 8, offset: 4, data: bytes('DDDD') });
  assert.equal(done.status, 'complete');
  if (done.status === 'complete') assert.equal(text(done.data), 'BBBBDDDD');
});

test('memory budget is bounded; least-recently-active messages are evicted', () => {
  const clock = makeClock();
  const r = new Reassembler({ maxBytes: 16, timeoutMs: 10_000, now: clock.now });

  // A single message larger than the budget can never be accepted.
  const tooBig = r.addFragment({ id: 'big', totalLength: 32, offset: 0, data: bytes('0123456789abcdef') });
  assert.equal(tooBig.status, 'rejected');
  if (tooBig.status === 'rejected') assert.equal(tooBig.reason, 'memory');

  // Two 8-byte messages fill the budget exactly.
  r.addFragment({ id: 'a', totalLength: 8, offset: 0, data: bytes('AAAA') });
  clock.advance(1);
  r.addFragment({ id: 'b', totalLength: 8, offset: 0, data: bytes('BBBB') });
  assert.equal(r.stats().usedBytes, 16);

  // A third message evicts the least-recently-active one ('a').
  clock.advance(1);
  r.addFragment({ id: 'c', totalLength: 8, offset: 0, data: bytes('CCCC') });
  assert.equal(r.stats().usedBytes, 16);
  assert.equal(r.has('a'), false);
  assert.equal(r.has('b'), true);
  assert.equal(r.has('c'), true);

  // Usage never exceeds the budget, whatever arrives.
  r.addFragment({ id: 'd', totalLength: 8, offset: 0, data: bytes('DDDD') });
  assert.ok(r.stats().usedBytes <= 16);
});

test('malformed fragments are rejected without touching state', () => {
  const r = new Reassembler({ maxBytes: 64, timeoutMs: 1000 });

  const overflowing = r.addFragment({ id: 'm', totalLength: 4, offset: 3, data: bytes('TOOLONG') });
  assert.equal(overflowing.status, 'rejected');
  if (overflowing.status === 'rejected') assert.equal(overflowing.reason, 'invalid');
  assert.equal(r.has('m'), false);

  const negative = r.addFragment({ id: 'm', totalLength: 4, offset: -1, data: bytes('x') });
  assert.equal(negative.status, 'rejected');

  const badEmpty = r.addFragment({ id: 'm', totalLength: 0, offset: 1, data: new Uint8Array(0) });
  assert.equal(badEmpty.status, 'rejected');
  assert.equal(r.has('m'), false);
});

test('sweep discards expired states and frees their memory', () => {
  const clock = makeClock();
  const r = new Reassembler({ maxBytes: 64, timeoutMs: 1000, now: clock.now });

  r.addFragment({ id: 'a', totalLength: 8, offset: 0, data: bytes('AAAA') });
  clock.advance(1);
  r.addFragment({ id: 'b', totalLength: 8, offset: 0, data: bytes('BBBB') });
  assert.equal(r.stats().usedBytes, 16);

  clock.advance(1000); // 'a' is now idle for 1001ms, 'b' for 1000ms
  assert.equal(r.sweep(), 1);
  assert.equal(r.has('a'), false);
  assert.equal(r.has('b'), true);
  assert.equal(r.stats().usedBytes, 8);
});
