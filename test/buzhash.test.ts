import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Buzhash } from '../src/buzhash.ts';
import { randomBytes } from '../src/prng.ts';

test('rolling update matches from-scratch window hash at every position', () => {
  const W = 64;
  const data = randomBytes(4096, 1);
  const roller = new Buzhash(W);
  for (let i = 0; i < data.length; i++) {
    const h = roller.update(data[i]);
    if (i + 1 >= W) {
      const window = data.subarray(i + 1 - W, i + 1);
      assert.equal(h, roller.hashWindow(window), `mismatch at byte ${i}`);
    }
  }
});

test('hash is deterministic across instances (verifiable)', () => {
  const data = randomBytes(512, 2);
  const a = new Buzhash(64);
  const b = new Buzhash(64);
  for (const byte of data) {
    assert.equal(a.update(byte), b.update(byte));
  }
});

test('reset restores initial state', () => {
  const data = randomBytes(256, 3);
  const h = new Buzhash(64);
  for (const byte of data) h.update(byte);
  h.reset();
  const fresh = new Buzhash(64);
  for (const byte of data) {
    assert.equal(h.update(byte), fresh.update(byte));
  }
});
