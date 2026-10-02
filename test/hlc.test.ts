import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  HLCClock,
  ClockDriftError,
  LogicalOverflowError,
  MAX_LOGICAL,
  compare,
  pack,
  unpack,
  type Timestamp,
} from '../src/hlc.ts';

/** Deterministic, manually advanced physical clock for testing. */
class ManualClock {
  private current: number;
  constructor(start = 1_000_000) {
    this.current = start;
  }
  now = (): number => this.current;
  advance(ms: number): void {
    this.current += ms;
  }
  set(ms: number): void {
    this.current = ms;
  }
}

function assertMonotonic(timestamps: Timestamp[]): void {
  for (let i = 1; i < timestamps.length; i++) {
    assert.ok(
      compare(timestamps[i - 1], timestamps[i]) < 0,
      `timestamps[${i - 1}] must be strictly less than timestamps[${i}]`,
    );
  }
}

describe('HLCClock: basic monotonicity', () => {
  it('tracks the physical clock when it advances normally', () => {
    const clock = new ManualClock();
    const hlc = new HLCClock({ physicalClock: clock.now });

    const first = hlc.now();
    assert.equal(first.wallTime, 1_000_000);
    assert.equal(first.logical, 0);

    clock.advance(10);
    const second = hlc.now();
    assert.equal(second.wallTime, 1_000_010);
    assert.equal(second.logical, 0);
    assert.ok(compare(first, second) < 0);
  });

  it('increments the logical counter within the same physical millisecond', () => {
    const clock = new ManualClock();
    const hlc = new HLCClock({ physicalClock: clock.now });

    const timestamps = Array.from({ length: 5 }, () => hlc.now());
    assert.deepEqual(
      timestamps.map((t) => t.logical),
      [0, 1, 2, 3, 4],
    );
    assert.ok(timestamps.every((t) => t.wallTime === 1_000_000));
    assertMonotonic(timestamps);
  });
});

describe('HLCClock: physical clock stalls', () => {
  it('keeps timestamps strictly monotonic via the logical counter', () => {
    const clock = new ManualClock();
    const hlc = new HLCClock({ physicalClock: clock.now });

    const timestamps = Array.from({ length: 1000 }, () => hlc.now());
    assertMonotonic(timestamps);
    // Physical component frozen, logical component carries the ordering.
    assert.ok(timestamps.every((t) => t.wallTime === 1_000_000));
    assert.equal(timestamps.at(-1)!.logical, 999);
  });

  it('throws ClockDriftError once the stall exceeds epsilon', () => {
    const clock = new ManualClock();
    const hlc = new HLCClock({ physicalClock: clock.now, epsilon: 500 });

    hlc.now(); // anchors at physical time
    clock.set(1_000_000 - 501); // physical clock stuck 501ms behind the HLC
    assert.throws(() => hlc.now(), ClockDriftError);
  });
});

describe('HLCClock: physical clock jumps backwards', () => {
  it('never emits a timestamp that goes backwards', () => {
    const clock = new ManualClock();
    const hlc = new HLCClock({ physicalClock: clock.now });

    const before = Array.from({ length: 10 }, () => hlc.now());
    clock.set(1_000_000 - 100); // NTP-style backward correction of 100ms
    const after = Array.from({ length: 10 }, () => hlc.now());

    assertMonotonic([...before, ...after]);
    // HLC physical component holds at the last seen value while the real
    // clock is behind it.
    assert.ok(after.every((t) => t.wallTime === 1_000_000));
  });

  it('rejects backward jumps larger than epsilon', () => {
    const clock = new ManualClock();
    const hlc = new HLCClock({ physicalClock: clock.now, epsilon: 500 });

    hlc.now();
    clock.set(1_000_000 - 600);
    assert.throws(() => hlc.now(), (err: unknown) => {
      assert.ok(err instanceof ClockDriftError);
      assert.equal(err.drift, 600);
      assert.equal(err.epsilon, 500);
      return true;
    });
  });
});

