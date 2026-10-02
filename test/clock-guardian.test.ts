import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FakeClocks } from '../src/clocks.ts';
import { ClockGuardian, type TripEvent } from '../src/clock-guardian.ts';
import { LeaseManager, LeaseRejectedError } from '../src/lease-manager.ts';

const TICK_MS = 100;
const ONE_HOUR_MS = 3_600_000;

function makeGuardian(options: { tearToleranceMs?: number } = {}) {
  const clocks = new FakeClocks();
  const leaseManager = new LeaseManager(clocks.monotonicNow);
  const guardian = new ClockGuardian({
    clocks,
    leaseManager,
    tearToleranceMs: options.tearToleranceMs ?? 250,
  });
  guardian.start();
  return { clocks, leaseManager, guardian };
}

function tickHealthy(clocks: FakeClocks, guardian: ClockGuardian, times = 1) {
  for (let i = 0; i < times; i++) {
    clocks.advance(TICK_MS);
    assert.equal(guardian.tick(), null, 'healthy tick must not trip');
  }
}

test('healthy clocks advancing in lockstep never trip the breaker', () => {
  const { clocks, leaseManager, guardian } = makeGuardian();
  leaseManager.acquire('lease-a', 'worker-1', 60_000);

  tickHealthy(clocks, guardian, 50);

  assert.equal(guardian.isTripped, false);
  assert.equal(guardian.mode, 'normal');
  assert.equal(leaseManager.activeCount(), 1);
});

test('small NTP jitter within tolerance does not trip', () => {
  const { clocks, guardian } = makeGuardian({ tearToleranceMs: 250 });
  for (let i = 0; i < 20; i++) {
    clocks.advance(TICK_MS);
    clocks.stepWall(50); // gentle forward NTP slew
    assert.equal(guardian.tick(), null);
  }
  assert.equal(guardian.isTripped, false);
});

test('ACCEPTANCE: wall clock set back 1 hour -> instant trip, all leases revoked, safe mode', () => {
  const { clocks, leaseManager, guardian } = makeGuardian();

  leaseManager.acquire('lease-a', 'worker-1', 60_000);
  leaseManager.acquire('lease-b', 'worker-2', 60_000);
  leaseManager.acquire('lease-c', 'worker-3', 60_000);
  tickHealthy(clocks, guardian, 5);
  assert.equal(leaseManager.activeCount(), 3);

  const trips: TripEvent[] = [];
  guardian.on('trip', (event) => trips.push(event));

  // Attacker / admin / VM restore: system time jumps back one hour.
  clocks.stepWall(-ONE_HOUR_MS);
  clocks.advanceMonotonicOnly(TICK_MS); // monotonic keeps honest pace

  // The very next tick must detect the tear.
  const event = guardian.tick();

  assert.ok(event, 'guardian must trip on the tick after the time change');
  assert.equal(event!.reason, 'WALL_CLOCK_BACKWARD');
  assert.equal(event!.wallDeltaMs, -ONE_HOUR_MS);
  assert.equal(event!.monoDeltaMs, TICK_MS);
  assert.ok(Math.abs(event!.tearMs) > ONE_HOUR_MS / 2);

  // All active leases force-revoked immediately.
  assert.equal(leaseManager.activeCount(), 0);
  assert.deepEqual(
    [...event!.revokedLeaseIds].sort(),
    ['lease-a', 'lease-b', 'lease-c'].sort(),
  );

  // Guardian degraded to safe mode; new leases are refused.
  assert.equal(guardian.mode, 'safe');
  assert.equal(guardian.isTripped, true);
  assert.equal(leaseManager.isAccepting, false);
  assert.throws(
    () => leaseManager.acquire('lease-d', 'worker-4', 60_000),
    LeaseRejectedError,
  );

  // Trip event emitted exactly once; later ticks stay tripped.
  assert.equal(trips.length, 1);
  clocks.advance(TICK_MS);
  assert.equal(guardian.tick(), event);
  assert.equal(trips.length, 1);
});

test('large forward NTP step trips with CLOCK_TEAR', () => {
  const { clocks, leaseManager, guardian } = makeGuardian();
  leaseManager.acquire('lease-a', 'worker-1', 60_000);
  tickHealthy(clocks, guardian, 3);

  clocks.advance(TICK_MS);
  clocks.stepWall(10_000); // NTP jumps 10s forward

  const event = guardian.tick();
  assert.ok(event);
  assert.equal(event!.reason, 'CLOCK_TEAR');
  assert.ok(event!.tearMs > 9_000);
  assert.equal(leaseManager.activeCount(), 0);
  assert.equal(guardian.mode, 'safe');
});

test('wall clock stalled (VM suspend) trips after stall threshold', () => {
  const { clocks, leaseManager, guardian } = makeGuardian();
  leaseManager.acquire('lease-a', 'worker-1', 60_000);
  tickHealthy(clocks, guardian, 3);

  // Wall clock frozen; monotonic advances 100ms per tick past the 1s threshold.
  let event: TripEvent | null = null;
  for (let i = 0; i < 10 && !event; i++) {
    clocks.advanceMonotonicOnly(TICK_MS);
    event = guardian.tick();
  }

  assert.ok(event, 'stalled wall clock must trip');
  assert.equal(event!.reason, 'WALL_CLOCK_STALLED');
  assert.equal(leaseManager.activeCount(), 0);
  assert.equal(guardian.mode, 'safe');
});

test('monotonic clock stalled while wall advances trips', () => {
  const { clocks, guardian } = makeGuardian();
  tickHealthy(clocks, guardian, 3);

  let event: TripEvent | null = null;
  for (let i = 0; i < 10 && !event; i++) {
    clocks.stepWall(TICK_MS); // wall advances, monotonic frozen
    event = guardian.tick();
  }

  assert.ok(event);
  assert.equal(event!.reason, 'MONOTONIC_STALLED');
  assert.equal(guardian.mode, 'safe');
});

test('lease expiry uses monotonic time, immune to wall-clock jumps', () => {
  const clocks = new FakeClocks();
  const leaseManager = new LeaseManager(clocks.monotonicNow);
  leaseManager.acquire('short', 'worker-1', 500);

  // Wall clock leaps a day forward: lease must NOT expire early.
  clocks.stepWall(86_400_000);
  assert.equal(leaseManager.activeCount(), 1);

  // Wall clock leaps a day backward: lease must NOT live forever.
  clocks.stepWall(-86_400_000);
  clocks.advanceMonotonicOnly(600);
  assert.equal(leaseManager.activeCount(), 0);
});
