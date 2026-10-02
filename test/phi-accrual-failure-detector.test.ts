import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PhiAccrualFailureDetector } from '../src/phi-accrual-failure-detector.ts';
import { FixedTimeoutDetector } from '../src/fixed-timeout-detector.ts';

const INTERVAL_PATTERN = [75, 125, 90, 110, 100, 80, 120, 95, 105, 100];

function makeDetector() {
  return new PhiAccrualFailureDetector({
    threshold: 8,
    minStdDeviationMs: 25,
    acceptableHeartbeatPauseMs: 100,
    firstHeartbeatMeanMs: 100,
    firstHeartbeatStdDeviationMs: 25,
  });
}

function feedWarmup(detector: { reportHeartbeat: (t: number) => void }, startAt: number, rounds: number): number {
  let now = startAt;
  detector.reportHeartbeat(now);
  for (let i = 0; i < rounds; i++) {
    now += INTERVAL_PATTERN[i % INTERVAL_PATTERN.length];
    detector.reportHeartbeat(now);
  }
  return now;
}

test('cold start: phi is 0 before any heartbeat and stays low right after the first one', () => {
  const detector = makeDetector();
  assert.equal(detector.phi(0), 0);
  assert.equal(detector.currentState, 'available');

  detector.reportHeartbeat(0);
  assert.ok(detector.phi(0) < 1e-9);
  assert.ok(detector.phi(120) < 1, `phi shortly after first heartbeat should be low, got ${detector.phi(120)}`);
  assert.equal(detector.isAvailable(120), true);
});

test('steady healthy traffic keeps phi near zero and node available', () => {
  const detector = makeDetector();
  const now = feedWarmup(detector, 0, 40);
  assert.ok(detector.phi(now + 50) < 0.5);
  assert.equal(detector.check(now + 50), 'available');
});

test('short jitter spike within historical variance does NOT trigger a false alarm', () => {
  const detector = makeDetector();
  const revoked: number[] = [];
  detector.on('lease-revoked', (e) => revoked.push(e.at));

  let now = feedWarmup(detector, 0, 40);

  const fixed = new FixedTimeoutDetector(250);
  let fixedNow = feedWarmup(fixed, 0, 40);
  assert.equal(fixedNow, now);

  const lateArrival = now + 300;
  const duringSilence = lateArrival - 1;

  assert.equal(detector.isAvailable(duringSilence), true);
  assert.ok(
    detector.phi(duringSilence) < 8,
    `phi during jitter spike should stay below threshold, got ${detector.phi(duringSilence)}`,
  );
  assert.equal(fixed.isAvailable(duringSilence), false, 'fixed timeout would falsely declare the node dead');

  now = lateArrival;
  detector.reportHeartbeat(now);
  fixedNow = lateArrival;
  fixed.reportHeartbeat(fixedNow);
  assert.equal(revoked.length, 0);
});

test('real network partition drives phi past the threshold and revokes the lease exactly once', () => {
  const detector = makeDetector();
  const events: string[] = [];
  detector.on('lease-revoked', () => events.push('revoked'));

  const now = feedWarmup(detector, 0, 40);

  assert.equal(detector.check(now + 330), 'available', 'still within learned variance + pause');
  assert.equal(detector.check(now + 350), 'unavailable', 'phi must cross threshold once silence is abnormal');

  for (let t = 400; t <= 2000; t += 100) {
    assert.equal(detector.check(now + t), 'unavailable');
  }
  assert.equal(events.length, 1, 'lease revocation fires exactly once, no flapping');
  assert.ok(detector.phi(now + 1000) >= 8);
});

test('phi grows monotonically as silence stretches during a partition', () => {
  const detector = makeDetector();
  const now = feedWarmup(detector, 0, 40);
  const samples = [100, 200, 300, 400, 600, 1000].map((d) => detector.phi(now + d));
  for (let i = 1; i < samples.length; i++) {
    assert.ok(samples[i] > samples[i - 1], `phi should increase: ${samples}`);
  }
});

test('heartbeats after a partition restore availability and re-issue the lease', () => {
  const detector = makeDetector();
  const restored: number[] = [];
  detector.on('lease-restored', (e) => restored.push(e.at));

  let now = feedWarmup(detector, 0, 40);
  detector.check(now + 1000);
  assert.equal(detector.currentState, 'unavailable');

  now += 1000;
  detector.reportHeartbeat(now);
  assert.equal(detector.currentState, 'available');
  assert.equal(restored.length, 1);
  assert.ok(detector.phi(now) < 1);
});

test('minStdDeviationMs floor prevents phi explosion when intervals are constant', () => {
  const detector = makeDetector();
  let now = 0;
  detector.reportHeartbeat(now);
  for (let i = 0; i < 30; i++) {
    now += 100;
    detector.reportHeartbeat(now);
  }

  const phi = detector.phi(now + 150);
  assert.ok(Number.isFinite(phi));
  assert.ok(phi < 8, `a 50ms extra delay over a constant rhythm must not kill the node, phi=${phi}`);
  assert.equal(detector.isAvailable(now + 150), true);
});

test('sampling window is bounded by maxSampleSize', () => {
  const detector = new PhiAccrualFailureDetector({ maxSampleSize: 10 });
  let now = 0;
  detector.reportHeartbeat(now);
  for (let i = 0; i < 25; i++) {
    now += 100;
    detector.reportHeartbeat(now);
  }
  assert.equal(detector.sampleCount, 10);
});
