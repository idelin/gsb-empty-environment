import { describe, test } from 'node:test';
import assert from 'node:assert/strict';

import {
  DEFAULT_BACKOFF,
  backoffDeadline,
  backoffDelayMs,
  resolveBackoff,
} from './backoff.js';

describe('backoffDelayMs', () => {
  test('grows exponentially from the initial interval', () => {
    assert.equal(backoffDelayMs(1, DEFAULT_BACKOFF), 10_000);
    assert.equal(backoffDelayMs(2, DEFAULT_BACKOFF), 20_000);
    assert.equal(backoffDelayMs(3, DEFAULT_BACKOFF), 40_000);
    assert.equal(backoffDelayMs(4, DEFAULT_BACKOFF), 80_000);
  });

  test('is capped at the maximum interval', () => {
    assert.equal(backoffDelayMs(20, DEFAULT_BACKOFF), 360_000);
  });

  test('honours custom multipliers and overrides', () => {
    const config = resolveBackoff({
      initialIntervalMs: 100,
      multiplier: 3,
      maxIntervalMs: 5_000,
    });
    assert.equal(backoffDelayMs(1, config), 100);
    assert.equal(backoffDelayMs(2, config), 300);
    assert.equal(backoffDelayMs(3, config), 900);
    assert.equal(backoffDelayMs(4, config), 2_700);
    assert.equal(backoffDelayMs(5, config), 5_000);
  });
});

describe('backoffDeadline', () => {
  test('returns an absolute timestamp relative to the failure instant', () => {
    assert.equal(backoffDeadline(1_000, 2, DEFAULT_BACKOFF), 21_000);
  });
});
