import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FixedTimeoutDetector } from '../src/fixed-timeout-detector.ts';

test('fixed timeout baseline: alive within timeout, dead beyond it', () => {
  const detector = new FixedTimeoutDetector(200);
  assert.equal(detector.isAvailable(0), false);

  detector.reportHeartbeat(1000);
  assert.equal(detector.isAvailable(1100), true);
  assert.equal(detector.isAvailable(1200), true);
  assert.equal(detector.isAvailable(1201), false);
});
