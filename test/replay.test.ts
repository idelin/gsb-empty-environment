import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runReplay } from '../examples/replay.ts';

test('replayable edit script matches the recorded golden output', () => {
  const golden = readFileSync(new URL('../examples/replay.output.txt', import.meta.url), 'utf8');
  assert.equal(`${runReplay()}\n`, golden);
});
