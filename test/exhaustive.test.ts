import assert from 'node:assert/strict';
import test from 'node:test';
import { runExhaustive } from '../src/exhaustive.ts';

test('exhaustive: short-string patch pairs converge when conflict-free and conserve edits', () => {
  const stats = runExhaustive({ maxLength: 3 });
  assert.ok(stats.pairs > 100_000, `expected a large search space, got ${stats.pairs}`);
  assert.ok(stats.conflictFreePairs > 0);
  assert.ok(stats.conflictedPairs > 0);
  assert.ok(stats.editsUnpreserved > 0);
});