describe('HLCClock: physical clock jumps forwards', () => {
  it('smoothly catches up and resets the logical counter', () => {
    const clock = new ManualClock();
    const hlc = new HLCClock({ physicalClock: clock.now });

    hlc.now();
    hlc.now();
    assert.equal(hlc.latest.logical, 1);

    clock.advance(60_000); // physical clock leaps a minute ahead
    const caught = hlc.now();
    assert.equal(caught.wallTime, 1_060_000);
    assert.equal(caught.logical, 0); // no artificial inflation, back on physical time
  });

  it('never emits timestamps detached from physical reality', () => {
    const clock = new ManualClock();
    const hlc = new HLCClock({ physicalClock: clock.now, epsilon: 500 });

    for (let i = 0; i < 10_000; i++) {
      const ts = hlc.now();
      const drift = ts.wallTime - clock.now();
      assert.ok(drift >= 0 && drift <= 500, `drift ${drift}ms out of bounds`);
      if (i % 7 === 0) clock.advance(3);
      if (i % 13 === 0) clock.set(clock.now() - 2); // small backward jitters
    }
  });
});

describe('HLCClock: causal consistency across nodes', () => {
  it('update() produces a timestamp greater than both local and remote', () => {
    const clockA = new ManualClock(1_000_000);
    const clockB = new ManualClock(1_000_050); // node B is 50ms ahead
    const nodeA = new HLCClock({ physicalClock: clockA.now });
    const nodeB = new HLCClock({ physicalClock: clockB.now });

    const sentByB = nodeB.now();
    const localA = nodeA.now();
    const merged = nodeA.update(sentByB);

    assert.ok(compare(merged, sentByB) > 0);
    assert.ok(compare(merged, localA) > 0);
    // Node A adopts B's physical component since it is ahead.
    assert.equal(merged.wallTime, sentByB.wallTime);
    assert.equal(merged.logical, sentByB.logical + 1);
  });

  it('a message round trip preserves happens-before ordering', () => {
    const clockA = new ManualClock(1_000_000);
    const clockB = new ManualClock(1_000_000);
    const nodeA = new HLCClock({ physicalClock: clockA.now });
    const nodeB = new HLCClock({ physicalClock: clockB.now });

    const a1 = nodeA.now(); // A sends
    const b1 = nodeB.update(a1); // B receives
    const b2 = nodeB.now(); // B replies
    const a2 = nodeA.update(b2); // A receives the reply

    assert.ok(compare(a1, b1) < 0);
    assert.ok(compare(b1, b2) < 0);
    assert.ok(compare(b2, a2) < 0);
  });

  it('concurrent events on different nodes stay comparable after merge', () => {
    const clockA = new ManualClock(1_000_000);
    const clockB = new ManualClock(1_000_000);
    const nodeA = new HLCClock({ physicalClock: clockA.now });
    const nodeB = new HLCClock({ physicalClock: clockB.now });

    const a = nodeA.now();
    const b = nodeB.now();
    assert.deepEqual(a, b); // concurrent, identical components

    const merged = nodeA.update(b);
    assert.ok(compare(merged, a) > 0);
    assert.ok(compare(merged, b) > 0);
  });
});

describe('HLCClock: logical counter overflow', () => {
  it('throws LogicalOverflowError beyond 65535 events in one millisecond', () => {
    const clock = new ManualClock();
    const hlc = new HLCClock({ physicalClock: clock.now });

    for (let i = 0; i <= MAX_LOGICAL; i++) hlc.now();
    assert.throws(() => hlc.now(), LogicalOverflowError);
  });
});

describe('Timestamp encoding', () => {
  it('pack/unpack round-trips losslessly', () => {
    const ts: Timestamp = { wallTime: 1_759_000_000_123, logical: 42 };
    assert.deepEqual(unpack(pack(ts)), ts);
  });

  it('packed ordering matches lexicographic ordering', () => {
    const samples: Timestamp[] = [
      { wallTime: 100, logical: 0 },
      { wallTime: 100, logical: 1 },
      { wallTime: 100, logical: MAX_LOGICAL },
      { wallTime: 101, logical: 0 },
      { wallTime: 1_759_000_000_000, logical: 7 },
    ];
    for (let i = 1; i < samples.length; i++) {
      assert.ok(compare(samples[i - 1], samples[i]) < 0);
      assert.ok(pack(samples[i - 1]) < pack(samples[i]));
    }
  });
});

describe('HLCClock: real wall clock smoke test', () => {
  it('produces timestamps close to Date.now with default options', () => {
    const hlc = new HLCClock();
    const before = Date.now();
    const ts = hlc.now();
    const after = Date.now();
    assert.ok(ts.wallTime >= before && ts.wallTime <= after);
    assert.equal(ts.logical, 0);
  });
});
