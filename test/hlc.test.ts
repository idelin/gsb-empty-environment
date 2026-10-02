import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  HybridLogicalClock,
  ClockDriftError,
  compare,
  equals,
  toString,
  fromString,
  type HLCTimestamp,
} from '../src/hlc.ts';

/** A manually controllable physical clock for deterministic tests. */
class FakeClock {
  t: number;
  constructor(start: number) {
    this.t = start;
  }
  now = (): number => this.t;
  advance(ms: number): void {
    this.t += ms;
  }
  set(ms: number): void {
    this.t = ms;
  }
}

const T0 = 1_700_000_000_000; // fixed epoch base for tests

test('normal operation: timestamps track the physical clock and are strictly monotonic', () => {
  const clock = new FakeClock(T0);
  const hlc = new HybridLogicalClock({ now: clock.now });

  let prev: HLCTimestamp | undefined;
  for (let i = 0; i < 1000; i++) {
    const ts = hlc.now();
    assert.equal(ts.wallTime, clock.t, 'wallTime follows the physical clock');
    assert.equal(ts.logical, 0, 'logical stays 0 while physical clock advances');
    if (prev) assert.ok(compare(prev, ts) < 0, 'strictly increasing');
    prev = ts;
    clock.advance(1);
  }
});

test('burst within the same physical millisecond: logical counter increments', () => {
  const clock = new FakeClock(T0);
  const hlc = new HybridLogicalClock({ now: clock.now });

  let prev = hlc.now();
  for (let i = 1; i <= 5000; i++) {
    const ts = hlc.now();
    assert.equal(ts.wallTime, T0);
    assert.equal(ts.logical, i);
    assert.ok(compare(prev, ts) < 0);
    prev = ts;
  }
});

test('physical clock stall: logical part keeps output strictly monotonic', () => {
  const clock = new FakeClock(T0);
  const hlc = new HybridLogicalClock({ now: clock.now });

  const first = hlc.now();
  clock.advance(10);
  hlc.now();
  // Physical clock stalls (NTP freeze / VM pause on read path).
  let prev = hlc.now();
  for (let i = 0; i < 100; i++) {
    const ts = hlc.now();
    assert.ok(compare(prev, ts) < 0, 'monotone while physical clock is stalled');
    assert.ok(ts.wallTime > first.wallTime);
    prev = ts;
  }
});

test('short backward jump of the physical clock: output never regresses', () => {
  const clock = new FakeClock(T0);
  const hlc = new HybridLogicalClock({ now: clock.now });

  clock.advance(100);
  const before = hlc.now();

  // NTP correction / leap smear pulls the physical clock back 50ms.
  clock.set(clock.t - 50);

  let prev = hlc.now();
  assert.ok(compare(before, prev) < 0, 'no regression after backward jump');
  assert.equal(prev.wallTime, before.wallTime, 'wallTime holds at last observed maximum');
  assert.equal(prev.logical, before.logical + 1, 'logical absorbs the regression');

  for (let i = 0; i < 50; i++) {
    const ts = hlc.now();
    assert.ok(compare(prev, ts) < 0);
    prev = ts;
  }

  // Once the physical clock catches back up, logical resets to 0.
  clock.set(before.wallTime + 10);
  const caught = hlc.now();
  assert.equal(caught.wallTime, before.wallTime + 10);
  assert.equal(caught.logical, 0);
});

test('forward jump of the physical clock: HLC smoothly catches up, logical resets', () => {
  const clock = new FakeClock(T0);
  const hlc = new HybridLogicalClock({ now: clock.now });

  const before = hlc.now();
  clock.advance(60_000); // physical clock leaps forward one minute

  const after = hlc.now();
  assert.equal(after.wallTime, clock.t, 'HLC catches up to physical time immediately');
  assert.equal(after.logical, 0, 'logical resets on physical catch-up');
  assert.ok(compare(before, after) < 0);
});

test('drift bound: emitted timestamps never lead the physical clock by more than epsilon', () => {
  const clock = new FakeClock(T0);
  const epsilon = 500;
  const hlc = new HybridLogicalClock({ now: clock.now, epsilon });

  clock.advance(epsilon); // HLC wallTime reaches T0 + 500
  const atBound = hlc.now();
  assert.equal(atBound.wallTime - clock.t, 0);

  // Physical clock falls 499ms behind the HLC wallTime: still within epsilon.
  clock.set(atBound.wallTime - (epsilon - 1));
  const stillOk = hlc.now();
  assert.ok(stillOk.wallTime - clock.t <= epsilon);

  // Physical clock falls more than epsilon behind: must refuse rather than
  // emit a timestamp detached from physical reality.
  clock.set(atBound.wallTime - (epsilon + 1));
  assert.throws(() => hlc.now(), (err: unknown) => {
    assert.ok(err instanceof ClockDriftError);
    assert.equal((err as ClockDriftError).epsilon, epsilon);
    return true;
  });
});

