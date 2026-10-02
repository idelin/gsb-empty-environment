import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ManualClock } from '../src/clock.ts';
import { LeaseManager, type LeaseEvent } from '../src/lease.ts';

const TTL_MS = 10_000;
const MAX_DRIFT_MS = 500;

function makeManager(maxRttMs = 0) {
  const clock = new ManualClock();
  const events: LeaseEvent[] = [];
  const manager = new LeaseManager(clock, {
    ttlMs: TTL_MS,
    maxClockDriftMs: MAX_DRIFT_MS,
    maxRttMs,
  });
  manager.onEvent((e) => events.push(e));
  return { clock, manager, events };
}

test('primary serves writes inside the safe window', () => {
  const { clock, manager } = makeManager();
  manager.acquire(1);

  clock.advance(9_000);
  manager.write('set x=1'); // t = 9.0s: allowed
  clock.advance(499);
  manager.write('set x=2'); // t = 9.499s: still allowed
  assert.equal(manager.isPrimary(), true);
});

test('acceptance: primary stops issuing writes at 9.5s (ttl 10s, drift 500ms)', () => {
  const { clock, manager, events } = makeManager();
  manager.acquire(1);

  clock.advance(9_500); // safe window (10s - 0.5s) exhausted
  assert.equal(manager.isPrimary(), false);
  assert.throws(() => manager.write('set x=3'), /fenced/);

  const demoted = events.find((e) => e.type === 'demoted');
  assert.equal(demoted?.reason, 'lease-expired');
  assert.equal(events.filter((e) => e.type === 'write').length, 0);
});

test('acceptance: 1s forward clock jump triggers emergency demotion (no split-brain)', () => {
  const { clock, manager, events } = makeManager();
  manager.acquire(1);

  clock.advance(3_000);
  manager.write('set a=1'); // healthy write at t = 3s
  assert.equal(manager.isPrimary(), true);

  clock.jumpWall(1_000); // wall clock jumps +1s (NTP step / admin change)
  assert.equal(manager.isPrimary(), false); // self-check demotes immediately
  assert.throws(() => manager.write('set a=2'), /fenced/);

  const demoted = events.find((e) => e.type === 'demoted');
  assert.equal(demoted?.reason, 'clock-jump');
  assert.ok(events.some((e) => e.type === 'write-rejected'));
});

test('drift within tolerance does not cause a false demotion', () => {
  const { clock, manager } = makeManager();
  manager.acquire(1);

  clock.advance(2_000);
  clock.jumpWall(400); // 400ms skew < 500ms max drift
  assert.equal(manager.isPrimary(), true);
  manager.write('set y=1');
});

test('backward clock jump beyond drift also fences the primary', () => {
  const { clock, manager, events } = makeManager();
  manager.acquire(1);

  clock.advance(1_000);
  clock.jumpWall(-800); // wall clock steps backwards by 800ms
  assert.equal(manager.isPrimary(), false);
  assert.equal(events.find((e) => e.type === 'demoted')?.reason, 'clock-jump');
});

test('round-trip latency shrinks the safe window', () => {
  const { clock, manager } = makeManager(200); // drift 500ms + rtt 200ms
  manager.acquire(1);

  clock.advance(9_299);
  assert.equal(manager.isPrimary(), true);
  clock.advance(1); // t = 9.3s = 10s - 0.5s - 0.2s
  assert.equal(manager.isPrimary(), false);
});

test('a demoted node stays fenced until it re-acquires a new term', () => {
  const { clock, manager } = makeManager();
  manager.acquire(1);
  clock.advance(10_000);
  assert.equal(manager.isPrimary(), false);

  manager.acquire(2); // new grant from quorum
  assert.equal(manager.isPrimary(), true);
  manager.write('set z=1');
});

test('safeTimeRemainingMs counts down the safe window', () => {
  const { clock, manager } = makeManager();
  manager.acquire(1);
  assert.equal(manager.safeTimeRemainingMs(), 9_500);
  clock.advance(4_500);
  assert.equal(manager.safeTimeRemainingMs(), 5_000);
  clock.advance(5_000);
  assert.equal(manager.safeTimeRemainingMs(), 0);
});