test('default epsilon is 500ms', () => {
  const hlc = new HybridLogicalClock();
  assert.equal(hlc.epsilon, 500);
});

test('invalid epsilon is rejected', () => {
  assert.throws(() => new HybridLogicalClock({ epsilon: -1 }), RangeError);
  assert.throws(() => new HybridLogicalClock({ epsilon: NaN }), RangeError);
});

test('receive event: causal ordering across nodes', () => {
  const clockA = new FakeClock(T0);
  const clockB = new FakeClock(T0 + 10); // B is 10ms ahead
  const nodeA = new HybridLogicalClock({ now: clockA.now });
  const nodeB = new HybridLogicalClock({ now: clockB.now });

  // A sends a message to B.
  const sent = nodeA.now();
  const received = nodeB.update(sent);
  assert.ok(compare(sent, received) < 0, 'receive is causally after send');

  // B replies to A; A's next event must be causally after B's send.
  const reply = nodeB.now();
  const afterReply = nodeA.update(reply);
  assert.ok(compare(reply, afterReply) < 0);
  assert.ok(compare(sent, afterReply) < 0, 'transitive causality holds');
});

test('receive event with remote clock far ahead: adopts remote wallTime', () => {
  const clockA = new FakeClock(T0);
  const clockB = new FakeClock(T0 + 100);
  const nodeA = new HybridLogicalClock({ now: clockA.now });
  const nodeB = new HybridLogicalClock({ now: clockB.now });

  const remote = nodeB.now();
  const merged = nodeA.update(remote);
  assert.equal(merged.wallTime, remote.wallTime, 'adopts the larger remote wallTime');
  assert.equal(merged.logical, remote.logical + 1);
});

test('receive event with remote timestamp beyond epsilon: rejected', () => {
  const clock = new FakeClock(T0);
  const hlc = new HybridLogicalClock({ now: clock.now, epsilon: 500 });

  const fromFuture: HLCTimestamp = { wallTime: T0 + 501, logical: 0 };
  assert.throws(() => hlc.update(fromFuture), ClockDriftError);

  const atEdge: HLCTimestamp = { wallTime: T0 + 500, logical: 0 };
  assert.doesNotThrow(() => hlc.update(atEdge));
});

test('receive event with stale remote timestamp: local clock still advances monotonically', () => {
  const clock = new FakeClock(T0);
  const hlc = new HybridLogicalClock({ now: clock.now });

  clock.advance(1000);
  const local = hlc.now();
  const stale: HLCTimestamp = { wallTime: T0, logical: 3 };
  const merged = hlc.update(stale);
  assert.equal(merged.wallTime, local.wallTime, 'ignores stale remote wallTime');
  assert.ok(compare(local, merged) < 0);
});

test('compare/equals implement a total order', () => {
  const a: HLCTimestamp = { wallTime: 100, logical: 0 };
  const b: HLCTimestamp = { wallTime: 100, logical: 1 };
  const c: HLCTimestamp = { wallTime: 101, logical: 0 };
  assert.ok(compare(a, b) < 0);
  assert.ok(compare(b, c) < 0);
  assert.ok(compare(a, c) < 0);
  assert.ok(compare(b, a) > 0);
  assert.equal(compare(a, { ...a }), 0);
  assert.ok(equals(a, { wallTime: 100, logical: 0 }));
  assert.ok(!equals(a, b));
});

test('string serialization round-trips and preserves ordering', () => {
  const clock = new FakeClock(T0);
  const hlc = new HybridLogicalClock({ now: clock.now });

  const stamps: HLCTimestamp[] = [];
  for (let i = 0; i < 100; i++) {
    stamps.push(hlc.now());
    if (i % 10 === 0) clock.advance(3);
  }
  for (const ts of stamps) {
    assert.ok(equals(fromString(toString(ts)), ts), 'round-trip preserves value');
  }
  const strings = stamps.map(toString);
  const sorted = [...strings].sort();
  assert.deepEqual(strings, sorted, 'lexicographic order matches causal order');
});

test('last() reports the latest observed timestamp without advancing the clock', () => {
  const clock = new FakeClock(T0);
  const hlc = new HybridLogicalClock({ now: clock.now });
  const ts = hlc.now();
  assert.ok(equals(hlc.last(), ts));
  const next = hlc.now();
  assert.ok(compare(ts, next) < 0, 'last() did not consume a tick');
});

test('real wall clock: generated timestamps stay close to Date.now()', () => {
  const hlc = new HybridLogicalClock();
  const ts = hlc.now();
  const drift = Math.abs(ts.wallTime - Date.now());
  assert.ok(drift <= 100, `drift ${drift}ms should be tiny on a healthy clock`);
});
